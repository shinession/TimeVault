# English

## TimeVault

**TimeVault — enterprise file backup, search and time travel** for Windows and NTFS. It keeps point-in-time recovery points of local folders, lets you browse the past state of any folder, search by name, and restore files into a safe folder below a restore root after a preview.

**Status: MVP (vertical slice).** One path works end to end on a local NTFS folder: browse, back up, keep history, verify, restore. It is **not** a production backup system: it reads the live disk (no VSS, no NTFS USN journal), has one administrator, no scheduler, no retention and no TLS of its own. [docs/mvp-status.md](docs/mvp-status.md) lists exactly what is done, partial and missing against [docs/plan.md](docs/plan.md).

### What works today

- A single Go service, `timevault-server`, with a bilingual (English / 中文) web UI in the OpenList style, light and dark themes, and a JSON API.
- **Explorer**: the live folder tree of a source, marked as protected, changed or new, plus name search through a local Everything instance (optional).
- **Time Machine**: choose any committed recovery point, browse the folder tree as it was, see deleted files, and list every version of a file.
- **Backups**: full and incremental backups into a content-addressed repository, with progress, cancel, skipped-file reporting and integrity verification. PostgreSQL stores the catalog.
- **Restore**: preview first, then a confirmed restore of files or folders into a folder below a configured restore root, with SHA-256 verification and skip / rename / overwrite policies.
- Sign-in, CSRF protection, a login throttle, an append-only audit log and a status page.

### Quick start (development, Windows PowerShell)

Needs Go 1.25+ and an installed PostgreSQL (see [docs/runbook.md](docs/runbook.md)). The scripts use a private database under the git-ignored `data\` folder and never touch an existing PostgreSQL service.

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-db.ps1 start    # private PostgreSQL
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-run.ps1         # build and run the server
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-demo.ps1        # in a second window: demo data
```

Open `http://127.0.0.1:8470/` and sign in as `admin`; the generated password is in `data\dev\admin-password.txt`. Run the tests with `. .\scripts\dev-env.ps1` followed by `go test ./... -count=1`.

### Documentation

| Document | Content |
|---|---|
| [docs/README.md](docs/README.md) | Index of all documentation |
| [docs/plan.md](docs/plan.md) | The original P0–P4 plan |
| [docs/mvp-status.md](docs/mvp-status.md) | What the MVP does and does not do |
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
| `internal/` | Catalog, backup engine, repository, restore, jobs, auth, audit, search adapter, HTTP server |
| `migrations/` | Database schema (embedded in the executable) |
| `web/` | Templates and static assets (embedded in the executable) |
| `scripts/` | Development helper scripts |
| `configs/` | Configuration template without secrets |
| `docs/` | Documentation |
| `data/` | Local development data (git-ignored) |

---

# 中文

## TimeVault

**TimeVault —— 企业文件备份、搜索与时间回溯**，面向 Windows 和 NTFS。它为本地文件夹保存时间点恢复点，可以浏览任意文件夹过去的状态、按名称搜索，并在预览之后把文件恢复到恢复根目录下的安全文件夹中。

**状态：MVP（垂直切片）。** 在本地 NTFS 文件夹上，浏览、备份、保留历史、校验、恢复这一条路径已经完整打通。它**不是**生产级备份系统：它读取在线磁盘（没有 VSS，也没有 NTFS USN journal），只有一个管理员，没有计划任务、保留策略，自身也没有 TLS。[docs/mvp-status.md](docs/mvp-status.md) 逐项列出了相对 [docs/plan.md](docs/plan.md) 已完成、部分完成和缺失的内容。

### 目前可用的功能

- 一个 Go 服务 `timevault-server`，带有 OpenList 风格的双语（English / 中文）网页界面、浅色和深色主题，以及 JSON API。
- **文件浏览**：备份源的实时文件夹树，标注为已保护、已变更或新增；另可通过本机的 Everything 实例按名称搜索（可选）。
- **时间机器**：选择任意已提交的恢复点，按当时的样子浏览文件夹树，查看已删除的文件，并列出某个文件的所有版本。
- **备份**：全量和增量备份到内容寻址的仓库，带进度、取消、跳过文件的报告和完整性校验。目录库保存在 PostgreSQL 中。
- **恢复**：先预览，再经确认把文件或文件夹恢复到所配置恢复根目录下的文件夹，带 SHA-256 校验以及跳过 / 改名 / 覆盖策略。
- 登录、CSRF 防护、登录限流、只追加的审计日志和状态页。

### 快速开始（开发环境，Windows PowerShell）

需要 Go 1.25+ 和已安装的 PostgreSQL（见 [docs/runbook.md](docs/runbook.md)）。脚本使用位于被 git 忽略的 `data\` 文件夹下的私有数据库，不会触碰已有的 PostgreSQL 服务。

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-db.ps1 start    # private PostgreSQL
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-run.ps1         # build and run the server
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-demo.ps1        # in a second window: demo data
```

打开 `http://127.0.0.1:8470/`，以 `admin` 登录；生成的密码在 `data\dev\admin-password.txt` 中。运行测试：先执行 `. .\scripts\dev-env.ps1`，再执行 `go test ./... -count=1`。

### 文档

| 文档 | 内容 |
|---|---|
| [docs/README.md](docs/README.md) | 全部文档的索引 |
| [docs/plan.md](docs/plan.md) | 最初的 P0–P4 计划 |
| [docs/mvp-status.md](docs/mvp-status.md) | MVP 做了什么、没做什么 |
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
| `internal/` | 目录库、备份引擎、仓库、恢复、任务、认证、审计、搜索适配器、HTTP 服务 |
| `migrations/` | 数据库结构（嵌入可执行文件） |
| `web/` | 模板和静态资源（嵌入可执行文件） |
| `scripts/` | 开发辅助脚本 |
| `configs/` | 不含机密信息的配置模板 |
| `docs/` | 文档 |
| `data/` | 本地开发数据（被 git 忽略） |
