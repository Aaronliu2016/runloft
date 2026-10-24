# RunLoft 文档

RunLoft 的文档以中文维护。项目当前处于 MVP 开发与跨平台验证阶段。方案文档、实现状态和已验证证据分别标注；首发平台验收未完成的部分不会视为已支持。

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [项目方案](PROJECT_PLAN.md) | 产品目标、用户场景、范围和成功标准 |
| [架构与生命周期](ARCHITECTURE.md) | 桌面壳、仓库、子进程、本地服务和 WebView 的协作方式 |
| [项目运行配置](PROJECT_CONFIG.md) | RunLoft 管理的项目配置格式和约束 |
| [推进计划](ROADMAP.md) | 阶段、交付物、验收门槛和待决事项 |
| [安全模型](SECURITY.md) | 仓库信任、命令执行、Git 更新和本地服务的安全要求 |
| [技术验证记录](VALIDATION.md) | P0 样本、代码验证证据和未完成的平台验收 |
| [DeepSeek Harness 示例配置](samples/deepseek-harness.runloft.yaml) | 可导入 RunLoft 的固定 P0 样本配置模板 |

## 当前已确认方向

- 项目暂定名为 **RunLoft**。
- 产品形态是 Go 桌面应用外壳；目标 Web 项目仍由其仓库提供的命令启动。
- 期望流程包括 Git 仓库获取、启动本地 Web 服务，以及在桌面窗口中显示该服务。
- MyGo 是当前桌面框架方向，需先通过真实项目的 WebView 和进程管理验证。

## 文档状态

项目配置 schema v1 当前随 MVP 实现维护，仍属实验性协议，尚未承诺长期兼容。配置由 RunLoft 外壳保存在应用数据中，不要求目标源码仓库包含 `.runloft.yaml`。技术验证未通过或未执行的平台见[验证记录](VALIDATION.md)；实际阶段门槛见[推进计划](ROADMAP.md)。
