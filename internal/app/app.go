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
	words                                          i18n.Catalog
	engine                                         *engine.Engine
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
	subnets                                        bool
	accessConsent, credentialConsent, routeConsent bool
	loginItem                                      bool
	quitting, readyToQuit                          bool
	cancel                                         context.CancelFunc
}

func New() *App {
	return &App{words: i18n.New("en"), tab: "overview", cfg: config.Default(), ctx: context.Background()}
}
func (a *App) t(s string) string { return a.words.T(s) }

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
			a.engine, err = engine.New(engine.Options{HelperPath: helper, StateDir: a.stateDir})
			if err != nil {
				a.fatal = err.Error()
			}
		}
	}
	a.routes = strings.Join(a.cfg.AdvertiseRoutes, "\n")
	a.subnets = len(a.cfg.AdvertiseRoutes) > 0
	// Each process launch begins disconnected. Consent is reviewed before Connect.
	a.accessConsent = false
	a.credentialConsent = false
	a.routeConsent = false
	a.menu = mygo.NewMenu([]*mygo.MenuItem{
		{ID: "status", Label: "TailBuddy · " + a.t("offline"), Disabled: true},
		{Label: a.t("settings"), Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
		{ID: "login", Label: a.t("login"), Disabled: true, Click: func(*mygo.MenuItem, *mygo.Window) { a.signIn() }},
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
	if !mygo.App.WasOpenedAtLogin() || a.fatal != "" {
		a.show()
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
		a.menu.ItemByID("login").SetEnabled(a.snapshot.Active && a.snapshot.BackendState != "Running" && !a.busy)
		a.menu.ItemByID("disconnect").SetEnabled(a.snapshot.Active && !a.busy)
	}
	if a.tray != nil {
		a.tray.SetToolTip("TailBuddy · " + a.statusLabel())
	}
	a.redraw()
}
func (a *App) statusLabel() string {
	if a.snapshot.LastError != "" || a.fatal != "" {
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
	case "Stopped":
		return a.t("stopped")
	case "Starting":
		return a.t("starting")
	default:
		return a.t("connecting")
	}
}
func (a *App) run(action func() error, success string) {
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
		mygo.RunOnMain(func() {
			a.busy = false
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
	cfg.ConsentAccepted = a.accessConsent
	cfg.PersistCredentials = a.credentialConsent
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
	if a.engine == nil {
		return
	}
	if !a.accessConsent || !a.credentialConsent {
		a.message = a.t("needsConsent")
		return
	}
	cfg, err := a.candidate()
	if err != nil {
		a.message = err.Error()
		return
	}
	a.cfg = cfg
	a.run(func() error {
		if err := config.Save(a.stateDir, cfg); err != nil {
			return err
		}
		return a.engine.Start(a.ctx, cfg)
	}, "")
}
func (a *App) disconnect() {
	if a.engine == nil {
		return
	}
	a.run(func() error {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return a.engine.Stop(ctx)
	}, "")
}
func (a *App) save() {
	cfg, err := a.candidate()
	if err != nil {
		a.message = err.Error()
		return
	}
	a.cfg = cfg
	running := a.snapshot.Active
	a.run(func() error {
		if running {
			ctx, cancel := context.WithTimeout(a.ctx, 15*time.Second)
			defer cancel()
			if err := a.engine.ApplyConfig(ctx, cfg); err != nil {
				return err
			}
		}
		return config.Save(a.stateDir, cfg)
	}, a.t("saved"))
}
func (a *App) signIn() {
	if a.engine == nil || !a.snapshot.Active {
		a.message = a.t("connectFirst")
		a.show()
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
