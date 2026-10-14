# Acceptance and evidence

## Automated checks (safe, no tailnet)

- `go test -race -count=1 ./...`
- `go vet ./...`
- `gofmt -l cmd internal` is empty
- `scripts/build-macos.sh arm64` and `scripts/build-macos.sh amd64`
- On macOS, `codesign --verify --deep --strict` and `plutil -lint`
- Optional interface snapshots: `TAILBUDDY_SNAPSHOT_DIR=/tmp/tailbuddy-ui go test -count=1 ./internal/app`

The screenshot test renders the app's interface without opening a window. It does not verify macOS window integration, Tailscale login, transport or subnet reachability.

## Interactive acceptance (requires owner approval and a Mac)

Before live-network checks, explicitly approve joining the intended tailnet, saving node credentials, exposing the local test services, and the exact subnet routes. Do not use production admin services for initial checks.

1. Install the complete app. Verify menu bar icon, accessory activation (no persistent Dock icon), English / Chinese text, light / dark modes, keyboard navigation and window close/reopen.
2. Set system language English and app language 简体中文, reopen, verify native window and tray labels. Repeat reversed. Remove the app override and verify the system-supported locale. Test on both Apple silicon and Intel where available.
3. With no consent checked, Connect must refuse. Cancel browser login, retry, repeatedly click connection buttons, close/reopen the window mid-operation, and verify no duplicate node process or implicit login.
4. Approve a test tailnet and credentials. Confirm that the device is separate from any existing Tailscale installation. Snapshot host route table, interfaces and DNS before/after and verify no new TUN or host route/DNS changes by TailBuddy.
5. Run authenticated/test-only TCP and UDP echo services bound to `127.0.0.1` at several low/high non-reserved ports. From an authorized separate tailnet device, verify same-port delivery, multiple streams, IPv4 and Tailscale IPv6 destination handling, UDP idle/reconnect. Verify a denied ACL client cannot reach them. Observe the loopback source caveat.
6. Verify `::1`-only and LAN-only services are not advertised as supported by the localhost mapping. Verify reserved Tailscale services are understood as exceptions. Do not claim mDNS or arbitrary IP forwarding.
7. Advertise an explicitly approved test subnet. Verify the UI's request/primary-route/unverified distinction before and after admin approval. Test an auto-approver case, permitted/denied original clients, client accept-routes on/off, and a redundant router. Never infer reachability from advertisement alone.
8. Disable subnet advertisement, verify daemon prefs contain `[]`, and verify new remote subnet access ceases. Change routes during reconnect backoff and confirm removed routes never reappear.
9. Inspect only the app's private state permissions and process ownership; do not paste credentials into reports. Verify there is no TCP LocalAPI, SOCKS or HTTP proxy listener.
10. Disconnect, quit, crash the UI, and interrupt helper startup. Confirm the managed helper stops, ordinary port forwarding ceases and no stale lock permits duplicate daemons. Reopen without connecting: no services must become exposed from old persisted preferences.
11. Sleep/wake, change Wi-Fi, disconnect the network, and restore it. Verify accurate state and bounded recovery, then retest disconnect. Observe behavior with the user's existing proxy/VPN configuration without changing its security settings.
12. Reopen at login: the app may appear in the menu bar, but the node must remain disconnected until explicit Connect. Review macOS login-item settings and remove the login item if desired.

## Evidence labels

Record the exact commit, macOS version, architecture and verification result. Separate **passed**, **failed**, and **not run**. Automated mocked-network tests are never a substitute for the explicitly authorized live checks above. Development bundles are ad-hoc signed only, not Developer ID signed or notarized.
