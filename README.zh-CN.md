# TailBuddy

[English](README.md)

使用 **MyGo Native** 实现的 macOS 菜单栏常驻应用，让这台 Mac 以独立的 Tailscale 节点接收入站连接。使用官方开源 `tailscaled` 用户态引擎，无 WebView，无需同时运行官方 Tailscale 图形应用。

## 功能与边界

- 发往节点的 TCP / UDP 流量转发至 **127.0.0.1 的相同端口**，按连接建立普通 socket，不在宿主机监听 65,535 个端口。
- 可选通告明确指定的 IPv4 / IPv6 子网，默认关闭；拒绝默认路由、出口路由及危险保留范围。
- 菜单栏显示状态，提供登录、断开、设置和退出。关闭窗口不会退出，退出应用会停止节点。
- 支持英文、简体中文和系统深浅色。通过 `NSBundle.preferredLocalizations` 跟随 macOS 系统语言及单应用语言设置，不依赖 `LANG`。

不创建宿主 TUN，不修改系统路由 / DNS，不接受 tailnet 路由，不提供出口节点或 SOCKS / HTTP 出站代理。Tailscale 自身仍需连接控制平面、对端及中继服务器。这是 TCP/UDP 用户态网关，不是全协议透明 VPN。

## 安装

支持 macOS 12 及以上、Apple silicon 和 Intel。

1. 打开 [GitHub Actions](https://github.com/ImSingee/TailBuddy/actions/workflows/ci.yml)，选择对应提交的成功运行，下载 `TailBuddy-macos-arm64`（Apple silicon）或 `TailBuddy-macos-amd64`（Intel）。下载 Actions 附件需要登录 GitHub。
2. 解压附件及其中的应用 zip，将 `TailBuddy.app` 放入「应用程序」。
3. 构建仅做 ad-hoc 签名，**没有 Developer ID 签名或 Apple 公证**。macOS 可能阻止未公证应用。请审阅源码与构建来源，并遵循你所在组织的软件安装要求；目前未提供 Developer ID 签名及公证版本。
4. 打开 TailBuddy，阅读访问范围，分别确认入站访问和凭据保存授权，点击「连接」，再点击「登录 Tailscale」。登录会创建独立设备。

打开应用本身不会连接、登录或暴露服务。「登录 Mac 时打开」只启动菜单栏应用，不自动连接节点。每次重新打开应用都需主动连接。连接后，受控进程会对普通故障做有限重试。Tailscale 引擎处理网络变化，系统唤醒后应用刷新状态。

## macOS 单应用语言

系统设置 → 通用 → 语言与地区 → 应用程序 → 添加 TailBuddy → 选择英文或简体中文 → 重新打开应用。

应用包声明 `en`、`zh-Hans` 并带有对应 `.lproj` 资源。未单独设置时跟随系统支持的首选语言。请使用打包后的 `.app` 验证，而非裸命令行程序。

## 连接前必读

**本机所有 TCP / UDP 服务都可能成为 tailnet 访问目标**，访问权限仍由 Tailscale 规则控制。这包括仅靠 localhost 隔离的开发服务器和无认证管理页面。服务端看到的来源往往是本机，而非原始 tailnet 客户端。请启用真正的服务认证，并收紧 tailnet grants / ACL。

- 默认目标固定为 IPv4 loopback `127.0.0.1`。仅监听 `::1`、局域网地址或 Unix socket 的服务不在此映射范围；兼容 IPv4 loopback 的通配监听服务可以访问。
- Tailscale 内部 / 保留服务优先，不能无条件保证其保留端口也转发本机。
- 不保证非 TCP/UDP 协议、mDNS / 组播 / 广播发现或任意 IP 协议透明工作。
- 子网「通告」「审批」「可达」是不同状态。已有 `autoApprovers` 时可立即生效；否则需管理员批准。原客户端的访问规则和路由接受设置也必须允许。应用不会替你改审批或 ACL。
- 界面分别显示请求通告的范围、Tailscale 报告的主路由和未验证的端到端可达性。冗余路由等场景中，未报告主路由并不等于未批准。须查看控制台并从授权远端测试。
- 关闭子网通告会把空路由列表实际写入运行中的引擎，不会打开 `accept-routes`。
- 其他代理 / VPN 软件仍可能截获 Tailscale 的 UDP 或 DERP 连接，兼容性取决于配置。

## 从源码构建

需要 Go **1.27.1+**、Python 3；Linux 跨编译还需要 `zip`。

```sh
git clone https://github.com/ImSingee/TailBuddy.git
cd TailBuddy
go test -race ./...
go vet ./...
./scripts/build-macos.sh arm64
# Intel: ./scripts/build-macos.sh amd64
```

脚本从官方 Go 模块生态获取固定版本依赖，构建 MyGo v0.4.8 原生界面和官方 `tailscaled` v1.104.1，打包完整 `.app`、保留依赖许可证，并输出 zip 与 SHA256 校验文件。macOS 上额外做 ad-hoc 签名及校验。Linux 跨编译成功不等于已经在 Mac 上运行验证。

## 隐私与进程管理

状态目录为 `~/Library/Application Support/TailBuddy`，目录权限 `0700`，配置 / 状态文件 `0600`。拒绝符号链接、共享权限、非当前用户所有的状态及不安全引擎文件。管理接口仅使用私有目录下的 Unix socket，不开放 TCP 管理端口。同一用户权限下的恶意软件仍可能访问这些数据，这不是针对同用户恶意代码的安全边界。

不继承代理凭据或任意 Tailscale 环境开关，不提供 auth-key 输入，不保存引擎标准输出或错误输出，关闭 Tailscale 日志上传，并从诊断中隐藏登录 URL。正常连接所需的身份和连接元数据仍会发送给 Tailscale。

父进程生命周期管道和排他锁防止普通应用崩溃留下孤立引擎。断开 / 退出停止入站访问，但保留凭据。启动时先以禁用且空路由的固定配置加载，再应用当前确认的设置，避免旧通告短暂恢复。上游配置格式为固定版本的 `alpha0`，升级依赖需要重新核验。

如需彻底移除：断开并退出，在 Tailscale 管理控制台删除该设备，再自行删除应用的状态目录。删除状态会丢失节点身份，重新连接将创建新身份。

## 验证与许可证

CI 包含单元 / race 测试、静态检查、双架构构建、macOS 平台测试、应用包及签名校验。测试只使用 mock LocalAPI 和假引擎，不会实际登录或暴露服务。交互式登录、实际端口、子网、系统语言、休眠及代理兼容性仍需按[验收清单](docs/ACCEPTANCE.md)在授权的 Mac 和 tailnet 上验证。

TailBuddy 原创代码为 MIT，MyGo 为 MIT，Tailscale 开源引擎为 BSD-3-Clause，其余依赖保留自身许可证。打包时附带完整通知和依赖清单。TailBuddy 与 Tailscale Inc. 无隶属关系。
