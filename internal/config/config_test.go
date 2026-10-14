package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestValidateRoutes(t *testing.T) {
	good := []string{"192.168.1.7/24", "10.4.0.1/32", "fd12:3456:789a::/48", "2001:db8::/32"}
	c, err := Validate(Config{Hostname: " My-Mac ", AdvertiseRoutes: append(good, "192.168.1.0/24")})
	if err != nil {
		t.Fatal(err)
	}
	if c.Hostname != "my-mac" || len(c.AdvertiseRoutes) != 4 || !strings.Contains(strings.Join(c.AdvertiseRoutes, ","), "192.168.1.0/24") {
		t.Fatalf("unexpected normalization: %+v", c)
	}
	for _, route := range []string{"0.0.0.0/0", "::/0", "127.0.0.1/32", "100.64.1.1/32", "100.0.0.0/8", "224.0.0.0/4", "192.0.0.0/2", "169.254.1.0/24", "fe80::/64", "ff00::/8", "fd7a:115c:a1e0::/48", "fd00::/8", "::1/128", "::/64", "::ffff:10.0.0.0/120", "invalid", "192.168.1.0/33", "0.0.0.0/8", "255.255.255.255/32"} {
		t.Run(route, func(t *testing.T) {
			if _, err := Validate(Config{AdvertiseRoutes: []string{route}}); err == nil {
				t.Errorf("accepted forbidden %q", route)
			}
		})
	}
}
func TestValidateHostname(t *testing.T) {
	for _, s := range []string{"-bad", "bad-", "a.b", "a b", "a;rm", strings.Repeat("a", 64), "中文"} {
		if _, err := Validate(Config{Hostname: s}); err == nil {
			t.Errorf("accepted %q", s)
		}
	}
	for _, s := range []string{"", "a", "my-mac", strings.Repeat("a", 63)} {
		if _, err := Validate(Config{Hostname: s}); err != nil {
			t.Errorf("rejected %q: %v", s, err)
		}
	}
}
func TestConfigRoundTripPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	c := Default()
	c.ConsentAccepted = true
	c.PersistCredentials = true
	if err := Save(dir, c); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("got %+v want %+v", got, c)
	}
	for p, mode := range map[string]os.FileMode{dir: 0700, filepath.Join(dir, "config.json"): 0600} {
		fi, err := os.Stat(p)
		if err != nil || fi.Mode().Perm() != mode {
			t.Fatalf("private mode %s: %v %v", p, fi, err)
		}
	}
}
func TestRejectLinkedSharedState(t *testing.T) {
	t.Run("directory symlink", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "real")
		os.Mkdir(target, 0700)
		link := filepath.Join(root, "link")
		os.Symlink(target, link)
		if EnsurePrivateDir(link) == nil {
			t.Fatal("accepted symlink")
		}
	})
	t.Run("file symlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "real")
		os.WriteFile(target, []byte("{}"), 0600)
		os.Symlink(target, filepath.Join(dir, "config.json"))
		if _, err := Load(dir); err == nil {
			t.Fatal("read symlink")
		}
		if Save(dir, Default()) == nil {
			t.Fatal("replaced symlink")
		}
	})
	t.Run("hardlink", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "real")
		os.WriteFile(target, []byte("{}"), 0600)
		os.Link(target, filepath.Join(dir, "config.json"))
		if _, err := Load(dir); err == nil {
			t.Fatal("read hardlink")
		}
	})
	t.Run("shared dir", func(t *testing.T) {
		dir := t.TempDir()
		os.Chmod(dir, 0755)
		if EnsurePrivateDir(dir) == nil {
			t.Fatal("accepted shared dir")
		}
	})
	t.Run("shared file", func(t *testing.T) {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}"), 0644)
		if _, err := Load(dir); err == nil {
			t.Fatal("read shared file")
		}
	})
}
func TestLoadInvalidConfig(t *testing.T) {
	for _, data := range []string{`{"proxy":"yes"}`, `{} {}`, strings.Repeat("x", 65537)} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "config.json"), []byte(data), 0600)
		if _, err := Load(dir); err == nil {
			t.Fatal("accepted invalid JSON")
		}
	}
}
func TestDefaultsNeverConsent(t *testing.T) {
	c := Default()
	if c.ConsentAccepted || c.PersistCredentials || c.SubnetConsentAccepted || len(c.AdvertiseRoutes) != 0 {
		t.Fatal("unsafe defaults")
	}
}
