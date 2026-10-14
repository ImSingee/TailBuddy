package app

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ImSingee/TailBuddy/internal/config"
	"github.com/ImSingee/TailBuddy/internal/engine"
	"github.com/ImSingee/TailBuddy/internal/i18n"
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

const Version = "0.1.0"

// App's view state is confined to MyGo's main thread; the engine synchronizes
// its own state. Slow operations are serialized by busy and never block drawing.
type App struct {
	ctx                                            context.Context
	smoke                                          bool
	words                                          i18n.Catalog
	engine                                         nodeEngine
	cfg                                            config.Config
	stateDir                                       string
	snapshot                                       engine.Snapshot
	window                                         *mygo.Window
	tray                                           *mygo.Tray
	menu                                           *mygo.Menu
	tab                                            string
	busy                                           bool
	message                                        string
	fatal                                          string
	routes                                         string
	hostname                                       string
	subnets                                        bool
	accessConsent, credentialConsent, routeConsent bool
	consentOpen                                    bool
	disconnectSavePending                          bool
	loginItem                                      bool
	quitting, readyToQuit                          bool
	cancel                                         context.CancelFunc
	dispatch                                       func(func())
}

func New() *App {
	return &App{words: i18n.New("en"), tab: "overview", cfg: config.Default(), hostname: config.Default().Hostname, ctx: context.Background(), dispatch: mygo.RunOnMain}
}
func (a *App) t(s string) string { return a.words.T(s) }

// RunSmoke exercises native window/tray creation without starting networking.
func (a *App) RunSmoke() error {
	a.smoke = true
	if err := a.Run(); err != nil {
		return err
	}
	if a.fatal != "" {
		return fmt.Errorf("native UI initialization failed")
	}
	return nil
}

func (a *App) Run() error {
	mygo.App.SetName("TailBuddy")
	mygo.App.SetVersion(Version)
	if !mygo.App.RequestSingleInstanceLock() {
		return nil
	}
	mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	mygo.App.OnWindowAllClosed(func() {})
	mygo.App.OnSecondInstance(func([]string, string) { a.show() })
	mygo.App.OnActivate(func(visible bool) {
		if !visible {
			a.show()
		}
	})
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if a.readyToQuit {
			return
		}
		e.PreventDefault()
		if a.quitting {
			return
		}
		a.quitting = true
		if a.cancel != nil {
			a.cancel()
		}
		go func() {
			if a.engine != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = a.engine.Stop(ctx)
			}
			mygo.RunOnMain(func() { a.readyToQuit = true; mygo.App.Quit() })
		}()
	})
	mygo.Power.OnResume(func() { a.refresh() })
	mygo.App.WhenReady(func() { a.ready() })
	return mygo.App.Run()
}
func (a *App) ready() {
	a.words = i18n.New(i18n.PreferredLocale())
	a.loginItem = mygo.App.OpenAtLogin()
	a.stateDir, _ = config.DefaultStateDir()
	if a.stateDir == "" {
		a.fatal = a.t("startupError")
	} else {
		cfg, err := config.Load(a.stateDir)
		if err != nil {
			a.fatal = err.Error()
		} else {
			a.cfg = cfg
		}
		helper, err := helperPath()
		if err != nil {
			a.fatal = a.t("missingHelper")
		} else {
			node, err := engine.New(engine.Options{HelperPath: helper, StateDir: a.stateDir})
			if err != nil {
				a.fatal = err.Error()
			} else {
				a.engine = node
			}
		}
	}
	a.routes = strings.Join(a.cfg.AdvertiseRoutes, "\n")
	a.hostname = a.cfg.Hostname
	a.subnets = len(a.cfg.AdvertiseRoutes) > 0
	// Persisted approval belongs to its scope, not to the UI's lifetime.
	a.routeConsent = a.cfg.SubnetConsentAccepted
	a.menu = mygo.NewMenu([]*mygo.MenuItem{
		{ID: "status", Label: "TailBuddy · " + a.t("offline"), Disabled: true},
		{Label: a.t("settings"), Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		{ID: "login", Label: a.t("login"), Hidden: true, Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) { a.signIn() }},
		{ID: "disconnect", Label: a.t("disconnect"), Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) { a.disconnect() }},
		mygo.Separator(), {Label: a.t("quit"), Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
	tray, err := mygo.NewTray(mygo.TrayOptions{Icon: trayPNG(), IconIsTemplate: true, ToolTip: "TailBuddy", Menu: a.menu})
	if err != nil {
		a.fatal = err.Error()
	} else {
		a.tray = tray
	}
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{{Role: mygo.RoleAppMenu}, {Role: mygo.RoleEditMenu}, {Role: mygo.RoleWindowMenu}}))
	ctx, cancel := context.WithCancel(context.Background())
	a.ctx = ctx
	a.cancel = cancel
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				mygo.RunOnMain(func() {
					if !a.quitting {
						a.refresh()
					}
				})
			}
		}
	}()
	if !mygo.App.WasOpenedAtLogin() || a.fatal != "" || a.smoke {
		a.show()
	}
	if a.smoke {
		time.AfterFunc(2*time.Second, func() { mygo.App.Quit() })
	} else {
		a.resumeConnection()
	}
}
func helperPath() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	p := filepath.Clean(filepath.Join(filepath.Dir(exe), "..", "Helpers", "tailscaled"))
	info, err := os.Stat(p)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("missing bundled helper")
	}
	return p, nil
}
func (a *App) show() {
	if a.window != nil && !a.window.IsDestroyed() {
		a.window.Show()
		a.window.Focus()
		return
	}
	a.window = mygo.NewWindow(mygo.WindowOptions{Title: "TailBuddy", Width: 760, Height: 820, MinWidth: 620, MinHeight: 620, Content: ui.View(a.view)})
	a.window.OnClosed(func() { a.dismissConsent() })
	a.window.Focus()
}
func (a *App) redraw() {
	if a.window != nil && !a.window.IsDestroyed() {
		a.window.Invalidate()
	}
}
func (a *App) refresh() {
	if a.engine != nil {
		a.snapshot = a.engine.Status()
	}
	if a.menu != nil {
		a.menu.ItemByID("status").SetLabel("TailBuddy · " + a.statusLabel())
		a.updateLoginMenuItem(a.menu.ItemByID("login"))
		a.menu.ItemByID("disconnect").SetEnabled(a.canDisconnect() && !a.busy && !a.consentOpen)
	}
	if a.tray != nil {
		a.tray.SetToolTip("TailBuddy · " + a.statusLabel())
	}
	a.redraw()
}

// needsLogin is the shared visibility rule for the window and menu bar.
// Starting, disconnected and approval-pending states are not login requests.
func (a *App) needsLogin() bool {
	return a.snapshot.Active && a.snapshot.BackendState == "NeedsLogin"
}
func (a *App) updateLoginMenuItem(item *mygo.MenuItem) {
	item.SetVisible(a.needsLogin())
	item.SetEnabled(a.needsLogin() && !a.busy && !a.quitting && !a.consentOpen)
}
func (a *App) statusLabel() string {
	if a.fatal != "" {
		return a.t("error")
	}
	// The last failure remains available in the error notice while recovery runs.
	if a.snapshot.Active && (a.snapshot.BackendState == "Reconnecting" || (a.snapshot.BackendState == "Starting" && a.snapshot.RestartCount > 0)) {
		return a.t("reconnecting")
	}
	if a.snapshot.LastError != "" {
		return a.t("error")
	}
	if !a.snapshot.Active {
		return a.t("offline")
	}
	switch a.snapshot.BackendState {
	case "Running":
		if !a.snapshot.Connected {
			return a.t("connecting")
		}
		return a.t("connected")
	case "NeedsLogin":
		return a.t("loginNeeded")
	case "NeedsMachineAuth":
		return a.t("awaitingApproval")
	case "NoState", "":
		return a.t("checkingStatus")
	case "InUseOtherUser":
		return a.t("inUse")
	case "Stopped":
		return a.t("stopped")
	case "Starting":
		return a.t("starting")
	default:
		return a.t("connecting")
	}
}

// statusHelp keeps recovery/approval instructions reachable without presenting
// an authentication action for states that do not request one.
func (a *App) statusHelp() string {
	if !a.snapshot.Active {
		return ""
	}
	switch a.snapshot.BackendState {
	case "NeedsMachineAuth":
		return a.t("approvalHelp")
	case "Stopped":
		return a.t("stoppedHelp")
	case "NoState", "":
		return a.t("checkingStatusHelp")
	case "InUseOtherUser":
		return a.t("inUseHelp")
	case "Starting", "Reconnecting", "NeedsLogin", "Running":
		return ""
	default:
		return a.t("checkingStatusHelp")
	}
}
func (a *App) run(action func() error, success string) {
	a.runAfter(action, success, nil)
}
func (a *App) runAfter(action func() error, success string, complete func(error)) {
	if a.busy || a.quitting {
		return
	}
	a.busy = true
	a.message = ""
	a.refresh()
	go func() {
		err := action()
		if a.ctx.Err() != nil {
			return
		}
		a.dispatch(func() {
			a.busy = false
			if complete != nil {
				complete(err)
			}
			if err != nil {
				a.message = err.Error()
			} else {
				a.message = success
			}
			a.refresh()
		})
	}()
}
func (a *App) candidate() (config.Config, error) {
	cfg := a.cfg
	cfg.Hostname = a.hostname
	cfg.SubnetConsentAccepted = a.routeConsent
	cfg.AdvertiseRoutes = nil
	if a.subnets {
		cfg.AdvertiseRoutes = strings.FieldsFunc(a.routes, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' || r == ' ' || r == '\t' })
		if len(cfg.AdvertiseRoutes) == 0 {
			return cfg, fmt.Errorf("%s", a.t("routesLabel"))
		}
		if !a.routeConsent {
			return cfg, fmt.Errorf("%s", a.t("approveChanges"))
		}
	}
	return config.Validate(cfg)
}
func (a *App) connect() {
	if a.engine == nil || a.fatal != "" || a.busy || a.quitting || a.snapshot.Active || a.consentOpen {
		return
	}
	if !a.cfg.HasConsent() {
		a.accessConsent, a.credentialConsent = false, false
		a.consentOpen = true
		a.redraw()
		return
	}
	cfg, err := a.candidate()
	if err != nil {
		a.message = err.Error()
		return
	}
	a.startConnection(cfg)
}
func (a *App) disconnect() {
	if a.busy || a.quitting || a.consentOpen {
		return
	}
	a.cfg.ConnectionWanted = false
	cfg := a.cfg
	a.runAfter(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return disconnectNode(ctx, a.stateDir, cfg, a.engine, a.t("disconnectSaveError"))
	}, "", func(err error) { a.disconnectSavePending = err != nil })
}
func (a *App) save() {
	if a.busy || a.quitting || a.consentOpen || a.fatal != "" {
		return
	}
	cfg, err := a.candidate()
	if err != nil {
		a.message = err.Error()
		return
	}
	running := a.snapshot.Active
	a.runAfter(func() error {
		if running {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			if err := a.engine.ApplyConfig(ctx, cfg); err != nil {
				return err
			}
		}
		return config.Save(a.stateDir, cfg)
	}, a.t("saved"), func(err error) {
		if err == nil {
			a.cfg = cfg
		}
	})
}
func (a *App) signIn() {
	// Ignore a stale menu/button callback after the reported state has changed.
	if a.engine == nil || !a.needsLogin() || a.busy || a.quitting || a.consentOpen || !a.cfg.HasConsent() {
		return
	}
	a.run(func() error {
		ctx, cancel := context.WithTimeout(a.ctx, 20*time.Second)
		defer cancel()
		raw, err := a.engine.BeginLogin(ctx)
		if err != nil {
			return err
		}
		if raw == "" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !validLoginURL(raw) {
			return fmt.Errorf("Tailscale returned an unexpected sign-in URL")
		}
		if err := mygo.Shell.OpenExternal(raw); err != nil {
			return fmt.Errorf("%s", a.t("browserError"))
		}
		return nil
	}, "")
}
func validLoginURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.User == nil && u.Port() == "" && (u.Hostname() == "login.tailscale.com" || u.Hostname() == "controlplane.tailscale.com")
}
