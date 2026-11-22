# English

## TimeVault documentation index

Look a module up here before changing or debugging it. Every document is bilingual: the complete English text comes first, the complete Simplified Chinese text second.

| Document | Describes |
|---|---|
| [plan.md](./plan.md) | The original implementation plan (P0–P4). It is the target; [mvp-status.md](./mvp-status.md) says what exists. |
| [PROJECT_CANVAS.md](./PROJECT_CANVAS.md), [TimeVault_Project_Canvas.html](./TimeVault_Project_Canvas.html) | Product charter and the offline UI concept. |
| [mvp-status.md](./mvp-status.md) | What the MVP implements, what it deliberately does not, and how each plan.md acceptance scenario is covered. Changes since 0.1.1 are marked there and listed in [changelog.md](./changelog.md). |
| [changelog.md](./changelog.md) | What changed in each version, and what to do when upgrading. |
| [architecture.md](./architecture.md) | The implemented architecture: components, data model, recovery-point lifecycle, repository format, restore flow, sign-in and single sign-on, the Site settings and the start-up in two steps, the NTFS index, scheduler, version limit, folder lists and display folders, the web log and the page guide. |
| [api.md](./api.md) | The HTTP/JSON API and the page routes, with who may call what. |
| [runbook.md](./runbook.md) | Development setup, where settings live (`.env` and the Site settings), the Site settings and the configuration reference, running, the search index, operating, troubleshooting. |
| [security.md](./security.md) | Security controls that exist, and the gaps that remain. |
| [adr/](./adr/) | Architecture decision records (0005: open browsing and the Redis user directory; 0006: where settings live and automatic backups; 0007: display folders and hiding the server's real folders; 0008: folder lists cut on the server; 0009: single sign-on and trusted proxies; 0010: the version limit and garbage collection; 0011: the staggered scheduler and the change probe; 0012: serving under a path prefix such as `/tv` behind a reverse proxy; 0013: what an account may do is decided per monitored folder; 0014: the service updates itself from a release folder; 0015: the browser reads the pictures of the accounts from the host that keeps them; 0016: the web log is appended to the Redis list that the company applications share; 0017: the operating settings are Site settings in the database, edited by the local administrator only; 0018: a native NTFS index replaces Everything). |
| Module notes | The Site settings (the page, the API, the command line, the restart) are documented in [runbook.md](./runbook.md#site-settings), the NTFS index (search, folder sizes, the change probe) in [runbook.md](./runbook.md#search-index), single sign-on in [runbook.md](./runbook.md#single-sign-on), the scheduler's pacing and the change probe in [runbook.md](./runbook.md#scheduler-settings), the permissions of a monitored folder in [runbook.md](./runbook.md#folder-permissions), releasing and the automatic update in [runbook.md](./runbook.md#releasing-and-automatic-updates), the pictures of the accounts in [runbook.md](./runbook.md#pictures-of-the-accounts), and the web log in [runbook.md](./runbook.md#web-log). The user guide is not a file of `docs\`: the application serves it itself at `/help` (template `web\templates\help.html`; the **Guide** button of every page starts the tour of that page, `web\assets\js\tour.js`, with its texts in `internal\server\i18n_tour.go`); how to use it is described in the **Operating** section of [runbook.md](./runbook.md#operating). |

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
| [architecture.md](./architecture.md) | 已实现的架构：组件、数据模型、恢复点生命周期、仓库格式、恢复流程、登录与单点登录、站点设置与分两步的启动、NTFS 索引、调度器、版本数上限、文件夹列表和显示文件夹，以及 Web 日志和页面导览。 |
| [api.md](./api.md) | HTTP/JSON API 与页面路由，以及谁可以调用什么。 |
| [runbook.md](./runbook.md) | 开发环境搭建、设置放在哪里（`.env` 和站点设置）、站点设置和配置参考、运行、搜索索引、运维、故障排查。 |
| [security.md](./security.md) | 已有的安全控制以及仍然存在的缺口。 |
| [adr/](./adr/) | 架构决策记录（0005：开放浏览与 Redis 用户目录；0006：设置放在哪里与自动备份；0007：显示文件夹与隐藏服务器真实文件夹；0008：在服务器上切分文件夹列表；0009：单点登录与受信任的代理；0010：版本数上限与垃圾回收；0011：错开时间的调度器与变化探测；0012：放在反向代理之后的 `/tv` 等路径前缀下提供服务；0013：账号能做什么按监控文件夹分别决定；0014：服务从发布文件夹更新自己；0015：由浏览器从存放头像的主机读取账号头像；0016：把 Web 日志追加到公司各应用共用的 Redis 列表；0017：运行设置是保存在数据库中的站点设置，只有本地管理员能编辑；0018：原生 NTFS 索引取代 Everything）。 |
| 模块说明 | 站点设置（页面、API、命令行、重启）见 [runbook.md](./runbook.md#站点设置)，NTFS 索引（搜索、文件夹大小、变更探测）见 [runbook.md](./runbook.md#搜索索引)，单点登录见 [runbook.md](./runbook.md#单点登录)，调度器的间隔控制与变更探测见 [runbook.md](./runbook.md#调度器设置)，监控文件夹的权限见 [runbook.md](./runbook.md#文件夹权限)，发布、自动更新见 [runbook.md](./runbook.md#发布与自动更新)，账号头像见 [runbook.md](./runbook.md#账号头像)，Web 日志见 [runbook.md](./runbook.md#web-日志)。用户手册不是 `docs\` 中的文件：它由应用自己在 `/help` 提供（模板 `web\templates\help.html`；每个页面的**页面导览**按钮开始该页面的导览，脚本是 `web\assets\js\tour.js`，文字在 `internal\server\i18n_tour.go` 中）；怎样使用它，见 [runbook.md](./runbook.md#运维) 的**运维**一节。 |
