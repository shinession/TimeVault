# English

## TimeVault

**TimeVault — enterprise file backup, search and time travel** for Windows and NTFS. It keeps point-in-time recovery points of local folders, lets you browse the past state of any folder, search by name, and restore files into a safe folder below a restore root after a preview.

**Status: version 0.1.1 (vertical slice).** One path works end to end on a local NTFS folder: browse, back up, keep history, verify, restore. Anyone can browse; company accounts from a Redis user directory sign in to back up, restore or change settings. It is **not** a production backup system: it reads the live disk (no VSS, no NTFS USN journal), has two roles, no retention and no TLS of its own. [docs/mvp-status.md](docs/mvp-status.md) lists exactly what is done, partial and missing against [docs/plan.md](docs/plan.md), and [docs/changelog.md](docs/changelog.md) lists what changed in each version.

### What works today

- A single Go service, `timevault-server`, with a bilingual (English / 中文) web UI in the OpenList style, light and dark themes, and a JSON API. It listens on `127.0.0.1:8660` by default.
- **Explorer**: the live folder tree of a monitored folder, marked as protected, changed or new, plus name search through a local Everything instance (optional).
- **Time Machine**: choose any committed recovery point, browse the folder tree as it was, see deleted files, and list every version of a file.
- **Backups**: full and incremental backups into a content-addressed repository, started by hand or on an automatic schedule per folder, with progress, cancel, skipped-file reporting and integrity verification. PostgreSQL stores the catalog.
- **Restore**: preview first, then a confirmed restore of files or folders into a folder below a configured restore root, with SHA-256 verification and skip / rename / overwrite policies.
- **Open browsing and sign-in**: anyone can browse; every change needs a company account from the Redis user directory (the `app_user_*` hashes of another application), with the roles `user` and `admin`. An optional local administrator is also possible.
- **Settings**: administrators add and edit the monitored folders (folder, exclusions, on or off, schedule) on a page. Deployment settings (database, Redis, backup and restore folders) live in the environment or the `.env` file.
- CSRF protection, sign-in limits, an append-only audit log and a status page.

### Quick start (development, Windows PowerShell)

Needs Go 1.25+ and a PostgreSQL server (see [docs/runbook.md](docs/runbook.md)). If Go is not on your `PATH`, first put the line `TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>` into `.env` in the repository folder. **`.env` is the only settings file**: the scripts create and fill it, and the service reads it. The scripts keep their other data under the git-ignored `data\` folder and never touch an existing PostgreSQL service.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-db.ps1 start    # optional: private PostgreSQL on port 55432 (skip it to use your own server)
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-run.ps1         # build and run the server
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-demo.ps1        # in a second window: demo data
```

To use your own PostgreSQL server, create the database and the role, then put the URL into `.env` (`TIMEVAULT_DATABASE_URL`, and `TIMEVAULT_TEST_DATABASE_URL` for the tests); the scripts then start no private cluster. `dev-run.ps1` creates `.env` with comments and fills in what the service needs. For company sign-in add `TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0` there and check it with `data\dev\bin\timevault-server.exe check-redis-users`. A `TIMEVAULT_*` variable in your terminal would override `.env`: the scripts remove such variables, and the service names them in its start-up log.

Open `http://127.0.0.1:8660/`. Anyone can browse; to back up, restore or change settings, sign in as `admin` (the generated password is in `data\dev\admin-password.txt`) or with a company account. An administrator adds the monitored folders on the **Settings** page. Run the tests with `. .\scripts\dev-env.ps1` followed by `go test ./... -count=1`.

### Documentation

| Document | Content |
|---|---|
| [docs/README.md](docs/README.md) | Index of all documentation |
| [docs/plan.md](docs/plan.md) | The original P0–P4 plan |
| [docs/mvp-status.md](docs/mvp-status.md) | What is done and what is not |
| [docs/changelog.md](docs/changelog.md) | What changed in each version, and upgrade notes |
| [docs/architecture.md](docs/architecture.md) | Implemented architecture |
| [docs/api.md](docs/api.md) | HTTP/JSON API and pages |
| [docs/runbook.md](docs/runbook.md) | Setup, configuration, operation, troubleshooting |
| [docs/security.md](docs/security.md) | Security controls and gaps |
| [docs/adr/](docs/adr/) | Architecture decision records |
| [docs/PROJECT_CANVAS.md](docs/PROJECT_CANVAS.md), [docs/TimeVault_Project_Canvas.html](docs/TimeVault_Project_Canvas.html) | Product charter and the offline UI concept (mock data) |

### Layout

| Path | Content |
|---|---|
| `cmd/timevault-server/` | Program entry point |
| `internal/` | Catalog, backup engine, repository, restore, jobs, auth, Redis user directory, audit, search adapter, scheduler, HTTP server |
| `migrations/` | Database schema (embedded in the executable) |
| `web/` | Templates and static assets (embedded in the executable) |
| `scripts/` | Development helper scripts |
| `configs/` | Settings template (`timevault.example.env`) without secrets |
| `docs/` | Documentation |
| `data/` | Local development data (git-ignored) |

---

# 中文

## TimeVault

**TimeVault —— 企业文件备份、搜索与时间回溯**，面向 Windows 和 NTFS。它为本地文件夹保存时间点恢复点，可以浏览任意文件夹过去的状态、按名称搜索，并在预览之后把文件恢复到恢复根目录下的安全文件夹中。

**状态：0.1.1 版（垂直切片）。** 在本地 NTFS 文件夹上，浏览、备份、保留历史、校验、恢复这一条路径已经完整打通。任何人都可以浏览；来自 Redis 用户目录的公司账号登录后才能备份、恢复或修改设置。它**不是**生产级备份系统：它读取在线磁盘（没有 VSS，也没有 NTFS USN journal），只有两种角色，没有保留策略，自身也没有 TLS。[docs/mvp-status.md](docs/mvp-status.md) 逐项列出了相对 [docs/plan.md](docs/plan.md) 已完成、部分完成和缺失的内容，[docs/changelog.md](docs/changelog.md) 列出了每个版本的改动。

### 目前可用的功能

- 一个 Go 服务 `timevault-server`，带有 OpenList 风格的双语（English / 中文）网页界面、浅色和深色主题，以及 JSON API。默认监听 `127.0.0.1:8660`。
- **文件浏览**：监控文件夹的实时文件夹树，标注为已保护、已变更或新增；另可通过本机的 Everything 实例按名称搜索（可选）。
- **时间机器**：选择任意已提交的恢复点，按当时的样子浏览文件夹树，查看已删除的文件，并列出某个文件的所有版本。
- **备份**：全量和增量备份到内容寻址的仓库，可以手动启动，也可以为每个文件夹设置自动计划，带进度、取消、跳过文件的报告和完整性校验。目录库保存在 PostgreSQL 中。
- **恢复**：先预览，再经确认把文件或文件夹恢复到所配置恢复根目录下的文件夹，带 SHA-256 校验以及跳过 / 改名 / 覆盖策略。
- **开放浏览与登录**：任何人都可以浏览；所有改动都需要 Redis 用户目录（另一个应用的 `app_user_*` 哈希）中的公司账号，角色为 `user` 和 `admin`。也可以另设一个可选的本地管理员。
- **设置**：管理员在页面上添加和编辑监控文件夹（文件夹、排除规则、开关、计划）。部署相关的设置（数据库、Redis、备份和恢复文件夹）放在环境变量或 `.env` 文件中。
- CSRF 防护、登录限制、只追加的审计日志和状态页。

### 快速开始（开发环境，Windows PowerShell）

需要 Go 1.25+ 和一个 PostgreSQL 服务器（见 [docs/runbook.md](docs/runbook.md)）。如果 Go 不在 `PATH` 中，请先把 `TIMEVAULT_GO_ROOT=<包含 bin\go.exe 的文件夹>` 这一行写入仓库文件夹中的 `.env`。**`.env` 是唯一的设置文件**：脚本创建并填写它，服务读取它。脚本把其他数据放在被 git 忽略的 `data\` 文件夹下，不会触碰已有的 PostgreSQL 服务。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-db.ps1 start    # optional: private PostgreSQL on port 55432 (skip it to use your own server)
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-run.ps1         # build and run the server
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-demo.ps1        # in a second window: demo data
```

要使用你自己的 PostgreSQL 服务器，先创建好数据库和角色，再把 URL 写入 `.env`（`TIMEVAULT_DATABASE_URL`，测试用的是 `TIMEVAULT_TEST_DATABASE_URL`）；这样脚本就不会启动私有集群。`dev-run.ps1` 会创建带注释的 `.env`，并填入服务需要的内容。要启用公司账号登录，请在其中加入 `TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0`，并用 `data\dev\bin\timevault-server.exe check-redis-users` 检查。终端里的 `TIMEVAULT_*` 变量会覆盖 `.env`：脚本会移除这类变量，服务也会在启动日志里点名它们。

打开 `http://127.0.0.1:8660/`。任何人都可以浏览；要备份、恢复或修改设置，请以 `admin`（生成的密码在 `data\dev\admin-password.txt` 中）或公司账号登录。管理员在**设置**页添加监控文件夹。运行测试：先执行 `. .\scripts\dev-env.ps1`，再执行 `go test ./... -count=1`。

### 文档

| 文档 | 内容 |
|---|---|
| [docs/README.md](docs/README.md) | 全部文档的索引 |
| [docs/plan.md](docs/plan.md) | 最初的 P0–P4 计划 |
| [docs/mvp-status.md](docs/mvp-status.md) | 做了什么、没做什么 |
| [docs/changelog.md](docs/changelog.md) | 每个版本的改动与升级说明 |
| [docs/architecture.md](docs/architecture.md) | 已实现的架构 |
| [docs/api.md](docs/api.md) | HTTP/JSON API 与页面 |
| [docs/runbook.md](docs/runbook.md) | 安装、配置、运维、故障排查 |
| [docs/security.md](docs/security.md) | 安全控制与缺口 |
| [docs/adr/](docs/adr/) | 架构决策记录 |
| [docs/PROJECT_CANVAS.md](docs/PROJECT_CANVAS.md)、[docs/TimeVault_Project_Canvas.html](docs/TimeVault_Project_Canvas.html) | 产品画布与离线界面概念稿（演示数据） |

### 目录结构

| 路径 | 内容 |
|---|---|
| `cmd/timevault-server/` | 程序入口 |
| `internal/` | 目录库、备份引擎、仓库、恢复、任务、认证、Redis 用户目录、审计、搜索适配器、调度器、HTTP 服务 |
| `migrations/` | 数据库结构（嵌入可执行文件） |
| `web/` | 模板和静态资源（嵌入可执行文件） |
| `scripts/` | 开发辅助脚本 |
| `configs/` | 不含机密信息的设置模板（`timevault.example.env`） |
| `docs/` | 文档 |
| `data/` | 本地开发数据（被 git 忽略） |
