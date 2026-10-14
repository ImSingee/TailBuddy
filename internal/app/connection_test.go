package app

import (
	"slices"
	"testing"

	"github.com/ImSingee/TailBuddy/internal/engine"
	"github.com/ImSingee/TailBuddy/internal/i18n"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestLoginVisibilityForBackendStates(t *testing.T) {
	states := []struct {
		name, state, label, help string
		active, connected, login bool
	}{
		{"disconnected", "Stopped", "offline", "", false, false, false},
		{"inactive stale login", "NeedsLogin", "offline", "", false, false, false},
		{"needs sign-in", "NeedsLogin", "loginNeeded", "", true, false, true},
		{"signed in", "Running", "connected", "", true, true, false},
		{"signed in while offline", "Running", "connecting", "", true, false, false},
		{"starting", "Starting", "starting", "", true, false, false},
		{"reconnecting", "Reconnecting", "reconnecting", "", true, false, false},
		{"awaiting approval", "NeedsMachineAuth", "awaitingApproval", "approvalHelp", true, false, false},
		{"stopped daemon", "Stopped", "stopped", "stoppedHelp", true, false, false},
		{"checking state", "NoState", "checkingStatus", "checkingStatusHelp", true, false, false},
		{"empty state", "", "checkingStatus", "checkingStatusHelp", true, false, false},
		{"other user", "InUseOtherUser", "inUse", "inUseHelp", true, false, false},
		{"unknown state", "FutureState", "connecting", "checkingStatusHelp", true, false, false},
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, state := range states {
			t.Run(lang+"/"+state.name, func(t *testing.T) {
				a := New()
				a.words = i18n.New(lang)
				a.snapshot = engine.Snapshot{Active: state.active, BackendState: state.state, Connected: state.connected}
				if got := a.needsLogin(); got != state.login {
					t.Fatalf("login visibility = %v, want %v", got, state.login)
				}
				if got := a.statusLabel(); got != a.t(state.label) {
					t.Errorf("status = %q, want %q", got, a.t(state.label))
				}
				help := ""
				if state.help != "" {
					help = a.t(state.help)
				}
				if got := a.statusHelp(); got != help {
					t.Errorf("help = %q, want %q", got, help)
				}
				// An unmounted item exercises the production menu update without a native
				// event loop. SetVisible/SetEnabled still update the actual menu item model.
				item := &mygo.MenuItem{}
				a.updateLoginMenuItem(item)
				if item.Hidden == state.login || item.Disabled == state.login {
					t.Errorf("menu state: hidden=%v disabled=%v", item.Hidden, item.Disabled)
				}
				tt := ui.NewTester(a.view, 760, 1100)
				if got := slices.Contains(tt.Texts(), a.t("login")); got != state.login {
					t.Errorf("window login visibility = %v", got)
				}
				if got := slices.Contains(tt.Texts(), a.t("disconnect")); got != state.active {
					t.Errorf("disconnect visibility = %v", got)
				}
				if got := slices.Contains(tt.Texts(), a.t("connect")); got == state.active {
					t.Errorf("connect recovery visibility = %v", got)
				}
				if help != "" && !slices.Contains(tt.Texts(), help) {
					t.Error("recovery or approval instructions missing")
				}
				if state.state == "NeedsMachineAuth" && !slices.Contains(tt.Texts(), a.t("admin")) {
					t.Error("approval console link missing")
				}
			})
		}
	}
}

func TestLoginStateTransitionsUpdateWindowAndMenu(t *testing.T) {
	a := New()
	a.snapshot = engine.Snapshot{Active: true, BackendState: "NeedsLogin"}
	tt := ui.NewTester(a.view, 760, 1100)
	item := &mygo.MenuItem{}
	for _, state := range []string{"NeedsLogin", "Starting", "Running", "Reconnecting", "NeedsMachineAuth", "NoState", "Stopped", "NeedsLogin", "Running"} {
		a.snapshot.BackendState = state
		a.snapshot.Connected = state == "Running"
		a.updateLoginMenuItem(item)
		tt.Frame()
		want := state == "NeedsLogin"
		if slices.Contains(tt.Texts(), a.t("login")) != want || item.Hidden == want {
			t.Fatalf("stale login action after transition to %s", state)
		}
		if !slices.Contains(tt.Texts(), a.t("disconnect")) {
			t.Fatalf("disconnect lost in %s", state)
		}
	}
	a.snapshot.Active = false
	a.updateLoginMenuItem(item)
	tt.Frame()
	if slices.Contains(tt.Texts(), a.t("login")) || !item.Hidden || !slices.Contains(tt.Texts(), a.t("connect")) {
		t.Fatal("disconnect must restore Connect without leaving a sign-in action")
	}
}

func TestBusyLoginStaysVisibleButCannotRun(t *testing.T) {
	a := New()
	a.snapshot = engine.Snapshot{Active: true, BackendState: "NeedsLogin"}
	a.busy = true
	item := &mygo.MenuItem{}
	a.updateLoginMenuItem(item)
	if item.Hidden || !item.Disabled {
		t.Fatal("pending sign-in should remain visible and disabled while busy")
	}
	tt := ui.NewTester(a.view, 760, 1100)
	if !slices.Contains(tt.Texts(), a.t("login")) {
		t.Fatal("pending sign-in disappeared while busy")
	}
	a.quitting = true
	a.busy = false
	a.updateLoginMenuItem(item)
	if !item.Disabled {
		t.Fatal("sign-in must be disabled while quitting")
	}
}

func TestLateSignInCallbackIgnoredOutsideNeedsLogin(t *testing.T) {
	a := New()
	var err error
	a.engine, err = engine.New(engine.Options{StateDir: "/tmp/tailbuddy-ui-test-state", HelperPath: "/unused/tailscaled"})
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"Running", "Starting", "Reconnecting", "NeedsMachineAuth", "Stopped", "NoState"} {
		a.snapshot = engine.Snapshot{Active: true, BackendState: state}
		a.message = "unchanged"
		a.signIn()
		if a.busy || a.message != "unchanged" {
			t.Fatalf("stale sign-in callback was handled in %s", state)
		}
	}
}

func TestReconnectStatusKeepsFailureDetail(t *testing.T) {
	a := New()
	for _, state := range []string{"Reconnecting", "Starting"} {
		a.snapshot = engine.Snapshot{Active: true, BackendState: state, RestartCount: 1, LastError: "previous helper failure"}
		if a.statusLabel() != a.t("reconnecting") {
			t.Fatalf("recovery not shown in %s", state)
		}
		tt := ui.NewTester(a.view, 760, 1100)
		if !slices.Contains(tt.Texts(), a.snapshot.LastError) {
			t.Fatal("recovery hid the failure detail")
		}
		if slices.Contains(tt.Texts(), a.t("login")) {
			t.Fatal("reconnection must not suggest reauthentication")
		}
	}
}
