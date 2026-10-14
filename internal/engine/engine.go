// Package engine manages a dedicated, unprivileged userspace tailscaled child.
// It never opens a TUN device, exposes an outbound proxy, or touches system DNS.
package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/ImSingee/TailBuddy/internal/config"
)

var ErrNotRunning = errors.New("TailBuddy is disconnected")
var ErrConsentRequired = errors.New("explicit inbound-access and credential-storage consent is required before connecting")

// Options contains deployment locations, never command-line overrides. The
// bundled helper must be the version built and verified by the application.
type Options struct {
	HelperPath   string
	StateDir     string
	PollInterval time.Duration
}

// Snapshot separates the daemon state, advertised configuration, and control
// plane primary-route observation. PrimaryRoutes does not prove end-to-end
// reachability and absence is not proof that a redundant route is unapproved.
type Snapshot struct {
	Active               bool
	Running              bool
	Connected            bool
	BackendState         string
	Hostname             string
	DNSName              string
	TailscaleIPs         []string
	AdvertisedRoutes     []string
	ApprovedRoutes       []string
	ApprovalKnown        bool
	Health               []string
	LoginURL             string `json:"-"`
	LastError            string
	ReachabilityVerified bool
	UpdatedAt            time.Time
	RestartCount         int
}

type Engine struct {
	opts   Options
	mu     sync.Mutex
	opMu   sync.Mutex
	apiMu  sync.Mutex
	cfg    config.Config
	snap   Snapshot
	cancel context.CancelFunc
	done   chan struct{}
	api    *localAPI
}

type process struct {
	cmd      *exec.Cmd
	lifetime *os.File
	exited   chan error
}

func New(opts Options) (*Engine, error) {
	if opts.StateDir == "" {
		var err error
		opts.StateDir, err = config.DefaultStateDir()
		if err != nil {
			return nil, err
		}
	}
	if !filepath.IsAbs(opts.StateDir) {
		return nil, errors.New("state directory must be absolute")
	}
	opts.StateDir = filepath.Clean(opts.StateDir)
	if len(filepath.Join(opts.StateDir, "tailscaled.sock")) > 100 {
		return nil, errors.New("state directory path is too long for a private Unix socket")
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = 3 * time.Second
	}
	return &Engine{opts: opts, cfg: config.Default(), snap: Snapshot{BackendState: "Stopped"}, api: newLocalAPI(filepath.Join(opts.StateDir, "tailscaled.sock"))}, nil
}

// Start requires explicit consent and returns after the supervised process has
// launched. Call Status for asynchronous login/readiness errors. Cancelling ctx
// has the same exposure-stopping effect as Stop; use an application-lifetime ctx.
func (e *Engine) Start(ctx context.Context, c config.Config) error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	c, err := config.Validate(c)
	if err != nil {
		return err
	}
	if !c.HasConsent() {
		return ErrConsentRequired
	}
	if len(c.AdvertiseRoutes) > 0 && !c.SubnetConsentAccepted {
		return errors.New("explicit subnet exposure consent is required")
	}
	e.mu.Lock()
	active := e.done != nil
	e.mu.Unlock()
	if active {
		return errors.New("TailBuddy is already starting or connected")
	}
	if err := validateHelper(e.opts.HelperPath); err != nil {
		return err
	}
	if err := config.CheckPrivateTree(e.opts.StateDir); err != nil {
		return err
	}
	lock, err := acquireLock(e.opts.StateDir)
	if err != nil {
		return err
	}
	fail := func(err error) error { lock.Close(); return err }
	if err := config.WritePrivateFile(e.opts.StateDir, "boot.json", safeBootConfig()); err != nil {
		return fail(err)
	}
	if err := config.Save(e.opts.StateDir, c); err != nil {
		return fail(err)
	}
	p, err := e.launch(lock)
	if err != nil {
		return fail(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	e.mu.Lock()
	e.cfg = c
	e.cancel = cancel
	e.done = done
	e.snap = Snapshot{Running: true, BackendState: "Starting", Hostname: c.Hostname, AdvertisedRoutes: append([]string{}, c.AdvertiseRoutes...), UpdatedAt: time.Now()}
	e.mu.Unlock()
	go e.run(runCtx, done, lock, p)
	return nil
}

func acquireLock(dir string) (*os.File, error) {
	path := filepath.Join(dir, "engine.lock")
	if err := config.CheckPrivateFile(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, errors.New("cannot open exclusive engine lock")
	}
	if err := config.CheckPrivateFile(path); err != nil {
		f.Close()
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("another TailBuddy instance is using this state directory")
	}
	return f, nil
}

func (e *Engine) launch(lock *os.File) (*process, error) {
	socket := e.api.socket
	if _, err := os.Lstat(socket); err == nil {
		if err := checkSocket(socket, true); err != nil {
			return nil, err
		}
		c, err := net.DialTimeout("unix", socket, 200*time.Millisecond)
		if err == nil {
			c.Close()
			return nil, errors.New("a daemon is already listening on TailBuddy's private socket")
		}
		if err := os.Remove(socket); err != nil {
			return nil, errors.New("cannot remove stale private socket")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("cannot inspect private socket")
	}
	if err := config.CheckPrivateTree(e.opts.StateDir); err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, errors.New("cannot find TailBuddy supervisor executable")
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, supervisorFlag, e.opts.HelperPath, e.opts.StateDir, strconv.Itoa(os.Getpid()))
	cmd.ExtraFiles = []*os.File{reader, lock}
	// The supervisor needs no inherited caller knobs. It receives exact locations
	// through positional arguments and credentials only through the private store.
	cmd.Env = helperEnvironment()
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		reader.Close()
		writer.Close()
		return nil, errors.New("cannot launch private helper supervisor")
	}
	reader.Close()
	p := &process{cmd: cmd, lifetime: writer, exited: make(chan error, 1)}
	go func() {
		observeErr := waitForExitWithoutReaping(cmd.Process.Pid)
		// The group leader is still an unreaped child here. Its numeric identity
		// cannot be reused, so this never targets an unrelated process group.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		waitErr := cmd.Wait()
		if observeErr != nil {
			waitErr = errors.New("cannot observe managed supervisor safely")
		}
		p.exited <- waitErr
		close(p.exited)
	}()
	return p, nil
}

func (e *Engine) run(ctx context.Context, done chan struct{}, lock *os.File, p *process) {
	var terminal error
	defer func() {
		// Retain the lock until the supervised child has definitely been reaped.
		lock.Close()
		e.mu.Lock()
		e.cancel = nil
		e.done = nil
		e.snap.Running = false
		e.snap.Connected = false
		e.snap.LoginURL = ""
		e.snap.TailscaleIPs = nil
		e.snap.ApprovedRoutes = nil
		e.snap.ApprovalKnown = false
		e.snap.BackendState = "Stopped"
		e.snap.UpdatedAt = time.Now()
		if terminal != nil {
			e.snap.BackendState = "Error"
			e.snap.LastError = terminal.Error()
		}
		e.mu.Unlock()
		close(done)
	}()
	restarts := 0
	stableSince := time.Now()
	for {
		err := e.serveProcess(ctx, p)
		// Closing the pipe also covers startup/configuration failures, without relying
		// on a working LocalAPI. The supervisor escalates TERM to KILL after 4 seconds.
		p.lifetime.Close()
		<-p.exited
		if ctx.Err() != nil {
			return
		}
		if time.Since(stableSince) > 5*time.Minute {
			restarts = 0
			stableSince = time.Now()
		}
		if errors.Is(err, errUnsafePreferences) {
			terminal = err
			return
		}
		if restarts >= 3 {
			terminal = errors.New("managed daemon repeatedly failed; disconnected safely. Reconnect to retry")
			return
		}
		restarts++
		e.mu.Lock()
		e.snap.Running = false
		e.snap.Connected = false
		e.snap.BackendState = "Reconnecting"
		e.snap.LoginURL = ""
		e.snap.LastError = err.Error()
		e.snap.RestartCount = restarts
		e.mu.Unlock()
		timer := time.NewTimer(time.Duration(1<<(restarts-1)) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		var launchErr error
		e.apiMu.Lock()
		p, launchErr = e.launch(lock)
		if launchErr != nil {
			e.apiMu.Unlock()
			terminal = launchErr
			return
		}
		e.mu.Lock()
		e.snap.Running = true
		e.snap.BackendState = "Starting"
		e.mu.Unlock()
		e.apiMu.Unlock()
	}
}

var errUnsafePreferences = errors.New("daemon preferences violate TailBuddy's networking safety policy; disconnected")

func (e *Engine) serveProcess(ctx context.Context, p *process) error {
	readyCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	for {
		_, err := e.api.status(readyCtx)
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-readyCtx.Done():
			return errors.New("managed daemon did not start its private LocalAPI")
		case <-p.exited:
			return errors.New("managed daemon exited before startup")
		case <-time.After(100 * time.Millisecond):
		}
	}
	e.apiMu.Lock()
	e.mu.Lock()
	c := e.cfg
	e.mu.Unlock()
	err := e.api.configure(ctx, c)
	e.apiMu.Unlock()
	if err != nil {
		return fmt.Errorf("cannot safely configure daemon: %w", err)
	}
	if err := e.refresh(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(e.opts.PollInterval)
	defer ticker.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
			_ = e.api.stop(stopCtx)
			cancel()
			return ctx.Err()
		case <-p.exited:
			return errors.New("managed daemon stopped unexpectedly")
		case <-ticker.C:
			if err := e.refresh(ctx); err != nil {
				if errors.Is(err, errUnsafePreferences) {
					return err
				}
				failures++
				e.mu.Lock()
				e.snap.Connected = false
				e.snap.LastError = "private daemon status unavailable; reconnecting if it persists"
				e.mu.Unlock()
				if failures >= 3 {
					return errors.New("managed daemon stopped responding")
				}
			} else {
				failures = 0
			}
		}
	}
}

func (e *Engine) refresh(ctx context.Context) error {
	e.apiMu.Lock()
	defer e.apiMu.Unlock()
	s, err := e.api.status(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	c := e.cfg
	e.mu.Unlock()
	if err := e.api.verifyPrefs(ctx, c); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.snap.Running = true
	e.snap.BackendState = s.BackendState
	e.snap.Connected = s.BackendState == "Running" && s.Self != nil && s.Self.Online
	e.snap.TailscaleIPs = append([]string{}, s.TailscaleIPs...)
	e.snap.Health = safeHealth(s.Health)
	e.snap.LastError = ""
	e.snap.LoginURL = ""
	e.snap.ApprovedRoutes = nil
	e.snap.ApprovalKnown = false
	if s.Self != nil {
		e.snap.Hostname = s.Self.HostName
		e.snap.DNSName = s.Self.DNSName
		if s.Self.PrimaryRoutes != nil {
			e.snap.ApprovalKnown = true
			e.snap.ApprovedRoutes = append([]string{}, (*s.Self.PrimaryRoutes)...)
		}
	}
	if s.AuthURL != "" {
		if loginURL, err := ValidateLoginURL(s.AuthURL); err == nil {
			e.snap.LoginURL = loginURL
		} else {
			e.snap.LastError = err.Error()
		}
	}
	e.snap.UpdatedAt = time.Now()
	return nil
}

func (e *Engine) Status() Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.snap
	s.Active = e.done != nil
	s.TailscaleIPs = append([]string{}, s.TailscaleIPs...)
	s.AdvertisedRoutes = append([]string{}, s.AdvertisedRoutes...)
	s.ApprovedRoutes = append([]string{}, s.ApprovedRoutes...)
	s.Health = append([]string{}, s.Health...)
	return s
}

func (e *Engine) Stop(ctx context.Context) error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	e.mu.Lock()
	cancel, done := e.cancel, e.done
	e.mu.Unlock()
	if done == nil {
		return nil
	}
	cancel()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return errors.New("disconnect is still stopping the managed daemon; wait before quitting")
	}
}

// BeginLogin requests an interactive login and returns only an allowlisted URL.
// It never launches a browser, captures credentials, or logs the returned URL.
func (e *Engine) BeginLogin(ctx context.Context) (string, error) {
	e.mu.Lock()
	active := e.done != nil
	e.mu.Unlock()
	if !active {
		return "", ErrNotRunning
	}
	if err := e.api.request(ctx, http.MethodPost, "/login-interactive", nil, nil); err != nil {
		return "", err
	}
	loginCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		s, err := e.api.status(loginCtx)
		if err == nil && s.AuthURL != "" {
			return ValidateLoginURL(s.AuthURL)
		}
		if err == nil && s.BackendState == "Running" {
			return "", nil
		}
		e.mu.Lock()
		active = e.done != nil
		e.mu.Unlock()
		if !active {
			return "", ErrNotRunning
		}
		select {
		case <-loginCtx.Done():
			return "", errors.New("login link is not ready yet; try again")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func (e *Engine) ApplyRoutes(ctx context.Context, routes []string) error {
	e.mu.Lock()
	c := e.cfg
	e.mu.Unlock()
	c.AdvertiseRoutes = append([]string{}, routes...)
	return e.ApplyConfig(ctx, c)
}

// ApplyConfig supports an explicit empty AdvertiseRoutes list to withdraw all
// subnet advertisements. Application-level approval must precede this method.
func (e *Engine) ApplyConfig(ctx context.Context, c config.Config) error {
	e.opMu.Lock()
	defer e.opMu.Unlock()
	c, err := config.Validate(c)
	if err != nil {
		return err
	}
	if !c.HasConsent() {
		return ErrConsentRequired
	}
	if len(c.AdvertiseRoutes) > 0 && !c.SubnetConsentAccepted {
		return errors.New("explicit subnet exposure consent is required")
	}
	e.apiMu.Lock()
	defer e.apiMu.Unlock()
	e.mu.Lock()
	active := e.done != nil
	running := e.snap.Running
	e.mu.Unlock()
	if active && running {
		if err := e.api.update(ctx, c); err != nil {
			return err
		}
		if err := e.api.verifyPrefs(ctx, c); err != nil {
			e.mu.Lock()
			if e.cancel != nil {
				e.cancel()
			}
			e.mu.Unlock()
			return errUnsafePreferences
		}
	}
	// Save only after the daemon accepts the change. On save failure, stop to avoid
	// showing a configuration that will unexpectedly change after next launch.
	if err := config.Save(e.opts.StateDir, c); err != nil {
		e.mu.Lock()
		if e.cancel != nil {
			e.cancel()
		}
		e.mu.Unlock()
		return errors.New("cannot persist configuration; disconnected to avoid inconsistent exposure")
	}
	e.mu.Lock()
	e.cfg = c
	e.snap.Hostname = c.Hostname
	e.snap.AdvertisedRoutes = append([]string{}, c.AdvertiseRoutes...)
	e.mu.Unlock()
	return nil
}
