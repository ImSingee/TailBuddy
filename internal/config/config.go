// Package config holds user choices. It contains no authentication tokens.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// Config deliberately has no proxy, DNS, route-acceptance, or exit-node settings.
// Consent flags must be set by an explicit user-facing consent flow.
type Config struct {
	Hostname              string   `json:"hostname"`
	AdvertiseRoutes       []string `json:"advertiseRoutes"`
	ConsentAccepted       bool     `json:"consentAccepted"`
	PersistCredentials    bool     `json:"persistCredentials"`
	SubnetConsentAccepted bool     `json:"subnetConsentAccepted"`
}

func Default() Config { return Config{Hostname: "tailbuddy", AdvertiseRoutes: []string{}} }

var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
var forbidden = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"), netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"), netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("224.0.0.0/4"), netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"), netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("::ffff:0:0/96"), netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"), netip.MustParsePrefix("fd7a:115c:a1e0::/48"),
}

// Validate normalizes prefixes and rejects any prefix overlapping a forbidden
// range, including broad ranges that contain a forbidden range. Host routes are
// allowed, but default routes and IPv4-mapped IPv6 are never accepted.
func Validate(c Config) (Config, error) {
	c.Hostname = strings.ToLower(strings.TrimSpace(c.Hostname))
	if c.Hostname != "" && !hostnamePattern.MatchString(c.Hostname) {
		return Config{}, errors.New("hostname must be one DNS label of 1–63 letters, digits, or internal hyphens")
	}
	if len(c.AdvertiseRoutes) > 64 {
		return Config{}, errors.New("at most 64 subnet routes are allowed")
	}
	seen := map[string]bool{}
	routes := make([]string, 0, len(c.AdvertiseRoutes))
	for _, raw := range c.AdvertiseRoutes {
		raw = strings.TrimSpace(raw)
		p, err := netip.ParsePrefix(raw)
		if err != nil || !p.IsValid() {
			return Config{}, fmt.Errorf("invalid subnet CIDR %q", raw)
		}
		p = p.Masked()
		if p.Bits() == 0 || p.Addr().Is4In6() {
			return Config{}, fmt.Errorf("default or IPv4-mapped route %q is not allowed", raw)
		}
		for _, block := range forbidden {
			if p.Overlaps(block) {
				return Config{}, fmt.Errorf("subnet %q overlaps a reserved, loopback, link-local, multicast, or Tailscale range", raw)
			}
		}
		s := p.String()
		if !seen[s] {
			routes = append(routes, s)
			seen[s] = true
		}
	}
	sort.Strings(routes)
	c.AdvertiseRoutes = routes
	return c, nil
}

func DefaultStateDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "TailBuddy"), nil
}

// EnsurePrivateDir refuses links and foreign owners rather than repairing them.
// A shared writable directory is never silently adopted for private state.
func EnsurePrivateDir(dir string) error {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir {
		return errors.New("state directory must be an absolute, clean path")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create private state directory: %w", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		return errors.New("state directory must be a real directory, not a link")
	}
	if err := owned(fi); err != nil {
		return err
	}
	if fi.Mode().Perm()&0077 != 0 {
		return errors.New("state directory permissions must be 0700; refusing shared state")
	}
	return nil
}

func owned(fi os.FileInfo) error {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("private state must be owned by the current user")
	}
	if fi.Mode().IsRegular() && stat.Nlink != 1 {
		return errors.New("private state must not have hard links")
	}
	return nil
}

// CheckPrivateFile rejects links, hard links, foreign owners and shared files.
// Missing files are allowed so callers can create them with O_EXCL/O_NOFOLLOW.
func CheckPrivateFile(path string) error {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("private state file must be a regular file, not a link")
	}
	if err := owned(fi); err != nil {
		return err
	}
	if fi.Mode().Perm()&0077 != 0 {
		return errors.New("private state file permissions must be 0600")
	}
	return nil
}

// CheckPrivateTree checks existing daemon state before it is read by the helper.
// Socket permissions are tightened separately after daemon startup.
func CheckPrivateTree(dir string) error {
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}
	return filepath.Walk(dir, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if err := owned(fi); err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return errors.New("links are not allowed inside private state")
		}
		if fi.Mode()&os.ModeSocket != 0 {
			return nil
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			return errors.New("unexpected special file inside private state")
		}
		if fi.Mode().Perm()&0077 != 0 {
			return errors.New("state directory contains a shared file or directory")
		}
		return nil
	})
}

func Load(dir string) (Config, error) {
	if err := EnsurePrivateDir(dir); err != nil {
		return Config{}, err
	}
	path := filepath.Join(dir, "config.json")
	if err := CheckPrivateFile(path); err != nil {
		return Config{}, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, 65537))
	if err != nil {
		return Config{}, err
	}
	if len(data) > 65536 {
		return Config{}, errors.New("configuration is too large")
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var c Config
	if err := d.Decode(&c); err != nil {
		return Config{}, errors.New("invalid configuration JSON")
	}
	if d.Decode(new(any)) != io.EOF {
		return Config{}, errors.New("trailing configuration JSON")
	}
	return Validate(c)
}

func Save(dir string, c Config) error {
	c, err := Validate(c)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return WritePrivateFile(dir, "config.json", append(data, '\n'))
}

// WritePrivateFile atomically replaces a private file without following links.
func WritePrivateFile(dir, name string, data []byte) error {
	if filepath.Base(name) != name || name == "." {
		return errors.New("invalid private filename")
	}
	if err := EnsurePrivateDir(dir); err != nil {
		return err
	}
	target := filepath.Join(dir, name)
	if err := CheckPrivateFile(target); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".tailbuddy-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
