package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/ImSingee/TailBuddy/internal/config"
	"github.com/ImSingee/TailBuddy/internal/engine"
	"github.com/ImSingee/TailBuddy/internal/i18n"
	"github.com/egoist/mygo/ui"
)

type fakeNode struct {
	dir                   string
	starts, stops, logins int
	snapshot              engine.Snapshot
	startErr              error
}

func (n *fakeNode) Start(ctx context.Context, c config.Config) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	n.starts++
	saved, err := config.Load(n.dir)
	if err != nil {
		return err
	}
	if !saved.CanResume() || !c.CanResume() {
		return errors.New("intent and consent not persisted before start")
	}
	if n.startErr != nil {
		return n.startErr
	}
	n.snapshot = engine.Snapshot{Active: true, Running: true, BackendState: "Running", Connected: true}
	return nil
}
func (n *fakeNode) Stop(context.Context) error                       { n.stops++; n.snapshot = engine.Snapshot{}; return nil }
func (n *fakeNode) Status() engine.Snapshot                          { return n.snapshot }
func (n *fakeNode) ApplyConfig(context.Context, config.Config) error { return nil }
func (n *fakeNode) BeginLogin(context.Context) (string, error)       { n.logins++; return "", nil }

func sessionApp(t *testing.T, dir string) (*App, *fakeNode, func()) {
	t.Helper()
	a := New()
	a.stateDir = dir
	n := &fakeNode{dir: dir}
	a.engine = n
	completed := make(chan func(), 1)
	a.dispatch = func(fn func()) { completed <- fn }
	finish := func() {
		t.Helper()
		select {
		case fn := <-completed:
			fn()
		case <-time.After(3 * time.Second):
			t.Fatal("operation did not complete")
		}
	}
	return a, n, finish
}

func approvedSession() config.Config {
	c := config.Default()
	c.ConsentVersion = config.CurrentConsentVersion
	c.ConsentAccepted = true
	c.PersistCredentials = true
	return c
}

func TestFirstConnectionModalAndRelaunch(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		t.Run(lang, func(t *testing.T) {
			dir := privateSessionDir(t)
			a, n, finish := sessionApp(t, dir)
			a.words = i18n.New(lang)
			tt := ui.NewTester(a.view, 760, 1000)
			if err := tt.Click(a.t("connect")); err != nil {
				t.Fatal(err)
			}
			if !a.consentOpen || n.starts != 0 {
				t.Fatal("first click must only open consent")
			}
			a.connect() // Repeated callbacks must not reset a dialog or start a node.
			if err := tt.Click(a.t("consent")); err != nil {
				t.Fatal(err)
			}
			a.confirmConsent()
			if a.cfg.HasConsent() || n.starts != 0 {
				t.Fatal("partial approval connected")
			}
			if err := tt.Click(a.t("persist")); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click(a.t("confirmConnect")); err != nil {
				t.Fatal(err)
			}
			finish()
			tt.Frame()
			if n.starts != 1 || a.consentOpen || !a.cfg.CanResume() {
				t.Fatal("approval did not connect once")
			}
			for _, k := range []string{"consent", "persist"} {
				if slices.Contains(tt.Texts(), a.t(k)) {
					t.Fatal("consent remained outside modal")
				}
			}
			// Quit stops only the running node; it deliberately preserves intent.
			if err := a.engine.Stop(context.Background()); err != nil {
				t.Fatal(err)
			}
			c, err := config.Load(dir)
			if err != nil || !c.CanResume() {
				t.Fatalf("quit lost intent: %+v %v", c, err)
			}
			b, next, done := sessionApp(t, dir)
			b.cfg = c
			b.resumeConnection()
			done()
			if next.starts != 1 || b.consentOpen || next.logins != 0 {
				t.Fatal("relaunch required redundant approval or sign-in")
			}
			b.disconnect()
			done()
			c, err = config.Load(dir)
			if err != nil || c.ConnectionWanted || !c.HasConsent() {
				t.Fatalf("disconnect lost consent or retained intent: %+v %v", c, err)
			}
			d, stopped, _ := sessionApp(t, dir)
			d.cfg = c
			d.resumeConnection()
			if d.busy || stopped.starts != 0 {
				t.Fatal("manual disconnect resumed on next launch")
			}
		})
	}
}

func TestConsentDismissalDoesNotSaveOrConnect(t *testing.T) {
	for _, dismiss := range []string{"cancel", "escape", "outside", "window close"} {
		t.Run(dismiss, func(t *testing.T) {
			a, n, _ := sessionApp(t, privateSessionDir(t))
			tt := ui.NewTester(a.view, 760, 1000)
			if err := tt.Click(a.t("connect")); err != nil {
				t.Fatal(err)
			}
			if err := tt.Click(a.t("consent")); err != nil {
				t.Fatal(err)
			}
			switch dismiss {
			case "cancel":
				if err := tt.Click(a.t("cancel")); err != nil {
					t.Fatal(err)
				}
			case "escape":
				tt.Key(0, ui.KeyEscape)
			case "outside":
				tt.ClickAt(1, 999)
			case "window close":
				a.dismissConsent()
			}
			tt.Frame()
			if a.consentOpen || a.accessConsent || a.credentialConsent || a.cfg.HasConsent() || n.starts != 0 {
				t.Fatal("dismissal granted permission")
			}
			if _, err := os.Stat(filepath.Join(a.stateDir, "config.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("dismissal wrote config: %v", err)
			}
			a.connect()
			if !a.consentOpen || a.accessConsent || a.credentialConsent {
				t.Fatal("reopened dialog reused partial choices")
			}
		})
	}
}

func TestResumeFailsClosed(t *testing.T) {
	for _, scenario := range []string{"fresh", "stale consent", "unapproved subnet", "fatal", "smoke", "quit"} {
		t.Run(scenario, func(t *testing.T) {
			a, n, _ := sessionApp(t, privateSessionDir(t))
			a.cfg = approvedSession()
			a.cfg.ConnectionWanted = true
			switch scenario {
			case "fresh":
				a.cfg = config.Default()
			case "stale consent":
				a.cfg.ConsentVersion++
			case "unapproved subnet":
				a.cfg.AdvertiseRoutes = []string{"192.168.50.0/24"}
			case "fatal":
				a.fatal = "invalid private storage"
			case "smoke":
				a.smoke = true
			case "quit":
				a.quitting = true
			}
			a.resumeConnection()
			if a.busy || n.starts != 0 || a.consentOpen {
				t.Fatal("unsafe automatic start")
			}
		})
	}
}

func TestDisconnectSaveFailureStillStopsAndAllowsRetry(t *testing.T) {
	dir := privateSessionDir(t)
	a, n, done := sessionApp(t, dir)
	a.cfg = approvedSession()
	a.cfg.ConnectionWanted = true
	// A directory where the config file should be forces a safe, portable write failure.
	if err := os.Mkdir(filepath.Join(dir, "config.json"), 0700); err != nil {
		t.Fatal(err)
	}
	a.disconnect()
	done()
	if n.stops != 1 || a.message == "" || !a.canDisconnect() {
		t.Fatal("failed persistence prevented stop or retry")
	}
	if err := os.Remove(filepath.Join(dir, "config.json")); err != nil {
		t.Fatal(err)
	}
	a.disconnect()
	done()
	c, err := config.Load(dir)
	if err != nil || c.ConnectionWanted || !c.HasConsent() || a.canDisconnect() {
		t.Fatal("retry did not save disconnected choice")
	}
}

func TestFailedStartRetainsDisconnectAction(t *testing.T) {
	a, n, done := sessionApp(t, privateSessionDir(t))
	a.cfg = approvedSession()
	n.startErr = errors.New("helper unavailable")
	a.connect()
	done()
	if a.snapshot.Active || !a.canDisconnect() {
		t.Fatal("cannot cancel retry-on-open after start failure")
	}
	tt := ui.NewTester(a.view, 760, 1000)
	if !slices.Contains(tt.Texts(), a.t("disconnect")) || !slices.Contains(tt.Texts(), a.t("connect")) {
		t.Fatal("failed start lost retry or disconnect")
	}
}

func TestCancelledConnectDoesNotPersistOrStart(t *testing.T) {
	dir := privateSessionDir(t)
	n := &fakeNode{dir: dir}
	c := approvedSession()
	c.ConnectionWanted = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := connectNode(ctx, dir, c, n); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if n.starts != 0 {
		t.Fatal("cancelled operation started")
	}
	if _, err := os.Stat(filepath.Join(dir, "config.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled operation persisted")
	}
}

func TestLegacyApprovalConnectsWithoutAnotherDialog(t *testing.T) {
	dir := privateSessionDir(t)
	legacy := `{"consentAccepted":true,"persistCredentials":true,"hostname":"tailbuddy"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	// An opaque synthetic state file must not be inspected, rewritten or erased.
	state := filepath.Join(dir, "tailscaled.state")
	if err := os.WriteFile(state, []byte("offline fixture, not a credential"), 0600); err != nil {
		t.Fatal(err)
	}
	a, n, done := sessionApp(t, dir)
	var err error
	a.cfg, err = config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	a.resumeConnection()
	if a.busy || n.starts != 0 {
		t.Fatal("legacy intent guessed")
	}
	a.connect()
	done()
	if n.starts != 1 || a.consentOpen {
		t.Fatal("legacy approval was forgotten")
	}
	a.disconnect()
	done()
	if got, err := os.ReadFile(state); err != nil || string(got) != "offline fixture, not a credential" {
		t.Fatal("existing daemon state was changed")
	}
}

func TestRestoredSubnetApprovalIsNotReusedAfterEditing(t *testing.T) {
	a := New()
	a.cfg = approvedSession()
	a.cfg.SubnetConsentAccepted = true
	a.cfg.AdvertiseRoutes = []string{"192.168.50.0/24"}
	a.subnets = true
	a.routes = "192.168.50.0/24"
	a.routeConsent = a.cfg.SubnetConsentAccepted
	a.tab = "access"
	if _, err := a.candidate(); err != nil {
		t.Fatal("saved exact routes lost consent", err)
	}
	tt := ui.NewTester(a.view, 760, 1100)
	if err := tt.Click(a.t("enableSubnets")); err != nil {
		t.Fatal(err)
	}
	if a.routeConsent {
		t.Fatal("changed subnet switch retained consent")
	}
	if err := tt.Click(a.t("enableSubnets")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.candidate(); err == nil {
		t.Fatal("reenabled routes reused old consent")
	}
}

func privateSessionDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}
func TestDisconnectWithoutHelperClearsIntent(t *testing.T) {
	a, _, done := sessionApp(t, privateSessionDir(t))
	a.cfg = approvedSession()
	a.cfg.ConnectionWanted = true
	a.engine = nil
	a.fatal = "missing helper"
	a.disconnect()
	done()
	c, err := config.Load(a.stateDir)
	if err != nil || c.ConnectionWanted {
		t.Fatalf("cannot clear intent without helper: %+v %v", c, err)
	}
}

func TestUnsavedInvalidSettingsCannotBlockDisconnect(t *testing.T) {
	a, _, done := sessionApp(t, privateSessionDir(t))
	a.cfg = approvedSession()
	a.cfg.ConnectionWanted = true
	a.hostname = "invalid host name"
	a.subnets = true
	a.routes = "invalid CIDR"
	a.routeConsent = false
	a.disconnect()
	done()
	c, err := config.Load(a.stateDir)
	if err != nil || c.ConnectionWanted || c.Hostname != "tailbuddy" || len(c.AdvertiseRoutes) != 0 {
		t.Fatalf("unsaved edits changed disconnect: %+v %v", c, err)
	}
}
