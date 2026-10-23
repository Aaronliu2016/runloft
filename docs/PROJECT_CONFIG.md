# 仓库配置草案

## 1. 目的与状态

仓库根目录的 `.runloft.yaml` 描述 RunLoft 应如何准备并运行该项目。此文件是 **草案**，不是已发布的稳定协议；首个实现前应完成 schema 校验器和兼容规则。

配置属于可执行边界：`prepare` 和 `start` 命令可以运行仓库代码。RunLoft 必须在首次执行以及配置变更后要求用户审查和授权。

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

server:
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
| `commands.prepare.steps` | 否 | 用户主动触发、按顺序执行的准备步骤，例如安装依赖和生成构建产物 |
| `commands.start.argv` | 是 | 启动本地服务的可执行文件及参数 |
| `server.port` | 是 | 项目服务监听端口；MVP 暂定固定端口 |
| `server.healthPath` | 是 | 就绪检查路径 |
| `server.readyTimeoutSeconds` | 否 | 就绪等待上限，默认值待实现确认 |

## 4. 命令与变量约束

- 每个步骤的 `argv` 都是参数数组，RunLoft 直接启动指定可执行文件，不把字符串交给 shell 解析。
- `prepare.steps` 按声明顺序执行；任一步失败即停止后续步骤，不自动重试。执行整组步骤前向用户展示并授权。
- 仅支持文档化的占位符，例如 `${RUNLOFT_PORT}`；不执行 `$()`、反引号、管道或重定向展开。
- `workingDirectory` 必须在克隆工作区内部，解析符号链接后仍需检查路径边界。
- 环境变量扩展以显式白名单设计；配置不应包含凭据或要求 RunLoft 将秘密写入日志。
- 不从仓库配置读取“下载并执行任意远程脚本”一类能力。

## 5. 校验与授权

加载配置时，应验证版本、字段类型、命令数组非空、端口范围、健康路径格式及工作目录边界。错误信息需指出字段路径和修复建议。

授权应绑定仓库身份、完整准备步骤序列、启动命令与相关字段的规范化摘要，而不是只记住仓库路径。命令、步骤顺序、工作目录、环境、端口或 URL 改变时必须重新展示差异并要求确认。

## 6. 待定事项

- 是否支持多个启动命令或启动后的任务。
- 如何表达 Windows、macOS、Linux 之间不同的命令和脚本。
- 是否支持配置层级/继承，以及是否为本地用户设置单独提供非版本化覆盖文件。
- 运行时版本要求由配置声明还是从生态清单（如 `package.json`）有限读取；MVP 不自动下载或安装运行时。
- 固定端口冲突后的重试或用户改配交互。
