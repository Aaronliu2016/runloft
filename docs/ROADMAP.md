# 推进计划

此计划按风险和依赖顺序推进。每阶段先产出可审查结果，再进入下一阶段；本文不预设具体日历日期。

## P0：方案收敛与技术验证

**目标：** 尽早验证桌面 WebView、本地服务和子进程的组合可行。

**工作项：**

- 以 Windows 11 x64 和 macOS 14+（仅 Apple Silicon）作为首发支持范围；将 [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 的 Web UI 模式作为真实验证样本。暂不承诺 Windows 10/ARM、Intel Mac 或更旧 macOS。
- 使用当前 MyGo 版本创建最小桌面窗口，加载本机 HTTP 页面。
- 验证 HTTP/WebSocket、Cookie、前端路由、外链拦截和本地服务未就绪提示。
- 从桌面应用启动和停止一个目标进程；验证关闭应用后的进程清理。
- 验证系统 Git 对公开仓库的 clone、status、fetch 和安全快进更新。
- 审阅 `.runloft.yaml` 草案、用户信任流程和固定端口方案；验证样本仓库须固定到具体 commit，并先审阅其 `SAFETY.md`。
- 按仓库 README 的源码流程验证 `pnpm install`、`pnpm run build`、`pnpm dsh web`；检查时根 `package.json` 声明 Node.js `>=24.9.0`、pnpm `11.28.5`。README 记载 npm 运行方式默认监听 `127.0.0.1:3080`；源码方式的实际监听地址、健康检查路径及两平台兼容性仍须实测。

**交付物：** 技术验证记录、决策更新、Windows/macOS 首发支持矩阵和 MVP 验收用例。

**进入下一阶段的门槛：** MyGo WebView 在 Windows 11 x64 和 macOS 14+（Apple Silicon）均能稳定加载 loopback 服务；两个平台的子进程均能被停止并清理；关键跨平台或安全障碍有明确方案。若任一平台不满足，应重新评估框架或缩小 MVP。

## P1：可启动的桌面骨架

**目标：** 形成 RunLoft 应用、项目状态模型和基础界面。

**工作项：**

- 建立 Go/MyGo 项目结构与首发平台开发构建方式。
- 实现项目列表、添加本地项目路径和基础状态提示。
- 实现本地持久化元数据，不改写目标项目文件。
- 完成最小 WebView 展示和错误/加载状态。

**交付物：** 能启动的开发版外壳及开发说明。

**验收门槛：** 应用在 Windows 11 x64 和 macOS 14+（Apple Silicon）启动，能打开测试 loopback 页面并显示网络错误状态。

## P2：仓库与配置

**目标：** 管理项目克隆并安全读取配置。

**工作项：**

- 实现 Git clone、status、fetch 和经用户明确确认的仅快进更新。
- 实现 `.runloft.yaml` schema 解析、版本拒绝和字段诊断。
- 展示仓库来源、工作区状态、配置命令和配置摘要。
- 实现命令预览、首次授权以及配置变化后的重新授权。

**验收门槛：** 无效配置不会进入执行流程；有本地变更时更新被阻止且原因清楚。

## P3：进程生命周期与服务呈现

**目标：** 将配置命令转为可观察、可停止的运行会话。

**工作项：**

- 实现可配置的有序 prepare 步骤与 start 命令执行、日志采集、退出码和运行时状态。
- 实现进程组清理、优雅退出、超时和崩溃提示。
- 实现本地 HTTP 健康检查、端口冲突处理和等待超时。
- 就绪后才加载 WebView，并限制导航和外链行为。

**验收门槛：** 一个真实项目从克隆开始可以完成安装、启动、就绪、显示、停止的闭环。

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
2. P0：在 Windows/macOS 验证 DeepSeek Harness Web UI 路径，固定样本 commit 并确认配置草案。
3. P1：桌面骨架与手工添加本地项目。
4. P2：配置读取、授权与 Git clone/status。
5. P3：启动/就绪/停止闭环，再增加安全更新。
6. P4：试用、分发与基于反馈的范围调整。

## 已确定的决策

- 首发平台范围：Windows 11 x64、macOS 14+（仅 Apple Silicon）；不承诺 Windows 10/ARM、Intel Mac 或更旧 macOS。仍需在 P0 验证 MyGo 对该范围的实际支持。
- 验证样本：[DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) 的 Web UI 模式；固定 commit、实际监听/健康检查地址、Node/pnpm 依赖准备方式及两平台兼容性仍待 P0 实测。
- Git 更新：用户主动检查时才 fetch；展示目标提交后，只有用户明确确认才应用快进更新。工作区不干净或分支分叉时阻止更新。
- 配置准备步骤：使用 `commands.prepare.steps` 按序执行；失败即停止，必须由用户主动触发。
- 运行时依赖：MVP 只诊断本机工具及版本并提示用户，不自动安装或更新运行时；项目依赖仍通过经授权的准备步骤处理。
- 开源许可：RunLoft 采用 MIT；`LICENSE` 版权行为 Aaronliu（2026）。
- 分发渠道：首发安装包通过 GitHub Releases 直接发布；暂不提供应用内自动更新，用户手动下载新版本。
- 发布签名策略：开发测试版可不签名；macOS 公开版须 Developer ID 签名并公证；Windows Authenticode 签名视预算决定。若没有预算，Windows 首发版可不签名并提示 SmartScreen 警告。RunLoft 是个人项目，签名身份使用个人开发者身份；Apple Developer 账号申请推迟到 P4、公开发布前。

## 后续落实事项

- P0 固定 DeepSeek Harness 验证 commit，并实测源码模式的监听地址、健康检查路径和 Windows/macOS 兼容性。
- 在 P4 macOS 公开发布前申请个人 Apple Developer 账号；Windows Authenticode 是否采购按预算执行，无预算时允许未签名首发。
