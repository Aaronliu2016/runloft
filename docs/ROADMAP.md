# 推进计划

此计划按风险和依赖顺序推进。每阶段先产出可审查结果，再进入下一阶段；本文不预设具体日历日期。

## 执行状态（2026-10-24）

- **P0 部分完成，平台门槛未全部通过。** macOS Apple Silicon 上已验证 MyGo loopback WebView 的 HTTP、Cookie、JavaScript、WebSocket、SPA 路由和外链取消；DeepSeek Harness 固定 commit 在隔离容器中完成源码安装、构建和 tokenized WebView 页面验证。Windows 11 x64 的原生构建/运行、两平台完整目标项目闭环及主应用人工 GUI 验收仍未完成。证据见[技术验证记录](VALIDATION.md)。
- **P1–P3 首版实现已进入仓库。** 当前包含 MyGo 原生项目列表、Git 工作区管理、严格配置校验、命令审查授权、版本诊断、prepare/start 生命周期、loopback 健康检查、日志遮盖、WebView 导航限制和进程停止。对应的单元测试已在 macOS 开发机通过；真实 Windows 与手动 GUI 验收仍待执行。
- **P4 已准备打包入口，尚未通过发布验收。** macOS ARM64 `.app`/DMG 和 Windows x64 可执行文件、NSIS 安装器均已生成；GitHub draft release workflow 配置了同类构建，但 workflow 尚未运行。macOS Developer ID 签名/公证、Windows 安装器目标机测试、安装升级卸载及正式发布仍未完成。

阶段实现进度不代表该阶段验收门槛已通过；尤其 P0/P1 中两个首发平台的门槛仍是后续工作。

## P0：方案收敛与技术验证

**目标：** 尽早验证桌面 WebView、本地服务和子进程的组合可行。

**工作项：**

- 以 Windows 11 x64 和 macOS 14+（仅 Apple Silicon）作为首发支持范围；将 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 的 Web UI 模式作为真实验证样本。暂不承诺 Windows 10/ARM、Intel Mac 或更旧 macOS。
- 使用当前 MyGo 版本创建最小桌面窗口，加载本机 HTTP 页面。
- 验证 HTTP/WebSocket、Cookie、前端路由、外链拦截和本地服务未就绪提示。
- 从桌面应用启动和停止一个目标进程；验证关闭应用后的进程清理。
- 验证系统 Git 对公开仓库的 clone、status、fetch 和安全快进更新。
- 审阅 `.runloft.yaml` 草案、用户信任流程和固定端口方案；DeepSeek Harness 样本已固定到具体 commit 并审阅 `SAFETY.md`。仍须在两平台完成宿主机和 RunLoft 主应用闭环验证。
- 按仓库 README 的源码流程验证 `pnpm install --frozen-lockfile`、`pnpm run build`、`pnpm dsh web`；根 `package.json` 声明 Node.js `>=24.9.0`、pnpm `11.28.5`。容器内实测源码入口为 `dsh web: http://127.0.0.1:<port>/?token=…`，`/` 的未授权响应为 401；配置样例显式绑定 loopback 并提取 token 入口 URL。该结果仅覆盖隔离容器，不代表宿主机两平台均已兼容。

**交付物：** 技术验证记录、决策更新、Windows/macOS 首发支持矩阵和 MVP 验收用例。

**验收状态：未完全通过。** macOS Apple Silicon 的最小 WebView 和隔离样本验证通过；Windows 11 x64 原生运行、目标进程树清理、桌面主应用人工交互尚待验证。当前按 MyGo + Unix 进程组 / Windows Job Object 与 `taskkill /T` 继续实现；在 Windows 设备完成验收前不宣称 P0 关闭。

## P1：可启动的桌面骨架

**目标：** 形成 RunLoft 应用、项目状态模型和基础界面。

**工作项：**

- 建立 Go/MyGo 项目结构与首发平台开发构建方式。
- 实现项目列表、添加本地项目路径和基础状态提示。
- 实现本地持久化元数据，不改写目标项目文件。
- 完成最小 WebView 展示和错误/加载状态。

**交付物：** 能启动的开发版外壳及开发说明。

**验收状态：** Go/MyGo 开发版可构建，UI 源码已实现；本机 WebView 技术验证已记录。RunLoft 主 UI 的可交互验收和 Windows 11 x64 启动/错误状态测试仍待完成。

## P2：仓库与配置

**目标：** 管理项目克隆并安全读取配置。

**工作项：**

- 实现 Git clone、status、fetch 和经用户明确确认的仅快进更新。
- 实现 `.runloft.yaml` schema 解析、版本拒绝和字段诊断。
- 展示仓库来源、工作区状态、配置命令和配置摘要。
- 实现命令预览、首次授权以及配置变化后的重新授权。

**验收状态：** 已实现并有自动测试覆盖无效配置拒绝、配置授权摘要变化、脏工作区阻止更新以及过期更新计划拒绝。主 UI 交互仍需手动验收。

## P3：进程生命周期与服务呈现

**目标：** 将配置命令转为可观察、可停止的运行会话。

**工作项：**

- 实现可配置的有序 prepare 步骤与 start 命令执行、日志采集、退出码和运行时状态。
- 实现进程组清理、优雅退出、超时和崩溃提示。
- 实现本地 HTTP 健康检查、端口冲突处理和等待超时。
- 就绪后才加载 WebView，并限制导航和外链行为。

**验收状态：** 生命周期和 loopback 401 就绪测试通过；Unix 回归测试验证 leader 退出后会清理同组后台子进程且不影响另一进程组。固定版 DeepSeek Harness 的容器 WebView 路径已单独验证。RunLoft 主应用接管真实 DSH 项目的完整闭环仍未在宿主机上验收，Windows 亦未验证。

## P4：体验与分发

**目标：** 准备可交给试用者的版本。

**工作项：**

- 完善首次运行引导、依赖诊断、日志查看、重试和更新提示。
- 针对 Windows 11 x64 和 macOS 14+（Apple Silicon）构建安装包并通过 GitHub Releases 直接发布；macOS 公开版须使用 Developer ID 签名并公证，Windows Authenticode 签名视预算决定；验证安装、手动升级和卸载行为。macOS 公开发布前申请个人 Apple Developer 账号。
- 对项目配置和进程关闭边界做安全复核。
- 记录受支持的项目约束、系统依赖、已知问题和反馈入口。

**验收门槛：** 试用者能够按文档添加目标项目并定位启动失败原因；卸载不会删除其项目工作区。

## 建议的首批交付顺序

1. P0：MyGo loopback WebView + 子进程验证。
2. P0：已记录 DeepSeek Harness 固定 commit、配置草案和隔离容器 Web UI 结果；后续在 Windows/macOS 完成 RunLoft 主应用宿主机闭环。
3. P1：桌面骨架与手工添加本地项目。
4. P2：配置读取、授权与 Git clone/status。
5. P3：启动/就绪/停止闭环，再增加安全更新。
6. P4：试用、分发与基于反馈的范围调整。

## 已确定的决策

- 首发平台范围：Windows 11 x64、macOS 14+（仅 Apple Silicon）；不承诺 Windows 10/ARM、Intel Mac 或更旧 macOS。仍需在 P0 验证 MyGo 对该范围的实际支持。
- 验证样本：[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 的 Web UI 模式；固定 commit、隔离容器中的监听/健康检查入口及 Node/pnpm 源码准备流程已记录。RunLoft 主应用完整闭环，以及 Windows 11 x64 和 macOS 14+ Apple Silicon 的宿主机兼容性仍待 P0 实测。
- Git 更新：用户主动检查时才 fetch；展示目标提交后，只有用户明确确认才应用快进更新。工作区不干净或分支分叉时阻止更新。
- 配置准备步骤：使用 `commands.prepare.steps` 按序执行；失败即停止，必须由用户主动触发。
- 运行时依赖：MVP 只诊断本机工具及版本并提示用户，不自动安装或更新运行时；项目依赖仍通过经授权的准备步骤处理。
- 开源许可：RunLoft 采用 MIT；`LICENSE` 版权行为 Aaronliu（2026）。
- 分发渠道：首发安装包通过 GitHub Releases 直接发布；暂不提供应用内自动更新，用户手动下载新版本。
- 发布签名策略：开发测试版可不签名；macOS 公开版须 Developer ID 签名并公证；Windows Authenticode 签名视预算决定。若没有预算，Windows 首发版可不签名并提示 SmartScreen 警告。RunLoft 是个人项目，签名身份使用个人开发者身份；Apple Developer 账号申请推迟到 P4、公开发布前。

## 后续落实事项

- P0 已固定 DeepSeek Harness 验证 commit，并记录隔离容器的监听地址、健康检查和 token 入口；仍须在 Windows 11 x64 与 macOS 14+ Apple Silicon 完成主应用实机验收。
- 已从 macOS 成功交叉构建 Windows x64 可执行文件，并在一次性 Debian/NSIS 容器中生成安装器。后续仍须在 Windows 11 x64 上启动应用及执行安装、升级、卸载验收。
- 在 P4 macOS 公开发布前申请个人 Apple Developer 账号；Windows Authenticode 是否采购按预算执行，无预算时允许未签名首发。
