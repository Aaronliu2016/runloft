# RunLoft

**把本地 Web 项目作为桌面应用启动。**

RunLoft 计划提供一个桌面入口：从 Git 仓库获取 Web 项目，按项目声明的命令在本机启动服务，再在桌面窗口中显示它。目标项目继续使用自己的框架和运行时；RunLoft 管理仓库、进程生命周期和窗口。

> **项目状态：** MVP 开发与跨平台验证阶段。当前仓库包含 Go/MyGo 桌面外壳和项目生命周期实现；Windows 实机运行、已生成安装器的安装/升级/卸载验收，以及两平台完整目标项目 GUI 验收仍待完成。详见[技术验证记录](docs/VALIDATION.md)。

## 预期使用流程

1. 添加 Git 仓库并选择本地工作区。
2. 克隆仓库，或为已有的干净工作区获取更新。
3. 为了让授权对应可审查的源码，只能在 Git 工作区干净时运行；先提交本地改动。
4. 查看项目的 RunLoft 配置并授权其中的命令。
5. 按需执行项目声明的准备步骤（如安装依赖、构建），然后启动项目。
6. 等待本地 HTTP 服务就绪，并在桌面窗口中打开。
7. 停止项目或退出 RunLoft 时，RunLoft 会尝试清理其启动的进程及子进程；每个后代进程的最终退出状态不单独确认。

RunLoft 面向已经能在开发者电脑上运行的本地 Web 应用。它不会重写项目界面，也不会悄悄替换项目现有的构建和运行工具链。

## 项目文档

- [文档索引](docs/README.md)
- [项目方案与范围](docs/PROJECT_PLAN.md)
- [架构与生命周期](docs/ARCHITECTURE.md)
- [仓库配置草案](docs/PROJECT_CONFIG.md)
- [推进计划与交付门槛](docs/ROADMAP.md)
- [安全模型](docs/SECURITY.md)

## 当前能力

- 使用系统 WebView 展示 loopback Web 应用。
- 添加本地 Git 工作区或克隆仓库；查看 Git 状态，并在工作区干净且用户确认后应用快进更新。
- 严格读取 `.runloft.yaml`，审查并授权命令；prepare 与 start 都由用户分别主动触发。
- 只在 Git 工作区干净时执行 prepare/start，并在执行前再次核对授权对应的提交和配置。
- 诊断配置声明的本机运行时，按序执行准备步骤，启动服务并等待 HTTP 就绪。
- 显示经过秘密遮盖的运行日志，限制 WebView 导航，并尝试停止 RunLoft 创建的进程及其子进程。

当前一次只运行一个项目。每次打开项目 WebView 前会清除 MyGo 应用浏览数据，所以项目 Cookie 和本地存储不会跨次保留。

## 开发

需要 Go 1.27.1。MyGo CLI 已由 Go module 固定，macOS 上可在仓库根目录运行：

```sh
make dev
```

常用验证与构建命令：

```sh
make test
make vet
make build
make build-windows-x64
```

`make build` 生成当前开发机平台的应用包；`make build-windows-x64` 从 macOS 交叉构建 Windows x64 应用。若构建机未安装 NSIS，MyGo 会跳过 Windows 安装器；交叉构建不验证 Windows 实机运行。

添加项目后，先选择“准备依赖”或“启动项目”并审查命令授权。RunLoft 不会在克隆、启动应用或打开项目列表时自动执行仓库命令。

示例项目配置见 [`docs/samples/deepseek-harness.runloft.yaml`](docs/samples/deepseek-harness.runloft.yaml)。该样本仓库是未经安全审计的实验性开发预览；使用前请阅读其安全说明，并在隔离环境中运行。更多实现状态与平台限制见[推进计划](docs/ROADMAP.md)和[验证记录](docs/VALIDATION.md)。

## 问题与反馈

请在 [GitHub Issues](https://github.com/Aaronliu2016/runloft/issues) 提交错误报告或功能建议。报告问题时可附上 RunLoft 版本、操作系统和已脱敏的相关日志；不要提交令牌、凭据或敏感环境变量。

## 许可证

采用 MIT，版权行标注为 Aaronliu（2026）；详见 [LICENSE](LICENSE)。
