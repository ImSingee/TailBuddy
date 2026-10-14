package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ImSingee/TailBuddy/internal/config"
)

const localAPIBase = "http://local-tailscaled.sock/localapi/v0"
const maximumResponse = 2 << 20

type localAPI struct {
	socket string
	client *http.Client
}

func newLocalAPI(socket string) *localAPI {
	a := &localAPI{socket: socket}
	a.client = &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("LocalAPI redirects are forbidden") },
		Transport: &http.Transport{
			Proxy: nil, DisableKeepAlives: true,
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if addr != "local-tailscaled.sock:80" {
					return nil, errors.New("unexpected LocalAPI host")
				}
				if err := checkSocket(socket, true); err != nil {
					return nil, err
				}
				return (&net.Dialer{}).DialContext(ctx, "unix", socket)
			},
		},
	}
	return a
}

func checkSocket(path string, tighten bool) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return errors.New("private LocalAPI socket is not ready")
	}
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if fi.Mode()&os.ModeSocket == 0 || !ok || int(stat.Uid) != os.Geteuid() {
		return errors.New("LocalAPI socket is not a current-user-owned Unix socket")
	}
	if fi.Mode().Perm()&0077 != 0 {
		if !tighten {
			return errors.New("LocalAPI socket is not private")
		}
		if err := os.Chmod(path, 0600); err != nil {
			return errors.New("cannot make LocalAPI socket private")
		}
	}
	return nil
}

func (a *localAPI) request(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return errors.New("cannot encode LocalAPI request")
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, localAPIBase+path, body)
	if err != nil {
		return errors.New("cannot construct LocalAPI request")
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := a.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("private LocalAPI is unavailable")
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maximumResponse+1))
	if err != nil {
		return errors.New("cannot read LocalAPI response")
	}
	if len(data) > maximumResponse {
		return errors.New("LocalAPI response exceeds safe size limit")
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		// Never include response bodies: errors can contain auth URLs or credentials.
		return fmt.Errorf("LocalAPI %s request failed (HTTP %d)", method, res.StatusCode)
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return errors.New("invalid LocalAPI JSON response")
		}
	}
	return nil
}

type daemonStatus struct {
	BackendState string
	AuthURL      string
	TailscaleIPs []string
	Health       []string
	Self         *struct {
		HostName      string
		DNSName       string
		Online        bool
		PrimaryRoutes *[]string
		AllowedIPs    *[]string
	}
}

type daemonPrefs struct {
	Hostname        string
	AdvertiseRoutes []string
	CorpDNS         bool
	RouteAll        bool
	ExitNodeID      string
	ExitNodeIP      string
	AutoExitNode    string
	RunSSH          bool
	RunWebClient    bool
	WantRunning     bool
	ShieldsUp       bool
	RemoteConfig    bool
}

func safePrefs(c config.Config, running bool) map[string]any {
	routes := append([]string{}, c.AdvertiseRoutes...)
	return map[string]any{
		"ControlURL": "https://controlplane.tailscale.com",
		"Hostname":   c.Hostname, "WantRunning": running, "LoggedOut": false,
		"AdvertiseRoutes": routes, "AdvertiseServices": []string{}, "AdvertiseTags": []string{},
		"CorpDNS": false, "RouteAll": false, "ExitNodeID": "", "ExitNodeIP": "", "AutoExitNode": "",
		"ExitNodeAllowLANAccess": false, "ShieldsUp": false, "RunSSH": false, "RunWebClient": false,
		"ForceDaemon": false, "OperatorUser": "", "NetfilterMode": 0, "NoSNAT": false,
		"AutoUpdate":   map[string]any{"Check": false, "Apply": false},
		"AppConnector": map[string]any{"Advertise": false}, "RemoteConfig": false,
	}
}

// SafeBootConfig overrides persisted WantRunning before the first control-plane
// connection. An explicit empty route list clears any stale advertisement.
// This alpha0 format is pinned and tested with tailscaled v1.104.1.
func safeBootConfig() []byte {
	return []byte(`{"version":"alpha0","locked":false,"enabled":false,"acceptDNS":false,"acceptRoutes":false,"exitNode":"","advertiseRoutes":[],"advertiseExitNode":false,"shieldsUp":true,"runSSHServer":false,"runWebClient":false,"remoteConfig":false,"netfilterMode":"off","autoUpdate":{"Check":false,"Apply":false}}`)
}

func (a *localAPI) configure(ctx context.Context, c config.Config) error {
	// Start's UpdatePrefs preserves the daemon's credential state but replaces
	// user preferences as a whole, so inherited tags/proxies/exit nodes cannot leak.
	if err := a.request(ctx, http.MethodPost, "/start", map[string]any{"UpdatePrefs": safePrefs(c, true)}, nil); err != nil {
		return err
	}
	return a.verifyPrefs(ctx, c)
}

func (a *localAPI) update(ctx context.Context, c config.Config) error {
	prefs := safePrefs(c, true)
	keys := make([]string, 0, len(prefs))
	for key := range prefs {
		keys = append(keys, key)
	}
	for _, key := range keys {
		if key != "ControlURL" {
			prefs[key+"Set"] = true
		}
	}
	// AutoUpdate and AppConnector are masks with their own schemas.
	prefs["AutoUpdateSet"] = map[string]bool{"CheckSet": true, "ApplySet": true}
	return a.request(ctx, http.MethodPatch, "/prefs", prefs, nil)
}

func (a *localAPI) verifyPrefs(ctx context.Context, c config.Config) error {
	var p daemonPrefs
	if err := a.request(ctx, http.MethodGet, "/prefs", nil, &p); err != nil {
		return err
	}
	if p.CorpDNS || p.RouteAll || p.ExitNodeID != "" || p.ExitNodeIP != "" || p.AutoExitNode != "" || p.RunSSH || p.RunWebClient || p.RemoteConfig {
		return errUnsafePreferences
	}
	normalized, err := config.Validate(config.Config{AdvertiseRoutes: p.AdvertiseRoutes})
	if err != nil || !slices.Equal(normalized.AdvertiseRoutes, c.AdvertiseRoutes) {
		return errUnsafePreferences
	}
	return nil
}

func (a *localAPI) status(ctx context.Context) (daemonStatus, error) {
	var s daemonStatus
	err := a.request(ctx, http.MethodGet, "/status?peers=false", nil, &s)
	return s, err
}

func (a *localAPI) stop(ctx context.Context) error {
	return a.request(ctx, http.MethodPatch, "/prefs", map[string]any{"WantRunning": false, "WantRunningSet": true, "ShieldsUp": true, "ShieldsUpSet": true}, nil)
}

// ValidateLoginURL permits only Tailscale's official HTTPS login origin.
// Authentication URLs must never be logged or persisted by TailBuddy.
func ValidateLoginURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "login.tailscale.com" || u.User != nil || u.Opaque != "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") {
		return "", errors.New("daemon returned an unexpected login origin")
	}
	return u.String(), nil
}

var urlInText = regexp.MustCompile(`(?i)https?://[^\s]+`)

func safeHealth(in []string) []string {
	out := make([]string, 0, min(len(in), 16))
	for i, s := range in {
		if i >= 16 {
			break
		}
		s = urlInText.ReplaceAllString(s, "[link hidden]")
		if len(s) > 512 {
			s = s[:512]
		}
		out = append(out, s)
	}
	return out
}
