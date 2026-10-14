package app

import (
	"fmt"
	"github.com/ImSingee/TailBuddy/internal/config"
	"github.com/ImSingee/TailBuddy/internal/i18n"
	"github.com/egoist/mygo/ui"
	"image/png"
	"os"
	"testing"
)

func TestInitialViewKeepsConsentInDialog(t *testing.T) {
	a := New()
	tt := ui.NewTester(a.view, 760, 1000)
	for _, key := range []string{"resting", "scopeTitle", "noTunnel"} {
		if !tt.HasText(a.t(key)) {
			t.Errorf("missing %s", key)
		}
	}
	for _, key := range []string{"consent", "persist"} {
		if tt.HasText(a.t(key)) {
			t.Errorf("persistent checkbox %s", key)
		}
	}
	if a.cfg.HasConsent() || a.accessConsent || a.credentialConsent || a.subnets {
		t.Fatal("unsafe defaults")
	}
}
func TestChineseViewAndNavigation(t *testing.T) {
	a := New()
	a.words = i18n.New("zh-Hans")
	tt := ui.NewTester(a.view, 760, 1200)
	if !tt.HasText("准备就绪") {
		t.Fatal("Chinese view missing")
	}
	if err := tt.Click("访问设置"); err != nil {
		t.Fatal(err)
	}
	if a.tab != "access" || !tt.HasText(a.t("hostname")) {
		t.Fatal("settings tab failed")
	}
	if err := tt.Click("关于"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(a.t("languageHelp")) {
		t.Fatal("system language guidance missing")
	}
	if err := tt.Click("概览"); err != nil {
		t.Fatal(err)
	}
	if a.tab != "overview" {
		t.Fatal("return to overview failed")
	}
}
func TestSubnetEditingNeedsNewConsent(t *testing.T) {
	a := New()
	a.cfg = config.Default()
	a.subnets = true
	a.routes = "192.168.50.1/24"
	if _, err := a.candidate(); err == nil {
		t.Fatal("unapproved subnet accepted")
	}
	a.routeConsent = true
	cfg, err := a.candidate()
	if err != nil || len(cfg.AdvertiseRoutes) != 1 || cfg.AdvertiseRoutes[0] != "192.168.50.0/24" {
		t.Fatalf("%+v %v", cfg, err)
	}
	a.subnets = false
	cfg, err = a.candidate()
	if err != nil || len(cfg.AdvertiseRoutes) != 0 {
		t.Fatal("subnet switch must clear advertisement")
	}
}
func TestLoginAllowlist(t *testing.T) {
	for _, bad := range []string{"http://login.tailscale.com/a/secret", "https://login.tailscale.com.evil.test/a/secret", "https://login.tailscale.com:8443/a/secret", "https://user@login.tailscale.com/a/secret", "file:///tmp/secret"} {
		if validLoginURL(bad) {
			t.Fatalf("accepted %q", bad)
		}
	}
	if !validLoginURL("https://login.tailscale.com/a/example") {
		t.Fatal("valid official origin rejected")
	}
}
func TestBusyPreventsDuplicateActions(t *testing.T) {
	a := New()
	a.busy = true
	called := false
	a.run(func() error { called = true; return nil }, "")
	if called {
		t.Fatal("duplicate work executed")
	}
}
func TestNativeViewSnapshots(t *testing.T) {
	dir := os.Getenv("TAILBUDDY_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set TAILBUDDY_SNAPSHOT_DIR for headless native-rendered PNGs")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, dark := range []bool{false, true} {
			a := New()
			a.words = i18n.New(lang)
			tt := ui.NewTester(a.view, 760, 1000)
			tt.SetDark(dark)
			name := lang
			if dark {
				name += "-dark"
			}
			f, err := os.Create(dir + "/" + name + ".png")
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestConsentDialogSnapshots(t *testing.T) {
	dir := os.Getenv("TAILBUDDY_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set TAILBUDDY_SNAPSHOT_DIR for dialog PNGs")
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, size := range [][2]int{{760, 820}, {620, 620}} {
			a := New()
			a.words = i18n.New(lang)
			a.consentOpen = true
			tt := ui.NewTester(a.view, size[0], size[1])
			f, err := os.Create(fmt.Sprintf("%s/%s-consent-%dx%d.png", dir, lang, size[0], size[1]))
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, tt.Image())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}
