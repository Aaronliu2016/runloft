# 技术验证记录

最后更新：2026-10-24

本文记录本项目 P0 样本、桌面框架和当前代码验证结果。标注为“未验证”的平台或行为不应被理解为已支持。

## P0 样本与隔离方式

- 样本仓库：[`deepseek-ai/deepseek-harness`](https://github.com/deepseek-ai/deepseek-harness)，固定 commit `22ddee3792402c3e70ec5f86a63c3e3e895a2f60`。
- 审阅了该 commit 的 `SAFETY.md`。项目自述为未经安全审计的开发预览，可执行生成代码、插件和命令，并访问进程、网络、凭据及文件；因此本次源码安装、构建和运行均在一次性 Docker 容器中完成，没有挂载主机用户目录或凭据。
- 容器内按仓库源码方式执行 `pnpm install --frozen-lockfile`、`pnpm run build` 和 `pnpm dsh web`。构建门禁结果为 10 passed、0 failed、0 skipped。
- DSH 服务在容器内绑定地址上运行，并通过 `127.0.0.1:13080` 映射给宿主机。未携带启动 token 的根路径请求返回 HTTP 401；MyGo WebView 加载携带 token 的实际页面后显示标题 `DSH 本地构建`，页面 origin 为 `http://127.0.0.1:13080`。
- 从该 WebView 尝试打开 `https://example.com` 时触发导航拦截回调并取消导航。
- 另一个最小 MyGo 页面验证了 HTTP 200、Cookie、JavaScript、WebSocket 和 SPA 路由切换。该验证在 macOS Apple Silicon 上执行。

## 当前代码验证

截至 2026-10-24，本轮已针对当前提交重新运行 Go 测试、Go/MyGo vet、Windows x64 交叉编译，并重建两平台应用包。主应用 GUI、Windows 实机和安装器交互仍未验收。

| 项目 | 结果 | 证据与边界 |
| --- | --- | --- |
| Go 项目与 MyGo 依赖 | 已验证 | 使用 Go 1.27.1、MyGo v0.6.0；针对当前提交运行 `make test`、`make vet` 均通过。 |
| 严格配置校验与授权摘要 | 已验证 | 单元测试覆盖未知字段、额外 YAML 文档、目录逃逸、运行时探针白名单、入口 URL 来源及仓库/commit/配置变化导致的授权摘要变化。 |
| Git 安全快进逻辑 | 已验证 | 临时本地仓库测试覆盖干净快进、脏工作区阻止更新、分叉阻止更新和过期计划拒绝。 |
| 公开仓库 Git 适配器流程 | 已验证（临时克隆） | RunLoft 的 `gitops.Git` 对公开 RunLoft 仓库执行 clone 和干净状态检查；将临时克隆切换到前一提交后，`CheckUpdates` 成功 fetch 并生成 `ahead=0, behind=1` 的计划，`FastForward` 将本地分支安全更新到目标提交 `614e3b2`，工作区保持干净。该验证未推送或修改远端；脏工作区、分叉和过期计划由本地仓库测试覆盖。 |
| 进程与 HTTP 就绪流程 | 已验证 | 本机测试子进程覆盖启动、401 健康检查、令牌 URL 捕获、日志秘密遮盖、超长输出行持续排空、端口占用和 prepare 失败即停止。Unix 回归测试还验证了 leader 退出后会清理同一进程组的后台子进程，且不影响另一进程组；Windows Job Object 清理尚未在目标 Windows 设备实测。 |
| 目标 DSH Web UI | 已验证（隔离容器） | 固定 commit 的源码构建与 tokenized WebView 页面成功。尚未由 RunLoft 主应用在宿主机运行该未审计项目。 |
| macOS 14+ Apple Silicon 完整 GUI 验收 | 部分验证 | 当前开发机可运行 MyGo 与最小 WebView 验证；桌面处于锁屏状态，未完成对 RunLoft 主界面的人工交互验收。 |
| macOS Apple Silicon 应用包 | 已构建并检查 | 针对当前提交重新生成 ARM64 `.app` 与 DMG；`codesign --verify --deep --strict` 和 `hdiutil verify` 均通过，最低系统版本为 macOS 14.0。该包使用 ad-hoc 签名，未公证；没有完成可交互桌面中的 RunLoft 人工验收。 |
| Windows x64 构建与安装器 | 已构建并检查 | 针对当前提交运行 `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build ./...` 与 `make build-windows-x64` 均通过。MyGo 在一次性 Debian/NSIS 容器中生成 Windows x64 应用和安装器；`file` 将应用识别为 PE32+ x86-64，将安装器识别为 Nullsoft Installer。两者尚未在 Windows 上运行。 |
| CI 与 draft release workflow | 已准备，未执行 | CI 配置了测试、Go/MyGo vet 和 Windows 交叉编译；打 tag 后的 draft release workflow 配置了 macOS DMG 与 Windows 安装器构建。尚无 workflow run 结果或已上传的发布产物。 |
| Windows 11 x64 运行 | 未验证 | 当前开发机为 macOS；尚无 Windows 11 x64 实机运行环境。WebView2、启动和停止进程树、安装器及安装/升级/卸载行为仍待在目标设备验收。 |

## DeepSeek Harness 示例配置

[`samples/deepseek-harness.runloft.yaml`](samples/deepseek-harness.runloft.yaml) 是该固定样本的配置模板，按其源码说明声明 Node.js `>=24.9.0`、pnpm `11.28.5`，先执行 frozen install 和 build，再以 `127.0.0.1:3080` 启动 Web UI。该配置不会自动运行：用户必须在 RunLoft 中单独审查并授权 prepare 或 start 命令。

DSH 的 token 在启动日志 URL 中传递给 WebView。RunLoft 只在内存中使用该入口 URL；日志遮盖 URL 查询令牌和从敏感环境变量读取的值。根路径返回 401 被视为服务存活，因为 DSH 只有在入口 URL 携带启动 token 时才允许访问页面。

## 后续验证

1. 在 macOS 14+ Apple Silicon 上使用可交互桌面会话运行 RunLoft，完成添加、授权、准备、WebView、停止和重开项目的人工验收。
2. 在 Windows 11 x64 上运行交叉构建的 RunLoft，确认 WebView2、外链策略、loopback Cookie/WebSocket 和子进程树停止行为；运行已生成的 NSIS 安装器并验证安装、升级和卸载。
3. 在两平台分别使用 DeepSeek Harness 的固定 commit 完成完整闭环；除非继续放在隔离环境内，否则不要对该样本提供主机凭据或敏感目录访问。
4. 验证签名安装包、升级和卸载，再进入 P4 发布准备。
