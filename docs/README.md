# English

## TimeVault documentation index

Look a module up here before changing or debugging it. Every document is bilingual: the complete English text comes first, the complete Simplified Chinese text second.

| Document | Describes |
|---|---|
| [plan.md](./plan.md) | The original implementation plan (P0–P4). It is the target; [mvp-status.md](./mvp-status.md) says what exists. |
| [PROJECT_CANVAS.md](./PROJECT_CANVAS.md), [TimeVault_Project_Canvas.html](./TimeVault_Project_Canvas.html) | Product charter and the offline UI concept. |
| [mvp-status.md](./mvp-status.md) | What the MVP implements, what it deliberately does not, and how each plan.md acceptance scenario is covered. Changes since 0.1.1 are marked there and listed in [changelog.md](./changelog.md). |
| [changelog.md](./changelog.md) | What changed in each version, and what to do when upgrading. |
| [architecture.md](./architecture.md) | The implemented architecture: components, data model, recovery-point lifecycle, repository format, restore flow, sign-in and single sign-on, scheduler, version limit, folder lists and display folders. |
| [api.md](./api.md) | The HTTP/JSON API and the page routes, with who may call what. |
| [runbook.md](./runbook.md) | Development setup, where settings live, configuration reference, running, operating, troubleshooting. |
| [security.md](./security.md) | Security controls that exist, and the gaps that remain. |
| [adr/](./adr/) | Architecture decision records (0005: open browsing and the Redis user directory; 0006: where settings live and automatic backups; 0007: display folders and hiding the server's real folders; 0008: folder lists cut on the server; 0009: single sign-on and trusted proxies; 0010: the version limit and garbage collection; 0011: the staggered scheduler and the change probe; 0012: serving under a path prefix such as `/tv` behind a reverse proxy; 0013: what an account may do is decided per monitored folder). |
| Module notes | The Everything search integration (and folder sizes) is documented in [runbook.md](./runbook.md#everything-search), single sign-on in [runbook.md](./runbook.md#single-sign-on), the scheduler's pacing and the Everything probe in [runbook.md](./runbook.md#scheduler-settings), and the permissions of a monitored folder in [runbook.md](./runbook.md#folder-permissions). |

---

# 中文

## TimeVault 文档索引

修改或排查某个模块之前，请先在这里查找。所有文档都是双语的：先给出完整的英文，再给出完整的简体中文。

| 文档 | 内容 |
|---|---|
| [plan.md](./plan.md) | 最初的实施计划（P0–P4），是目标；[mvp-status.md](./mvp-status.md) 说明现在实际有什么。 |
| [PROJECT_CANVAS.md](./PROJECT_CANVAS.md)、[TimeVault_Project_Canvas.html](./TimeVault_Project_Canvas.html) | 产品画布与离线界面概念稿。 |
| [mvp-status.md](./mvp-status.md) | MVP 实现了什么、有意不做什么，以及 plan.md 的每个验收场景如何被覆盖。0.1.1 之后的变化在其中有标注，并列在 [changelog.md](./changelog.md) 中。 |
| [changelog.md](./changelog.md) | 每个版本改了什么，以及升级时要做什么。 |
| [architecture.md](./architecture.md) | 已实现的架构：组件、数据模型、恢复点生命周期、仓库格式、恢复流程、登录与单点登录、调度器、版本数上限、文件夹列表和显示文件夹。 |
| [api.md](./api.md) | HTTP/JSON API 与页面路由，以及谁可以调用什么。 |
| [runbook.md](./runbook.md) | 开发环境搭建、设置放在哪里、配置参考、运行、运维、故障排查。 |
| [security.md](./security.md) | 已有的安全控制以及仍然存在的缺口。 |
| [adr/](./adr/) | 架构决策记录（0005：开放浏览与 Redis 用户目录；0006：设置放在哪里与自动备份；0007：显示文件夹与隐藏服务器真实文件夹；0008：在服务器上切分文件夹列表；0009：单点登录与受信任的代理；0010：版本数上限与垃圾回收；0011：错开时间的调度器与变化探测；0012：放在反向代理之后的 `/tv` 等路径前缀下提供服务；0013：账号能做什么按监控文件夹分别决定）。 |
| 模块说明 | Everything 搜索集成（以及文件夹大小）见 [runbook.md](./runbook.md#everything-搜索)，单点登录见 [runbook.md](./runbook.md#单点登录)，调度器的间隔控制与 Everything 探测见 [runbook.md](./runbook.md#调度器设置)，监控文件夹的权限见 [runbook.md](./runbook.md#文件夹权限)。 |
