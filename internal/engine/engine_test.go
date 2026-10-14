package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ImSingee/TailBuddy/internal/config"
)

// These tests run only a fake LocalAPI over a private Unix socket. They never
// launch tailscaled, authenticate, or open TCP/UDP listeners.
func TestMain(m *testing.M) {
	if handled, code := SupervisorMain(os.Args[1:]); handled {
		os.Exit(code)
	}
	if len(os.Args) > 1 && os.Args[1] == "--tun=userspace-networking" {
		os.Exit(fakeHelper())
	}
	os.Exit(m.Run())
}

func fakeHelper() int {
	var dir, socket string
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--statedir=") {
			dir = strings.TrimPrefix(arg, "--statedir=")
		}
		if strings.HasPrefix(arg, "--socket=") {
			socket = strings.TrimPrefix(arg, "--socket=")
		}
	}
	if dir == "" || socket == "" {
		return 91
	}
	boot, err := os.ReadFile(filepath.Join(dir, "boot.json"))
	if err != nil {
		return 92
	}
	var b map[string]any
	if json.Unmarshal(boot, &b) != nil || b["enabled"] != false || b["shieldsUp"] != true {
		return 93
	}
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "TS_AUTHKEY", "LD_PRELOAD", "TS_DEBUG_NETSTACK_SUBNETS"} {
		if os.Getenv(k) != "" {
			return 94
		}
	}
	_ = os.WriteFile(filepath.Join(dir, "fake.pid"), []byte(strconv.Itoa(os.Getpid())), 0600)
	modeBytes, _ := os.ReadFile(filepath.Join(dir, "fake.mode"))
	mode := string(modeBytes)
	if mode == "crash" {
		return 90
	}
	if strings.HasPrefix(mode, "memory:") {
		for {
			time.Sleep(time.Hour)
		}
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		return 95
	}
	defer ln.Close()
	_ = os.Chmod(socket, 0666) // mirror upstream; application must tighten this.
	handler := newFakeHandler(mode)
	if err := http.Serve(ln, handler); err != nil {
		return 96
	}
	return 0
}

func newFakeHandler(mode string) http.Handler {
	var mu sync.Mutex
	prefs := map[string]any{"WantRunning": false, "AdvertiseRoutes": []any{}}
	login := ""
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Host != "local-tailscaled.sock" {
			http.Error(w, "bad host", 400)
			return
		}
		if mode == "error" {
			http.Error(w, "https://login.tailscale.com/a/SECRET tskey-SECRET", 500)
			return
		}
		switch r.URL.Path {
		case "/localapi/v0/status":
			state := "Stopped"
			if prefs["WantRunning"] == true {
				state = "Running"
			}
			if mode == "login" {
				state = "NeedsLogin"
			}
			self := map[string]any{"HostName": "tailbuddy", "DNSName": "tailbuddy.example.ts.net.", "Online": true, "PrimaryRoutes": prefs["AdvertiseRoutes"]}
			json.NewEncoder(w).Encode(map[string]any{"BackendState": state, "AuthURL": login, "TailscaleIPs": []string{"100.80.1.2"}, "Self": self})
		case "/localapi/v0/start":
			var req struct{ UpdatePrefs map[string]any }
			if json.NewDecoder(r.Body).Decode(&req) != nil {
				http.Error(w, "bad JSON", 400)
				return
			}
			prefs = req.UpdatePrefs
			if mode == "unsafe" {
				prefs["CorpDNS"] = true
			}
			w.WriteHeader(204)
		case "/localapi/v0/prefs":
			if r.Method == http.MethodPatch {
				var patch map[string]any
				if json.NewDecoder(r.Body).Decode(&patch) != nil {
					http.Error(w, "bad JSON", 400)
					return
				}
				for key, value := range patch {
					if patch[key+"Set"] == true {
						prefs[key] = value
					}
				}
			}
			json.NewEncoder(w).Encode(prefs)
		case "/localapi/v0/login-interactive":
			login = "https://login.tailscale.com/a/FAKE_TEST_TOKEN"
			w.WriteHeader(204)
		default:
			http.NotFound(w, r)
		}
	})

	return handler
}

func privateDir(t *testing.T) string {
	t.Helper()
	// macOS Unix sockets have a short pathname limit. t.TempDir can exceed it.
	dir, err := os.MkdirTemp("", "tb-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
func approvedConfig() config.Config {
	c := config.Default()
	c.ConsentVersion = config.CurrentConsentVersion
	c.ConsentAccepted = true
	c.PersistCredentials = true
	c.SubnetConsentAccepted = true
	return c
}
func testEngine(t *testing.T, mode string) (*Engine, string) {
	t.Helper()
	dir := privateDir(t)
	if err := os.WriteFile(filepath.Join(dir, "fake.mode"), []byte("memory:"+mode), 0600); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e, err := New(Options{HelperPath: exe, StateDir: dir, PollInterval: 25 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	handler := newFakeHandler(mode)
	e.api.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		pidBytes, err := os.ReadFile(filepath.Join(dir, "fake.pid"))
		if err != nil {
			return nil, errors.New("fake not ready")
		}
		pid, _ := strconv.Atoi(string(pidBytes))
		if pid <= 0 || syscall.Kill(pid, 0) != nil {
			return nil, errors.New("fake not alive")
		}
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		return rr.Result(), nil
	})}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
		defer cancel()
		if err := e.Stop(ctx); err != nil {
			t.Error(err)
		}
	})
	return e, dir
}
func waitFor(t *testing.T, timeout time.Duration, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestStartRequiresAllConsents(t *testing.T) {
	e, _ := testEngine(t, "")
	if err := e.Start(context.Background(), config.Default()); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("got %v", err)
	}
	c := approvedConfig()
	c.ConsentVersion++
	if err := e.Start(context.Background(), c); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("stale consent started: %v", err)
	}
	if err := e.ApplyConfig(context.Background(), c); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("stale consent applied: %v", err)
	}
	c = approvedConfig()
	c.PersistCredentials = false
	if err := e.Start(context.Background(), c); !errors.Is(err, ErrConsentRequired) {
		t.Fatalf("got %v", err)
	}
	c = approvedConfig()
	c.AdvertiseRoutes = []string{"192.168.1.0/24"}
	c.SubnetConsentAccepted = false
	if err := e.Start(context.Background(), c); err == nil {
		t.Fatal("subnet started without consent")
	}
	if e.Status().Running {
		t.Fatal("daemon started without consent")
	}
}

func TestLifecycleRoutesAndExclusiveLock(t *testing.T) {
	e, dir := testEngine(t, "")
	c := approvedConfig()
	c.ConnectionWanted = true
	c.AdvertiseRoutes = []string{"192.168.20.0/24"}
	if err := e.Start(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return e.Status().Connected })
	s := e.Status()
	if len(s.ApprovedRoutes) != 1 || !s.ApprovalKnown || s.ReachabilityVerified {
		t.Fatalf("invalid status %+v", s)
	}
	second, err := New(e.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Start(context.Background(), c); err == nil {
		second.Stop(context.Background())
		t.Fatal("duplicate engine accepted")
	}
	s.TailscaleIPs[0] = "modified"
	if e.Status().TailscaleIPs[0] == "modified" {
		t.Fatal("status aliases live memory")
	}
	if err := e.ApplyRoutes(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, time.Second, func() bool { return len(e.Status().ApprovedRoutes) == 0 })
	loaded, err := config.Load(dir)
	if err != nil || len(loaded.AdvertiseRoutes) != 0 || !loaded.CanResume() {
		t.Fatalf("routes not cleared: %+v %v", loaded, err)
	}
	pidBytes, _ := os.ReadFile(filepath.Join(dir, "fake.pid"))
	pid, _ := strconv.Atoi(string(pidBytes))
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	if err := e.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	if e.Status().Running {
		t.Fatal("still running")
	}
	if pid <= 0 || syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Fatalf("fake child not reaped: %d", pid)
	}
	if err := e.Start(context.Background(), approvedConfig()); err != nil {
		t.Fatalf("cannot restart: %v", err)
	}
	waitFor(t, 5*time.Second, func() bool { return e.Status().Connected })
}

func TestCancelStopsChild(t *testing.T) {
	e, dir := testEngine(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.Start(ctx, approvedConfig()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return e.Status().Connected })
	pidBytes, _ := os.ReadFile(filepath.Join(dir, "fake.pid"))
	pid, _ := strconv.Atoi(string(pidBytes))
	cancel()
	waitFor(t, 6*time.Second, func() bool { return !e.Status().Running })
	if syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Fatal("child survived cancellation")
	}
}

func TestParentPipeClosureReapsHelper(t *testing.T) {
	e, dir := testEngine(t, "")
	lock, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := config.WritePrivateFile(dir, "boot.json", safeBootConfig()); err != nil {
		t.Fatal(err)
	}
	p, err := e.launch(lock)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { _, err := os.Stat(filepath.Join(dir, "fake.pid")); return err == nil })
	pidBytes, _ := os.ReadFile(filepath.Join(dir, "fake.pid"))
	pid, _ := strconv.Atoi(string(pidBytes))
	p.lifetime.Close() // identical EOF to abrupt GUI death, no LocalAPI request.
	select {
	case <-p.exited:
	case <-time.After(6 * time.Second):
		t.Fatal("supervisor did not terminate")
	}
	if syscall.Kill(pid, 0) != syscall.ESRCH {
		t.Fatal("child survived parent pipe closure")
	}
}

func TestUnsafePreferencesFailClosed(t *testing.T) {
	e, _ := testEngine(t, "unsafe")
	if err := e.Start(context.Background(), approvedConfig()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 4*time.Second, func() bool { return e.Status().BackendState == "Error" })
	if e.Status().Running || e.Status().RestartCount != 0 {
		t.Fatalf("unsafe daemon retried: %+v", e.Status())
	}
}

func TestLoginOnlyOfficialOrigin(t *testing.T) {
	e, _ := testEngine(t, "login")
	if err := e.Start(context.Background(), approvedConfig()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return e.Status().BackendState == "NeedsLogin" })
	got, err := e.BeginLogin(context.Background())
	if err != nil || got != "https://login.tailscale.com/a/FAKE_TEST_TOKEN" {
		t.Fatalf("login failed: %v", err)
	}
	for _, u := range []string{"http://login.tailscale.com/a/key", "https://evil.example/a/key", "https://login.tailscale.com.evil.example/a/key", "https://user@login.tailscale.com/a/key", "https://login.tailscale.com:443/a/key", "file:///secret", "javascript:alert(1)", "https://login.tailscale.com/a/key#fragment"} {
		if _, err := ValidateLoginURL(u); err == nil {
			t.Fatalf("accepted %q", u)
		}
	}
	b, _ := json.Marshal(e.Status())
	if strings.Contains(string(b), "FAKE_TEST_TOKEN") {
		t.Fatal("snapshot JSON leaks auth URL")
	}
}

func TestLocalAPIResponseErrorRedactsSecrets(t *testing.T) {
	a := newLocalAPI("unused-test-socket")
	a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("https://login.tailscale.com/a/SECRET tskey-SECRET")), Request: r}, nil
	})
	_, err := a.status(context.Background())
	if err == nil || strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "https:") {
		t.Fatalf("unredacted error %v", err)
	}
}

func TestHelperFlagsAndEnvironment(t *testing.T) {
	args := strings.Join(helperArguments("/private/state"), " ")
	for _, need := range []string{"--tun=userspace-networking", "--state=/private/state/tailscaled.state", "--socket=/private/state/tailscaled.sock", "--config=/private/state/boot.json", "--no-logs-no-support"} {
		if !strings.Contains(args, need) {
			t.Errorf("missing %s", need)
		}
	}
	for _, bad := range []string{"socks5", "outbound-http-proxy", "accept-routes", "exit-node"} {
		if strings.Contains(args, bad) {
			t.Errorf("unsafe args %s", args)
		}
	}
	t.Setenv("TS_AUTHKEY", "secret")
	t.Setenv("HTTPS_PROXY", "https://secret.example")
	t.Setenv("TS_DEBUG_NETSTACK_SUBNETS", "1")
	env := strings.Join(helperEnvironment(), "\n")
	if strings.Contains(env, "secret") || strings.Contains(env, "TS_DEBUG") {
		t.Fatal("environment leaked")
	}
	if !strings.Contains(env, "TS_NO_LOGS_NO_SUPPORT=true") {
		t.Fatal("logging upload not disabled")
	}
}

func TestLocalAPIRejectsOversizedAndRedirects(t *testing.T) {
	for _, mode := range []string{"large", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			a := newLocalAPI("unused-test-socket")
			a.client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Host != "local-tailscaled.sock" {
					t.Fatal("redirect reached external origin")
				}
				if mode == "large" {
					return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", maximumResponse+1))), Request: r}, nil
				}
				return &http.Response{StatusCode: 307, Header: http.Header{"Location": []string{"https://evil.example"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
			})
			if _, err := a.status(context.Background()); err == nil {
				t.Fatal("accepted unsafe response")
			}
		})
	}
}

func TestSafeBootHasNoInheritedExposure(t *testing.T) {
	var c map[string]any
	if err := json.Unmarshal(safeBootConfig(), &c); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"enabled", "acceptDNS", "acceptRoutes", "advertiseExitNode", "runSSHServer", "runWebClient", "remoteConfig"} {
		if c[k] != false {
			t.Errorf("%s is not disabled", k)
		}
	}
	if c["shieldsUp"] != true || len(c["advertiseRoutes"].([]any)) != 0 {
		t.Fatal("boot config may expose stale routes")
	}
	if fmt.Sprint(c["version"]) != "alpha0" {
		t.Fatal("unexpected config version")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func listenUnixOrSkip(t *testing.T, path string) net.Listener {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES) {
		t.Skip("execution sandbox forbids Unix socket creation; mock LocalAPI and real process-supervision tests run separately")
	}
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func TestRefreshSerializedWithApplyConfig(t *testing.T) {
	e, err := New(Options{StateDir: privateDir(t)})
	if err != nil {
		t.Fatal(err)
	}
	old := approvedConfig()
	old.AdvertiseRoutes = []string{"192.168.10.0/24"}
	e.cfg = old
	e.done = make(chan struct{})
	e.snap.Running = true
	handler := newFakeHandler("")
	patched := make(chan struct{})
	release := make(chan struct{})
	e.api.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		rr := httptest.NewRecorder()
		handler.ServeHTTP(rr, req)
		if req.Method == http.MethodPatch {
			close(patched)
			<-release
		}
		return rr.Result(), nil
	})}
	if err := e.api.configure(context.Background(), old); err != nil {
		t.Fatal(err)
	}
	changed := old
	changed.AdvertiseRoutes = []string{}
	applyDone := make(chan error, 1)
	go func() { applyDone <- e.ApplyConfig(context.Background(), changed) }()
	<-patched
	refreshDone := make(chan error, 1)
	go func() { refreshDone <- e.refresh(context.Background()) }()
	select {
	case err := <-refreshDone:
		t.Fatalf("refresh was not serialized: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if err := <-applyDone; err != nil {
		t.Fatal(err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatalf("refresh saw mismatched prefs/config: %v", err)
	}
}

func TestConfigUpdatesDuringReconnectBackoff(t *testing.T) {
	e, dir := testEngine(t, "")
	cfg := approvedConfig()
	cfg.AdvertiseRoutes = []string{"192.168.2.0/24"}
	if err := e.Start(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return e.Status().Connected })
	pidBytes, _ := os.ReadFile(filepath.Join(dir, "fake.pid"))
	pid, _ := strconv.Atoi(string(pidBytes))
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Second, func() bool { s := e.Status(); return s.Active && !s.Running && s.BackendState == "Reconnecting" })
	if err := e.ApplyRoutes(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Second, func() bool { return e.Status().Connected })
	if len(e.Status().AdvertisedRoutes) != 0 || len(e.Status().ApprovedRoutes) != 0 {
		t.Fatalf("stale routes restored after reconnect: %+v", e.Status())
	}
}

func TestUnixLocalAPIPrivateSocket(t *testing.T) {
	dir := privateDir(t)
	socket := filepath.Join(dir, "a.sock")
	ln := listenUnixOrSkip(t, socket)
	defer ln.Close()
	if err := os.Chmod(socket, 0666); err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: newFakeHandler("")}
	defer srv.Close()
	go srv.Serve(ln)
	a := newLocalAPI(socket)
	if _, err := a.status(context.Background()); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(socket)
	if err != nil || fi.Mode().Perm() != 0600 {
		t.Fatalf("socket not private: %v %v", fi, err)
	}
}

func TestSupervisorCrashTerminatesOwnedHelper(t *testing.T) {
	e, dir := testEngine(t, "")
	lock, err := acquireLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := config.WritePrivateFile(dir, "boot.json", safeBootConfig()); err != nil {
		t.Fatal(err)
	}
	p, err := e.launch(lock)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 3*time.Second, func() bool { _, err := os.Stat(filepath.Join(dir, "fake.pid")); return err == nil })
	pidBytes, _ := os.ReadFile(filepath.Join(dir, "fake.pid"))
	pid, _ := strconv.Atoi(string(pidBytes))
	defer syscall.Kill(pid, syscall.SIGKILL)
	if err := p.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-p.exited:
	case <-time.After(6 * time.Second):
		t.Fatal("supervisor was not reaped")
	}
	p.lifetime.Close()
	waitFor(t, 2*time.Second, func() bool {
		if syscall.Kill(pid, 0) == syscall.ESRCH {
			return true
		}
		// When its supervisor is killed, the helper is adopted by init. Some test
		// containers do not reap promptly; a zombie is terminated and cannot expose
		// services. macOS launchd ordinarily reaps it immediately.
		if runtime.GOOS == "linux" {
			data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
			end := strings.LastIndex(string(data), ") ")
			return end >= 0 && strings.HasPrefix(string(data)[end+2:], "Z ")
		}
		return false
	})
}
