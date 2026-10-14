package app

import (
	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"strings"
)

func (a *App) view(c *ui.Context) {
	t := *c.Theme()
	t.Accent = ui.Hex("#168577")
	t.Radius = 9
	c.SetTheme(&t)
	ui.Column(c).Fill().Children(func() {
		ui.Column(c).Padding(24, 28, 18, 28).Gap(5).Children(func() {
			ui.Row(c).Gap(12).Children(func() {
				ui.Text(c, "TailBuddy").FontSize(29).Bold()
				ui.Spacer(c)
				ui.Text(c, "● "+a.statusLabel()).FontSize(12).TextColor(t.TextMuted)
			})
			ui.Text(c, a.t("tagline")).TextColor(t.TextMuted)
		})
		ui.Row(c).Padding(0, 28, 16, 28).Gap(8).Children(func() {
			for _, tab := range []string{"overview", "access", "about"} {
				b := ui.Button(c, a.t(tab))
				if a.tab == tab {
					b.Background(t.Accent.Alpha(.12)).TextColor(t.Accent)
				}
				if b.Clicked() {
					a.tab = tab
					a.message = ""
				}
			}
		})
		ui.Divider(c)
		ui.Scroll(c).Grow(1).Padding(24, 28).Gap(20).Children(func() {
			if a.fatal != "" {
				a.notice(c, a.fatal, true)
			}
			if a.message != "" {
				a.notice(c, a.message, false)
			}
			if a.snapshot.LastError != "" {
				a.notice(c, a.snapshot.LastError, true)
			}
			switch a.tab {
			case "access":
				a.accessView(c)
			case "about":
				a.aboutView(c)
			default:
				a.overviewView(c)
			}
		})
		ui.Divider(c)
		ui.Text(c, a.t("noTunnel")).Padding(12, 28).FontSize(11).TextColor(t.TextMuted)
	})
}
func (a *App) notice(c *ui.Context, text string, danger bool) {
	color := c.Theme().Accent
	if danger {
		color = c.Theme().Danger
	}
	ui.Text(c, text).Padding(12).Background(color.Alpha(.08)).Border(1, color.Alpha(.25)).Radius(8).TextColor(color).Selectable()
}
func (a *App) card(c *ui.Context, label string, body func()) {
	t := c.Theme()
	ui.Column(c).Gap(12).Padding(20).Radius(12).Border(1, t.Border).Background(t.Surface).Children(func() { ui.Text(c, label).FontSize(11).Bold().TextColor(t.TextMuted); body() })
}
func (a *App) pair(c *ui.Context, label, value string) {
	if value == "" {
		value = a.t("none")
	}
	ui.Column(c).Gap(4).Children(func() { ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted); ui.Text(c, value).Selectable() })
}
func (a *App) overviewView(c *ui.Context) {
	a.card(c, a.t("status"), func() {
		if !a.snapshot.Active {
			ui.Text(c, a.t("resting")).FontSize(24).Bold()
			ui.Text(c, a.t("restingHelp")).TextColor(c.Theme().TextMuted)
		} else {
			ui.Text(c, a.statusLabel()).FontSize(24).Bold()
			a.pair(c, a.t("identity"), first(a.snapshot.DNSName, a.snapshot.Hostname, a.cfg.Hostname))
			a.pair(c, a.t("address"), strings.Join(a.snapshot.TailscaleIPs, "\n"))
			if help := a.statusHelp(); help != "" {
				ui.Text(c, help).TextColor(c.Theme().TextMuted)
			}
			if a.snapshot.BackendState == "NeedsMachineAuth" {
				ui.Link(c, a.t("admin"), "https://login.tailscale.com/admin/machines")
			}
		}
		ui.Row(c).Gap(10).Wrap().Children(func() {
			if a.snapshot.Active {
				if ui.Button(c, a.t("disconnect")).Disabled(a.busy).Clicked() {
					a.disconnect()
				}
				if a.needsLogin() {
					if ui.PrimaryButton(c, a.t("login")).Disabled(a.busy || a.quitting).Clicked() {
						a.signIn()
					}
				}
			} else {
				if ui.PrimaryButton(c, a.t("connect")).Disabled(a.busy || a.engine == nil).Clicked() {
					a.connect()
				}
			}
			if a.busy {
				ui.Text(c, a.t("working")).TextColor(c.Theme().TextMuted)
			}
		})
	})
	a.card(c, a.t("scope"), func() {
		ui.Text(c, a.t("scopeTitle")).FontSize(18).Bold()
		ui.Text(c, a.t("scopeDetail"))
		ui.Checkbox(c, &a.accessConsent, a.t("consent")).Disabled(a.snapshot.Active || a.busy)
		ui.Checkbox(c, &a.credentialConsent, a.t("persist")).Disabled(a.snapshot.Active || a.busy)
		ui.Text(c, a.t("credentials")).FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Text(c, a.t("limits")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
	if a.subnets && !a.snapshot.Active {
		a.notice(c, a.t("routeWarning"), true)
		ui.Checkbox(c, &a.routeConsent, a.t("routeConsent"))
		ui.Text(c, strings.Join(strings.FieldsFunc(a.routes, func(r rune) bool { return r == '\n' || r == ',' }), ", ")).Selectable()
	}
	if len(a.snapshot.AdvertisedRoutes) > 0 {
		a.routeStatus(c)
	}
	if len(a.snapshot.Health) > 0 {
		a.pair(c, a.t("health"), strings.Join(a.snapshot.Health, "\n"))
	}
}
func (a *App) accessView(c *ui.Context) {
	a.card(c, a.t("hostname"), func() {
		ui.TextInput(c, &a.cfg.Hostname).Label(a.t("hostname")).Disabled(a.busy)
		ui.Text(c, a.t("hostHelp")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
	a.card(c, a.t("subnets"), func() {
		if ui.Checkbox(c, &a.subnets, a.t("subnets")).Disabled(a.busy).Changed() {
			a.routeConsent = false
		}
		ui.Text(c, a.t("subnetsHelp")).TextColor(c.Theme().TextMuted)
		if a.subnets {
			ui.Text(c, a.t("routesLabel"))
			if ui.TextArea(c, &a.routes).Height(95).Label(a.t("routesLabel")).Placeholder(a.t("routesPlaceholder")).Disabled(a.busy).Changed() {
				a.routeConsent = false
			}
			a.notice(c, a.t("routeWarning"), true)
			ui.Checkbox(c, &a.routeConsent, a.t("routeConsent")).Disabled(a.busy)
		}
		if ui.PrimaryButton(c, a.t("save")).Disabled(a.busy || a.engine == nil).Clicked() {
			a.save()
		}
	})
	a.routeStatus(c)
	ui.Link(c, a.t("admin"), "https://login.tailscale.com/admin/machines")
	a.card(c, a.t("security"), func() {
		ui.Text(c, a.t("securityHelp"))
		ui.Text(c, a.t("limits")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}
func (a *App) routeStatus(c *ui.Context) {
	a.card(c, a.t("subnets"), func() {
		routes := strings.Join(a.snapshot.AdvertisedRoutes, ", ")
		if routes == "" {
			routes = a.t("routesEmpty")
		}
		a.pair(c, a.t("advertised"), routes)
		approved := strings.Join(a.snapshot.ApprovedRoutes, ", ")
		if approved == "" {
			approved = a.t("approvalUnknown")
		}
		a.pair(c, a.t("approved"), approved)
		a.pair(c, a.t("reachability"), a.t("notVerified"))
	})
}
func (a *App) aboutView(c *ui.Context) {
	a.card(c, "TailBuddy  "+Version, func() {
		ui.Text(c, a.t("technology"))
		ui.Text(c, a.t("independent")).FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Link(c, a.t("source"), "https://github.com/ImSingee/TailBuddy")
	})
	a.card(c, a.t("language"), func() {
		ui.Text(c, a.t("languageHelp"))
		ui.Text(c, a.t("restartLanguage")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
	a.card(c, a.t("launch"), func() {
		if ui.Checkbox(c, &a.loginItem, a.t("launch")).Changed() {
			if err := mygo.App.SetOpenAtLogin(a.loginItem); err != nil {
				a.message = err.Error()
				a.loginItem = mygo.App.OpenAtLogin()
			}
		}
		ui.Text(c, a.t("launchHelp")).FontSize(12).TextColor(c.Theme().TextMuted)
	})
}
func first(v ...string) string {
	for _, s := range v {
		if s != "" {
			return s
		}
	}
	return ""
}
