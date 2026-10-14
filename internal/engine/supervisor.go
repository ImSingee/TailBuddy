package engine

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ImSingee/TailBuddy/internal/config"
)

const supervisorFlag = "--tailbuddy-supervisor"

// SupervisorMain must run before initializing the GUI. Call as:
// if handled, code := engine.SupervisorMain(os.Args[1:]); handled { os.Exit(code) }
// The supervisor is a small, separate process whose sole purpose is to reap the
// exact child it launches when the UI lifetime pipe closes. It does not accept a
// PID to kill and never attaches to an existing daemon.
func SupervisorMain(args []string) (handled bool, exitCode int) {
	if len(args) == 0 || args[0] != supervisorFlag {
		return false, 0
	}
	if len(args) != 4 {
		return true, 64
	}
	helper, dir, parentPID := args[1], args[2], args[3]
	expected, err := strconv.Atoi(parentPID)
	if err != nil || expected <= 1 || os.Getppid() != expected || syscall.Getpgrp() != os.Getpid() {
		return true, 64
	}
	lifetime := os.NewFile(3, "tailbuddy-parent-lifetime")
	lock := os.NewFile(4, "tailbuddy-exclusive-lock")
	if lifetime == nil || lock == nil {
		return true, 64
	}
	defer lifetime.Close()
	defer lock.Close()
	fi, err := lifetime.Stat()
	if err != nil || fi.Mode()&os.ModeNamedPipe == 0 {
		return true, 64
	}
	lockInfo, err := lock.Stat()
	if err != nil || !lockInfo.Mode().IsRegular() {
		return true, 64
	}
	if err := config.CheckPrivateTree(dir); err != nil {
		return true, 65
	}
	if err := validateHelper(helper); err != nil {
		return true, 65
	}
	syscall.Umask(0077)
	cmd := exec.Command(helper, helperArguments(dir)...)
	cmd.Env = helperEnvironment()
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// Inherit our private process group. The GUI retains its unreaped leader
	// until group cleanup, including if this supervisor dies unexpectedly.
	if err := cmd.Start(); err != nil {
		return true, 69
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	parentGone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, lifetime); close(parentGone) }()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	defer signal.Stop(signals)
	select {
	case err := <-exited:
		if err != nil {
			return true, 70
		}
		return true, 0
	case <-parentGone:
	case <-signals:
	}
	// The process group was created by this supervisor and is still represented by
	// an unreaped child. No stale PID file or process-name matching is involved.
	_ = syscall.Kill(-syscall.Getpgrp(), syscall.SIGTERM)
	timer := time.NewTimer(4 * time.Second)
	defer timer.Stop()
	select {
	case <-exited:
		return true, 0
	case <-timer.C:
		_ = cmd.Process.Kill()
		<-exited
		return true, 0
	}
}

func helperArguments(dir string) []string {
	return []string{
		"--tun=userspace-networking",
		"--state=" + filepath.Join(dir, "tailscaled.state"),
		"--statedir=" + dir,
		"--socket=" + filepath.Join(dir, "tailscaled.sock"),
		"--config=" + filepath.Join(dir, "boot.json"),
		"--port=0", "--no-logs-no-support",
	}
}

func helperEnvironment() []string {
	// Do not pass proxy credentials, auth keys, environment knobs, injected shared
	// libraries, or caller-specified Tailscale behavior to the managed daemon.
	out := []string{"PATH=/usr/bin:/bin:/usr/sbin:/sbin", "TS_NO_LOGS_NO_SUPPORT=true"}
	for _, k := range []string{"HOME", "USER", "LOGNAME", "TMPDIR", "LANG"} {
		if v, ok := os.LookupEnv(k); ok && !strings.ContainsRune(v, '\x00') {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func validateHelper(path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("helper path must be absolute")
	}
	fi, err := os.Lstat(path)
	if err != nil {
		return errors.New("bundled tailscaled helper is missing")
	}
	if !fi.Mode().IsRegular() || fi.Mode()&0111 == 0 || fi.Mode().Perm()&0022 != 0 {
		return errors.New("tailscaled helper must be a non-shared, executable regular file")
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || (int(st.Uid) != os.Geteuid() && st.Uid != 0) {
		return errors.New("tailscaled helper has an unexpected owner")
	}
	return nil
}
