# 项目运行配置

## 1. 目的与状态

项目运行配置描述 RunLoft 应如何准备并运行该项目。它由 RunLoft 外壳导入或编辑，并保存在 RunLoft 应用数据中，不放入目标项目源码目录。schema v1 已随 MVP 实现，但仍属**实验性协议**，尚未承诺长期兼容；字段与兼容规则仍可能随 MVP 迭代。

配置属于可执行边界：`prepare` 和 `start` 命令可以运行仓库代码。首次执行以及提交或配置变化后必须重新审查和授权。配置保存在应用数据里，目标仓库无需添加 RunLoft 专用文件；prepare/start 与 Git 更新仍要求目标 Git 工作区干净。

## 2. 草案示例

```yaml
schemaVersion: 1

project:
  name: Example Web app
  description: A local development server

workingDirectory: .

commands:
  prepare:
    steps:
      - argv:
          - pnpm
          - install
          - --frozen-lockfile
      - argv:
          - pnpm
          - run
          - build
  start:
    argv:
      - pnpm
      - dev
      - --host
      - 127.0.0.1
      - --port
      - "${RUNLOFT_PORT}"

runtime:
  checks:
    - name: Node.js
      command: node
      version: ">=24.9.0"

server:
  host: 127.0.0.1
  port: 43120
  healthPath: /
  readyTimeoutSeconds: 90
```

具体项目的包管理器、启动命令、端口参数和健康路径应以该仓库实际支持的方式填写。RunLoft 不应根据项目名称猜测命令。

## 3. 字段草案

| 字段 | 必需 | 含义 |
| --- | --- | --- |
| `schemaVersion` | 是 | 配置协议主版本 |
| `project.name` | 是 | RunLoft 中显示的项目名称 |
| `project.description` | 否 | 简短说明 |
| `workingDirectory` | 否 | 命令的相对工作目录，默认为仓库根目录；不得逃逸工作区 |
| `runtime.checks` | 否 | 本机运行时的内置版本探针；只诊断，不自动安装或升级 |
| `commands.prepare.steps` | 否 | 用户主动触发、按顺序执行的准备步骤，例如安装依赖和生成构建产物 |
| `commands.start.argv` | 是 | 启动本地服务的可执行文件及参数 |
| `server.host` | 否 | loopback 监听地址，默认 `127.0.0.1` |
| `server.port` | 是 | 项目服务监听端口；MVP 暂定固定端口 |
| `server.healthPath` | 是 | 就绪检查路径 |
| `server.readyTimeoutSeconds` | 否 | 就绪等待上限，默认 60 秒，最大 600 秒 |
| `server.entryURLRegex` | 否 | 从启动日志提取入口 URL 的正则表达式，必须且只能有一个捕获组；捕获 URL 必须与配置的 HTTP loopback host/port 相同 |

`runtime.checks` 目前只允许这些命令及固定参数，防止未授权配置借“版本诊断”执行任意代码：`node --version`、`pnpm --version`、`npm --version`、`go version`、`python --version`、`python3 --version`、`git --version`。版本约束使用 semver，例如 Node.js `>=24.9.0`、pnpm `=11.28.5`。

## 4. 命令与变量约束

- 每个步骤的 `argv` 都是参数数组，RunLoft 直接启动指定可执行文件，不把字符串交给 shell 解析。
- `prepare.steps` 按声明顺序执行；任一步失败即停止后续步骤，不自动重试。执行整组步骤前向用户展示并授权。
- 仅支持文档化的占位符，例如 `${RUNLOFT_PORT}`；不执行 `$()`、反引号、管道或重定向展开。
- `workingDirectory` 必须在克隆工作区内部，解析符号链接后仍需检查路径边界。
- 子进程继承启动 RunLoft 时的完整进程环境；MVP 不提供配置任意环境变量的字段。命令审查会列出名称匹配敏感字段规则的环境变量，但隐藏值；其余环境变量不在预览中逐项展示。
- 继承的进程环境不属于授权摘要，因此它在 RunLoft 外部变化不会触发重新授权。运行不可信仓库时，应避免让 RunLoft 继承含有不应交给目标程序的秘密。
- 配置不应包含凭据或要求 RunLoft 将秘密写入日志。
- 不从项目配置读取“下载并执行任意远程脚本”一类能力。

## 5. 校验与授权

导入或保存配置时，应验证版本、字段类型、命令数组非空、运行时探针白名单、端口范围、健康路径格式、入口 URL 来源及工作目录边界。错误信息需指出字段路径和修复建议。

授权摘要绑定仓库身份、当前 Git commit 和解析后的应用内项目配置，而不是只记住仓库路径。执行前要求 Git 工作区干净，并在显示确认框后再次读取提交、应用内配置和工作区状态；若提交、配置或工作区状态期间发生变化则取消执行。commit 或配置中的命令、步骤顺序、工作目录、端口或 URL 改变时，RunLoft 会要求用户重新审查并授权。RunLoft 将参数数组直接交给操作系统，不经 shell 解析。

此检查使用 Git 的普通工作区状态，不覆盖被 `.gitignore` 忽略的文件（例如依赖安装目录或构建产物）；这些文件不会纳入授权摘要。项目程序仍可读取本地工作区中的文件。

HTTP 健康检查接受 2xx、401 或 403 状态码，拒绝重定向；401/403 可表示服务已监听但页面需要入口 token。若配置 `entryURLRegex`，必须在健康检查成功前捕获并校验入口 URL。

日志会遮盖常见 token/口令字段、Bearer 值和由敏感环境变量名标识的环境值。这会降低误泄漏风险，但不能识别项目程序自行编码、拆分或变形后的所有秘密。

## 6. 待定事项

- 是否支持多个启动命令或启动后的任务。
- 如何表达 Windows、macOS、Linux 之间不同的命令和脚本。
- 是否支持配置层级/继承，以及是否为本地用户设置单独提供非版本化覆盖文件。
- 运行时版本要求由配置声明；MVP 不自动下载或安装运行时。
- 固定端口冲突后的重试或用户改配交互。
- 多项目 WebView 独立 profile；目前在每次打开项目 WebView 前清除 MyGo 应用的浏览数据，因此项目会话不会跨次保留。
