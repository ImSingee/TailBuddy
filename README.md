# TailBuddy

[简体中文](README.zh-CN.md)

**Access services on your Mac from your other Tailscale devices, while keeping your usual internet and proxy setup.**

TailBuddy lives in the macOS menu bar and gives your Mac a separate identity on your private Tailscale network. Use it to reach a development site, a local dashboard or another service when you are away from your desk.

## What you can do

- **Reach local services remotely.** Use TailBuddy's Tailscale address with the service's usual port, without setting up each port separately.
- **Keep your existing proxy setup.** TailBuddy is designed to work alongside proxy apps without taking over your Mac's everyday internet connection. Compatibility depends on your proxy's settings.
- **Leave it in the menu bar.** Check its status, connect, disconnect and open settings whenever you need to.
- **Optionally share a subnet.** Allow access to a network your Mac can reach. This is off by default.
- **Use English or Simplified Chinese.** TailBuddy follows macOS language and appearance settings, including the language chosen for this app.

## Install

Requires macOS 12 or later. Apple silicon and Intel downloads are available.

1. Open [Downloads](https://github.com/ImSingee/TailBuddy/actions/workflows/ci.yml) and select the latest successful run. Under Artifacts, choose `TailBuddy-macos-arm64` for Apple silicon or `TailBuddy-macos-amd64` for Intel. GitHub sign-in is required.
2. Unzip the download, then the app zip inside it.
3. Move `TailBuddy.app` to Applications and open it.

The app is not Apple-notarized, so macOS may block it from opening.

## Get connected

1. Click **Connect**. On your first connection, a dialog asks you to approve inbound access and saving your login. This approval is remembered.
2. If asked, click **Sign in to Tailscale** and sign in to the network you want to use.
3. From another permitted device on that network, open the service using TailBuddy's displayed address and its usual port. For example, a website running on port 3000 is available at `http://<TailBuddy-address>:3000`.

Closing the window leaves TailBuddy running. **Quit** stops access; reopening the app resumes your previous connection, including when opened at Mac login. **Disconnect** stops access and keeps the node disconnected on future launches until you choose **Connect** again. Both keep your saved login.

Upgrading from an older version? Your saved login and approvals are kept. Click **Connect** once to resume and remember your connection choice; you do not need to approve the same access again.

### Optional subnet access

In **Access settings**, enable **Advertise subnets**, enter the network ranges you want to share and confirm the access notice. Save to apply them. Turn the option off and save to stop sharing those subnets.

Your Tailscale administrator may need to approve the routes. Networks with automatic approval may enable them immediately. Access rules and the connecting device's settings must also allow the connection; saving a subnet does not by itself confirm it is reachable.

### Choose a language

TailBuddy follows your Mac's language by default. To change it for this app: **System Settings → General → Language & Region → Applications → TailBuddy**, then reopen TailBuddy.

## Before you connect

- All supported local ports, including newly started services, can become reachable by devices your Tailscale rules allow. Protect private dashboards and admin tools with authentication.
- Some services may treat incoming connections as local access. Do not rely on “localhost only” as your only protection.
- Local service access covers TCP/UDP services available at `127.0.0.1`, with some Tailscale-reserved ports excluded. Services listening only on another address may not work.
- Share only the subnets you intend to make accessible. Proxy or VPN settings may affect connectivity.

## License

[MIT](LICENSE)
