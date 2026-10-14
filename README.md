# TailBuddy

[简体中文](README.zh-CN.md)

A macOS menu bar app with an independent Tailscale identity, written in **MyGo Native**. It runs the official open-source `tailscaled` as a private, unprivileged userspace child. There is no webview and no dependency on the official Tailscale GUI app.

## What it does

- Receive tailnet TCP and UDP traffic and forward it to **127.0.0.1 at the same destination port**. No 65,535-port listener farm or host TUN is created.
- Optionally advertise explicit IPv4/IPv6 subnet CIDRs through the Mac's existing connectivity. Disabled by default; default/exit routes and dangerous reserved ranges are rejected.
- Keep connection status, sign-in, disconnect, access settings and quit in the menu bar. Closing the settings window keeps the app running; quitting stops the managed node.
- Follow macOS appearance and **system / per-application language preferences**. English and Simplified Chinese are bundled. Locale selection uses `NSBundle.preferredLocalizations`, not `LANG`.

This is a TCP/UDP userspace gateway, **not a transparent VPN**. It does not create a host TUN, modify host routing or DNS, accept tailnet routes, act as an exit node, or offer an outbound SOCKS/HTTP proxy. Tailscale's own transport still makes outbound network connections to its control plane, peers and relays.

## Install

macOS 12+; Apple silicon and Intel builds are packaged separately.

1. Open [Actions → Verify and package](https://github.com/ImSingee/TailBuddy/actions/workflows/ci.yml), choose a successful run for the commit you want, and download `TailBuddy-macos-arm64` (Apple silicon) or `TailBuddy-macos-amd64` (Intel). GitHub requires a sign-in to download Actions artifacts.
2. Unzip the artifact and then the app zip inside it. Move `TailBuddy.app` to Applications.
3. The builds use **ad-hoc signing only**. They are **not Developer ID signed or notarized**. macOS may block an unnotarized app. Review the source and build provenance and follow your organization’s installation policy; a Developer ID-signed, notarized distribution is not currently provided.
4. Open TailBuddy, review the access warnings and explicitly authorize both inbound access and credential storage. Click Connect, then Sign in to Tailscale. This creates a separate tailnet device.

No connection or sign-in happens just by opening the app. Open-at-login launches the menu bar app only, not the node. You must explicitly connect each app session; once connected, a bounded supervisor recovers from ordinary helper failures. The Tailscale engine handles network changes, and status refreshes after wake.

### Language

System Settings → General → Language & Region → Applications → add TailBuddy → choose English or Simplified Chinese → reopen TailBuddy. The bundle declares `en` and `zh-Hans`, and includes matching `.lproj` resources. The macOS default applies when no app override is set. The command-line development binary is not an app bundle, so test this with the packaged `.app`.

## Read this before connecting

**Every local TCP/UDP service becomes a potential tailnet destination**, subject to tailnet access rules. That includes localhost-only admin panels and development servers that previously relied on loopback for protection. The destination often sees the connection as coming from localhost, not the original tailnet IP. Use real application authentication and restrictive Tailscale grants/ACLs; do not treat localhost trust as authentication.

- Mapping targets IPv4 loopback `127.0.0.1`. Services bound only to `::1`, only to a LAN address, or to Unix sockets are not mapped. Wildcard listeners that accept IPv4 loopback work.
- Tailscale-internal/reserved services take precedence over generic forwarding. Ports used by Tailscale services are not an unconditional guarantee of localhost forwarding.
- Non-TCP/UDP traffic, multicast/mDNS/broadcast discovery and arbitrary IP protocols are outside the all-port forwarding promise.
- Subnet advertisement is separate from approval and reachability. Existing `autoApprovers` may activate a route immediately; otherwise an admin must approve it. The original client's access rules and route acceptance must also allow it. TailBuddy never changes approvals or ACLs for you.
- The UI separates requested advertisements, Tailscale-reported primary routes, and unverified end-to-end reachability. An absent primary route does not prove rejection, especially with redundant routers. Check the admin console and test from an authorized remote device.
- Disabling subnet advertisement explicitly writes an empty route list to the running daemon. It does not turn on `accept-routes`.
- Other proxy/VPN software can still intercept Tailscale's UDP or DERP transport. No blanket compatibility guarantee is possible.

## Build from source

Requires Go **1.27.1+**, Python 3, and `zip` (Linux cross-builds). Builds fetch pinned public dependencies from the official Go module ecosystem; no credentials are needed.

```sh
git clone https://github.com/ImSingee/TailBuddy.git
cd TailBuddy
go test -race ./...
go vet ./...
./scripts/build-macos.sh arm64
# Or: ./scripts/build-macos.sh amd64
```

The script builds the native app and **official `tailscaled` v1.104.1** from source, creates a complete `.app`, retains dependency license files, and outputs `build/darwin-<arch>/TailBuddy-0.1.0-macos-<arch>.zip` plus `SHA256SUMS`. On macOS it ad-hoc signs and verifies the bundle. Linux can cross-compile both architectures; this does **not** prove the app ran on macOS.

MyGo **v0.4.8** is pinned in `go.mod`/`go.sum`. Runtime calls use MyGo's `ui.View` and native desktop APIs. The application package contains no JavaScript runtime, HTML UI or browser shell. `mygo.json` documents app metadata; the packaging script additionally includes the networking helper and licenses.

## Privacy and lifecycle

State lives in `~/Library/Application Support/TailBuddy`, with a private `0700` directory and `0600` state/settings files. The app refuses links, shared state, foreign owners and unsafe helpers. LocalAPI uses a Unix socket inside this private directory, never a TCP management port. Same-user malware can still access a user's processes/files; this is not an isolation boundary against malicious code running as you.

The app strips inherited proxy and Tailscale environment overrides from its helper, does not offer auth-key entry, discards daemon stdout/stderr, disables Tailscale log uploads, and suppresses login URLs from diagnostics. Tailscale still exchanges the identity and connection metadata required to operate the node. No secret is committed to this repository.

A parent-lifetime pipe and exclusive state lock keep the managed daemon tied to the app. Disconnect/quit stop incoming access, but retain login credentials for a later explicit connection. Startup uses a fixed, disabled configuration with an empty route list before applying the current reviewed settings, preventing stale saved route preferences from briefly resuming. The pinned upstream config format is `alpha0`; dependency upgrades must re-validate it.

To remove the device entirely, disconnect and quit, remove the TailBuddy device in the Tailscale admin console, then delete TailBuddy's private application-support folder yourself. Deleting this folder loses its local node identity; reconnecting creates a new identity.

## Verification

CI runs unit tests, race tests, `go vet`, formatting checks, native-platform tests on macOS runners, both architecture builds, bundle/plist inspection, and ad-hoc signature verification. Unit tests use mocked LocalAPI and fake helper processes. CI never joins a tailnet or exposes real services.

See [manual acceptance checks](docs/ACCEPTANCE.md) for the remaining interactive sign-in, port, subnet, per-app language, sleep/wake and proxy coexistence checks. Successful compilation or headless UI rendering must not be described as completed live-network acceptance.

## Architecture and upstream evidence

- [Tailscale: userspace vs kernel routers](https://tailscale.com/docs/reference/kernel-vs-userspace-routers)
- [Tailscale userspace networking](https://tailscale.com/docs/features/userspace-networking)
- [Pinned netstack implementation](https://github.com/tailscale/tailscale/blob/v1.104.1/wgengine/netstack/netstack.go)
- [Pinned daemon userspace setup](https://github.com/tailscale/tailscale/blob/v1.104.1/cmd/tailscaled/tailscaled.go)
- [MyGo Native](https://mygo.egoist.dev/docs/ui)
- [Apple bundle localization matching](https://developer.apple.com/documentation/foundation/bundle/preferredlocalizations)

## License

TailBuddy original code: MIT. MyGo: MIT. Tailscale open-source engine: BSD-3-Clause; transitive dependencies retain their respective licenses. Packaged notices are in `Contents/Resources/OpenSource`. TailBuddy is independent software and is not affiliated with Tailscale Inc.
