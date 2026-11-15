# English

## Overview

How to set up, configure, run, operate and troubleshoot the TimeVault MVP. It describes what exists today; the service is a single Go program, `timevault-server`, that needs PostgreSQL and a repository folder. There is no installer and no Windows service wrapper yet.

## Prerequisites

| Item | Notes |
|---|---|
| Windows with NTFS | Developed and tested on Windows 11 Pro. The server also compiles on other systems (some path rules and tests are Windows specific), but only Windows was exercised. |
| Go 1.25 or newer | Needed to build (`go.mod` declares `go 1.25.0`); the restore code uses `os.Root`. A portable Go works: set `TIMEVAULT_GO_ROOT` to the Go folder (the one that contains `bin\go.exe`) and the dev scripts add it to `PATH`. |
| PostgreSQL | Tested with 16.4 only. The SQL needs at least PostgreSQL 11 (identity columns, `hashtextextended`), but older versions than 16 were not tried. |
| Everything 1.4.x (optional) | Only for search. Without it search shows "not configured" and everything else works. See [Everything search](#everything-search). |

## Quick start for development

The scripts keep everything under the git-ignored `data\` folder, never touch an existing PostgreSQL service and print no secrets. If PowerShell blocks the scripts, run them as `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1`.

1. `scripts\dev-db.ps1 start` creates (first time) and starts a private PostgreSQL cluster in `data\pgdev` on `127.0.0.1:55432` with the databases `timevault` and `timevault_test`. The random password is stored in `data\pgdev\db.env`. It needs the installed PostgreSQL binaries (`TIMEVAULT_PG_BIN` or `-PgBin` to point at them). Other actions: `stop`, `status`, `init`, `reset` (deletes the cluster).
2. `scripts\dev-run.ps1` starts the database if needed, builds `data\dev\bin\timevault-server.exe`, creates `data\dev` (a demo source folder, repository, restore folder, `server.env`, and a random admin password in `data\dev\admin-password.txt`) on the first run and runs the server in the foreground on `http://127.0.0.1:8470/` (`-Port` changes the port). Stop it with Ctrl+C.
3. `scripts\dev-demo.ps1` (with the server running) registers `data\dev\source` as the source "Demo files" and creates three recovery points through the API, leaving some uncommitted changes so the Explorer shows changed, new and deleted items.
4. Open the address, sign in as `admin` with the password from `data\dev\admin-password.txt`. The header has the language switch (English / 中文) and a light/dark theme toggle (the page follows the system setting until you use the toggle; the browser then remembers your choice).

5. Optional, for the search box: `scripts\dev-everything.ps1 -Action start`, then restart the server (see [Everything search](#everything-search)).

`. .\scripts\dev-env.ps1` loads the same settings into your own PowerShell session (`data\pgdev\db.env`, `data\dev\server.env`, and `data\everything\everything.env` when present) so you can run `go test` or `timevault-server` by hand.

## Commands

| Command | Purpose |
|---|---|
| `timevault-server serve` | Run the service (the default when no command is given). |
| `timevault-server migrate` | Apply the database migrations and exit. |
| `timevault-server hash-password` | Read a password (hidden when typed in a terminal, or one line from standard input) and print the value for `TIMEVAULT_ADMIN_PASSWORD_HASH`. |
| `timevault-server version` | Print the version. |

Build: `go build -o timevault-server.exe ./cmd/timevault-server`. The web templates and assets are embedded in the executable, and so are the migrations.

## Configuration reference

All settings are environment variables. Invalid values stop the program with a message that names the variable. Secrets are never logged.

| Variable | Default | Meaning |
|---|---|---|
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8470` | Address to listen on. A non-loopback address is refused unless `TIMEVAULT_ALLOW_NON_LOOPBACK=true`. |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | Confirms that a TLS reverse proxy protects a non-loopback listener. |
| `TIMEVAULT_DATABASE_URL` | none (required) | PostgreSQL connection URL. Secret. |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | Apply migrations at start. When `false` the service refuses to start if the schema is not current; run `migrate` first. |
| `TIMEVAULT_REPOSITORY_PATH` | none (required) | Repository folder. A new or empty folder is initialised; a non-empty folder that is not a TimeVault repository is refused. |
| `TIMEVAULT_RESTORE_ROOT` | empty | Folder below which restores write (one folder per restore, created when needed). Empty disables restore. It must not be inside or contain the repository. |
| `TIMEVAULT_AUTH_DISABLED` | `false` | Turns sign-in off. Only accepted on a loopback listener. For development only. |
| `TIMEVAULT_ADMIN_USER` | `admin` | Administrator user name. |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | none (required unless auth is disabled) | Output of `timevault-server hash-password`. Secret. |
| `TIMEVAULT_SESSION_KEY` | random per start | Base64 text that decodes to at least 32 bytes. Set it so sessions and restore confirmations survive a restart. Secret. |
| `TIMEVAULT_SESSION_TTL` | `8h` | Session lifetime, from `5m` to `720h`. |
| `TIMEVAULT_COOKIE_SECURE` | `false` | Adds `Secure` to cookies. Set it to `true` behind HTTPS. |
| `TIMEVAULT_EVERYTHING_URL` | empty | Base URL of the Everything HTTP server, for example `http://127.0.0.1:<port>/`. Empty disables search. |
| `TIMEVAULT_EVERYTHING_USERNAME`, `TIMEVAULT_EVERYTHING_PASSWORD` | empty | Optional HTTP Basic credentials for Everything. The password is a secret. |
| `TIMEVAULT_EVERYTHING_TIMEOUT` | `5s` | Per-search timeout, `100ms` to `30s`. |
| `TIMEVAULT_JOB_WORKERS` | `2` | Jobs that run at the same time (1 to 16). The database connection pool is sized to match (2 × workers + 8, at least 16 connections); PostgreSQL's `max_connections` must allow that. |
| `TIMEVAULT_HASH_WORKERS` | smaller of 4 and the CPU count | Files hashed and copied in parallel inside one backup (1 to 32). |
| `TIMEVAULT_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `TIMEVAULT_LOG_FORMAT` | `text` | `text` or `json`. |

Variables used only by the helper scripts and tests: `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN`, `TIMEVAULT_TEST_DATABASE_URL` (see [Tests](#tests)). [configs/timevault.example.env](../configs/timevault.example.env) is a template with placeholders.

## Running outside the dev scripts

1. Create a PostgreSQL role and database for TimeVault (use your own password):

   ```sql
   CREATE ROLE timevault LOGIN PASSWORD '<choose-a-password>';
   CREATE DATABASE timevault OWNER timevault;
   ```

2. Create the password hash: `timevault-server hash-password`.
3. Set the environment (see the template), then run `timevault-server migrate` (or set `TIMEVAULT_AUTO_MIGRATE=true`) and `timevault-server serve`.
4. For access from other computers put an HTTPS reverse proxy in front, listen on loopback, and set `TIMEVAULT_COOKIE_SECURE=true`. Listening on a non-loopback address directly needs `TIMEVAULT_ALLOW_NON_LOOPBACK=true` and still has no TLS.
5. The service has no Windows service wrapper. Start it with a method you trust (for example a scheduled task or a service manager); this was not tested. It stops cleanly on Ctrl+C or SIGTERM, waiting up to 30 seconds for the HTTP server and running jobs.

At start the service marks jobs that were running when the previous process ended as failed ("interrupted by a service restart"), removes the partial history of unfinished recovery points, and then resumes queued jobs.

## Tests

`go test ./...` runs everything. Tests that need PostgreSQL are skipped unless `TIMEVAULT_TEST_DATABASE_URL` points at a database; each test creates a private schema named `tvtest_<random>` and drops it, so use the `timevault_test` database from `dev-db.ps1`, never the working database. Tests that exercise junctions and Windows file locking run only on Windows. Everything live tests are described in [Everything search](#everything-search).

```powershell
. .\scripts\dev-env.ps1      # also sets TIMEVAULT_TEST_DATABASE_URL from data\pgdev\db.env
go test ./... -count=1
```

## Everything search

Search is optional. TimeVault asks a **local Everything instance** (the HTTP server of Everything 1.4) which files and folders exist below a registered source *right now*. It is not a history search: browsing the past uses the catalog. Without Everything the search box shows "not configured" or "unavailable" and everything else keeps working.

**Set it up**

1. Use Everything 1.4.x with its HTTP server enabled, bound to `127.0.0.1` only (the adapter refuses any other host), protected with a user name and password, and with "Allow file download" switched **off** (`http_server_allow_file_download` is on by default and TimeVault does not need it). A dedicated named instance is better than your everyday one; for development `scripts\dev-everything.ps1` creates one.
2. Set `TIMEVAULT_EVERYTHING_URL` (for example `http://127.0.0.1:<port>/`) and, if you set credentials, `TIMEVAULT_EVERYTHING_USERNAME` and `TIMEVAULT_EVERYTHING_PASSWORD`. Make sure Everything indexes the source folders.
3. Open **Explorer** and type in the search box. The **Status** page shows the search engine as `ok`, `degraded` (reachable but answering wrongly or slowly) or `disabled`.

**What happens to a query**

- The typed text is split into terms (whitespace separates them, double quotes make a phrase; at most 256 bytes and 16 terms). `*` and `?` act as separators because Everything keeps treating them as wildcards even inside quotes.
- Every term is put in double quotes, so Everything operators such as `|`, `!`, `<>`, `regex:`, `size:` or `content:` are searched as plain text.
- The query is confined to the source with `path:"<source root>\"`. The trailing backslash matters: `path:` is a plain substring match, so without it a source `D:\src` would also match `D:\src2`. `infolder:` was not used because it only matches direct children.
- Every result is checked again against the source root. Anything outside is dropped and counted, never shown.
- Pages hold at most 200 results; folder sizes are shown as unknown when Everything does not index them.

**Development instance** (`scripts\dev-everything.ps1`, needs Everything 1.4.x installed; point `TIMEVAULT_EVERYTHING_EXE` at `Everything.exe` if it is not found):

| Action | Effect |
|---|---|
| `-Action start` | Starts the named instance `TimeVault` from a private copy of `Everything.exe` in `data\everything\bin`, with its own ini and database in `data\everything`. It indexes only `-IndexRoot` (default `data\dev\source`) as a folder index, listens on `127.0.0.1:38480` (`-Port`), has file download off, shows no tray icon and keeps no history. Random credentials are kept in `data\everything\http-credentials.txt`; the settings for the server are written to `data\everything\everything.env`, which `dev-env.ps1` and `dev-run.ps1` load. |
| `-Action stop`, `status`, `reset` | Stop it (only its own process), show whether it answers, or stop and delete its ini, database and `everything.env`. |
| `-Action fixture` | Creates the test tree used by the live tests under `-IndexRoot`. |

The script never touches your normal Everything instance and never writes into the Everything installation folder. It was tested from a normal (not elevated) shell. After `start`, restart the TimeVault server so it picks up `everything.env`.

Live tests against that instance:

```powershell
$s = 'powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-everything.ps1'
Invoke-Expression "$s -Action fixture -IndexRoot data\everything-test"
Invoke-Expression "$s -Action start -IndexRoot data\everything-test"
. .\scripts\dev-env.ps1
go test ./internal/search/... -run Live -count=1 -v
Invoke-Expression "$s -Action stop"
```

Without `TIMEVAULT_TEST_EVERYTHING_ROOT` the live tests are skipped, so `go test ./...` never needs Everything.

**Verified and not verified.** Observed on a live Everything 1.4.1.1024: the JSON shape (`size` and `date_modified` are strings; the date is a Windows FILETIME), scoping with `path:"<root>\"`, literal quoting of operators, paging and the empty-query shape. Not verified: Everything 1.5, indexes of NTFS volumes with USN monitoring (the development instance uses a folder index), UNC or drive-root sources (unit tests only), and speed at millions of entries (`path:` scans full paths).

## Operating

- **Health.** `GET /healthz` returns `ok` without signing in and only proves the process is up. The **Status** page checks the database, the repository (a write test and free space; "degraded" below 5% free), the search engine, the restore folder and the job queue, and lists VSS and USN as "disabled". Only `/healthz`, the sign-in page, the language switch and the static assets work without a session.
- **Backups.** In **Backups**, add a source (a local folder; it must not overlap the repository, the restore folder or another source), then **Back up now**. A normal backup trusts size and modification time to skip unchanged files; **Full re-read** reads and hashes every file again. Only one backup per source runs at a time. A backup that cannot read a file (locked, changed while reading, link, special file) records it as skipped with the reason, keeps the previous good version, and still completes with a warning.
- **Verify.** The verify job on a recovery point checks the catalog against the manifest digest and that every referenced object exists with the right size; "deep" also re-hashes the content. Run it after copying or restoring the repository.
- **Restore.**
  1. Open **Time Machine**, choose a source and a recovery point, then tick files or folders and choose **Restore selected…**, or choose **Restore this folder…** / **Restore everything…**. The version page of a single file has **Restore…**.
  2. Review the preview: counts, size, conflicts, blocked items (missing content, unsafe names, never captured) and the planned changes.
  3. Choose the destination folder name (a folder below `TIMEVAULT_RESTORE_ROOT`, created if it does not exist), the conflict policy (skip, rename, overwrite) and, if you accept restoring only part of the selection, the partial option. Overwriting needs an extra confirmation.
  4. **Start restore** within 15 minutes of the preview (the token expires and is bound to your user and the exact plan; any change needs a new preview). Follow the job page. Do not start another restore into the same folder until it has finished. Copy the result from `<TIMEVAULT_RESTORE_ROOT>\<destination>` to its final place yourself. Restores never write into a source.
- **Audit.** The **Audit** page lists sign-ins, source changes, backups, verifies, restore previews and results, and cancellations. The application has no way to change or delete these records, and a database trigger rejects UPDATE and DELETE statements on them.
- **Backing up TimeVault itself.** Back up both the PostgreSQL database (for example with `pg_dump`) and the whole repository folder. The repository only grows, so an older database copy with a newer repository is consistent, but a newer database with an older repository reports missing objects (verify shows them and restores block). After restoring either, run a deep verify.

## Troubleshooting

| Symptom | Cause and action |
|---|---|
| The service stops with "is not a loopback address" | Use `127.0.0.1` behind a reverse proxy, or set `TIMEVAULT_ALLOW_NON_LOOPBACK=true` knowingly. |
| The service stops with a schema message | Run `timevault-server migrate`, or set `TIMEVAULT_AUTO_MIGRATE=true`. |
| The service stops with "another TimeVault service is already running against this database" | A second `timevault-server` uses the same database (schema). Stop the other one first; its lock is released when its process ends. |
| The service stops with "not a TimeVault repository" | `TIMEVAULT_REPOSITORY_PATH` points at a non-empty folder without `timevault-repository.json`. Use an empty folder. |
| `go` is not found by the scripts | Add Go to `PATH` or set `TIMEVAULT_GO_ROOT`. |
| `dev-db.ps1` cannot find PostgreSQL | Set `TIMEVAULT_PG_BIN` or pass `-PgBin`. |
| Sign-in answers 429 | Five failed attempts per address in 10 minutes. Wait, or restart the service (the counter is in memory). |
| "Restore is not enabled" | `TIMEVAULT_RESTORE_ROOT` is empty. |
| The restore preview is rejected as stale or expired | Preview again; tokens last 15 minutes and are tied to the exact plan. After a restart without `TIMEVAULT_SESSION_KEY` the key changes too. |
| Files are listed as skipped (locked) | The file was open by another program, or changed while it was read. Run the backup again later; there is no VSS in the MVP. |
| A restore reports a missing or corrupt object | The repository lost or damaged a file. Run a deep verify, restore the repository from its backup, or choose another recovery point. Nothing is restored from a bad object. |
| An interrupted restore left `.tvpart-*` files | Temporary files in the destination folder from a crashed restore. They can be deleted. |
| Search says "not configured" or "unavailable" | See [Everything search](#everything-search). History and restore keep working. |
| A job stays "failed: interrupted by a service restart" | The service was stopped while the job ran. Start the job again; unfinished recovery points were already cleaned up. |

---

# 中文

## 概述

介绍如何安装、配置、运行、运维和排查 TimeVault MVP。它描述的是目前实际存在的内容：服务是一个 Go 程序 `timevault-server`，需要 PostgreSQL 和一个仓库文件夹。目前没有安装程序，也没有 Windows 服务封装。

## 前置条件

| 项目 | 说明 |
|---|---|
| 带 NTFS 的 Windows | 在 Windows 11 Pro 上开发和测试。服务也能在其他系统上编译（部分路径规则和测试仅适用于 Windows），但只在 Windows 上实际运行过。 |
| Go 1.25 或更新版本 | 构建所需（`go.mod` 声明了 `go 1.25.0`）；恢复代码使用了 `os.Root`。可以使用便携版 Go：把 `TIMEVAULT_GO_ROOT` 设为 Go 文件夹（包含 `bin\go.exe` 的那个），开发脚本会把它加入 `PATH`。 |
| PostgreSQL | 只测试过 16.4。所用 SQL 至少需要 PostgreSQL 11（identity 列、`hashtextextended`），但没有试过低于 16 的版本。 |
| Everything 1.4.x（可选） | 仅用于搜索。没有它时搜索显示"未配置"，其他功能正常。见 [Everything 搜索](#everything-搜索)。 |

## 开发环境快速开始

脚本把所有内容放在被 git 忽略的 `data\` 文件夹下，不会触碰已有的 PostgreSQL 服务，也不会打印任何机密信息。如果 PowerShell 拦截脚本，请用 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1` 运行。

1. `scripts\dev-db.ps1 start`（首次）创建并启动一个私有 PostgreSQL 集群，位于 `data\pgdev`，监听 `127.0.0.1:55432`，包含数据库 `timevault` 和 `timevault_test`。随机密码保存在 `data\pgdev\db.env`。它需要已安装的 PostgreSQL 二进制文件（用 `TIMEVAULT_PG_BIN` 或 `-PgBin` 指定位置）。其他动作：`stop`、`status`、`init`、`reset`（会删除该集群）。
2. `scripts\dev-run.ps1` 在需要时启动数据库，构建 `data\dev\bin\timevault-server.exe`，首次运行时创建 `data\dev`（示例备份源文件夹、仓库、恢复目录、`server.env`，以及保存在 `data\dev\admin-password.txt` 的随机管理员密码），并在前台运行服务，地址为 `http://127.0.0.1:8470/`（用 `-Port` 修改端口）。按 Ctrl+C 停止。
3. `scripts\dev-demo.ps1`（服务运行时）把 `data\dev\source` 登记为备份源"Demo files"，并通过 API 创建三个恢复点，同时留下一些未备份的改动，使文件浏览页能显示已变更、新增和已删除的项目。
4. 打开该地址，用 `admin` 和 `data\dev\admin-password.txt` 中的密码登录。页眉有语言切换（English / 中文）和浅色/深色主题切换按钮（在使用该按钮之前，页面跟随系统设置；之后浏览器会记住你的选择）。

5. 可选，用于搜索框：`scripts\dev-everything.ps1 -Action start`，然后重启服务（见 [Everything 搜索](#everything-搜索)）。

`. .\scripts\dev-env.ps1` 会把同样的设置加载到你自己的 PowerShell 会话中（`data\pgdev\db.env`、`data\dev\server.env`，以及存在时的 `data\everything\everything.env`），这样可以手动运行 `go test` 或 `timevault-server`。

## 命令

| 命令 | 用途 |
|---|---|
| `timevault-server serve` | 运行服务（不带命令时的默认行为）。 |
| `timevault-server migrate` | 应用数据库迁移后退出。 |
| `timevault-server hash-password` | 读取密码（在终端中输入时不回显，或从标准输入读取一行），并输出可用于 `TIMEVAULT_ADMIN_PASSWORD_HASH` 的值。 |
| `timevault-server version` | 输出版本号。 |

构建：`go build -o timevault-server.exe ./cmd/timevault-server`。Web 模板、静态资源和迁移脚本都已嵌入可执行文件。

## 配置参考

所有设置都是环境变量。无效的值会使程序退出并给出点名该变量的提示。机密信息绝不会写入日志。

| 变量 | 默认值 | 含义 |
|---|---|---|
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8470` | 监听地址。除非设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，否则会拒绝非回环地址。 |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | 确认由 TLS 反向代理保护非回环监听。 |
| `TIMEVAULT_DATABASE_URL` | 无（必填） | PostgreSQL 连接 URL。机密。 |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | 启动时应用迁移。为 `false` 时，如果数据库结构不是最新，服务会拒绝启动；请先运行 `migrate`。 |
| `TIMEVAULT_REPOSITORY_PATH` | 无（必填） | 仓库文件夹。新文件夹或空文件夹会被初始化；不是 TimeVault 仓库的非空文件夹会被拒绝。 |
| `TIMEVAULT_RESTORE_ROOT` | 空 | 恢复写入的位置（每次恢复一个文件夹，需要时创建）。为空表示禁用恢复。不得位于仓库之内，也不得包含仓库。 |
| `TIMEVAULT_AUTH_DISABLED` | `false` | 关闭登录。只在回环监听时被接受。仅用于开发。 |
| `TIMEVAULT_ADMIN_USER` | `admin` | 管理员用户名。 |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | 无（未关闭登录时必填） | `timevault-server hash-password` 的输出。机密。 |
| `TIMEVAULT_SESSION_KEY` | 每次启动随机生成 | 解码后至少 32 字节的 Base64 文本。设置它可使会话和恢复确认在重启后仍然有效。机密。 |
| `TIMEVAULT_SESSION_TTL` | `8h` | 会话有效期，范围 `5m` 到 `720h`。 |
| `TIMEVAULT_COOKIE_SECURE` | `false` | 给 Cookie 加上 `Secure`。在 HTTPS 之后请设为 `true`。 |
| `TIMEVAULT_EVERYTHING_URL` | 空 | Everything HTTP 服务的基础 URL，例如 `http://127.0.0.1:<port>/`。为空表示禁用搜索。 |
| `TIMEVAULT_EVERYTHING_USERNAME`、`TIMEVAULT_EVERYTHING_PASSWORD` | 空 | Everything 的可选 HTTP Basic 凭据。密码属于机密。 |
| `TIMEVAULT_EVERYTHING_TIMEOUT` | `5s` | 单次搜索的超时，范围 `100ms` 到 `30s`。 |
| `TIMEVAULT_JOB_WORKERS` | `2` | 同时运行的任务数（1 到 16）。数据库连接池的大小与之匹配（2 × worker 数 + 8，至少 16 个连接）；PostgreSQL 的 `max_connections` 必须允许这个数量。 |
| `TIMEVAULT_HASH_WORKERS` | 4 与 CPU 数中较小者 | 一次备份内并行哈希和复制的文件数（1 到 32）。 |
| `TIMEVAULT_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error`。 |
| `TIMEVAULT_LOG_FORMAT` | `text` | `text` 或 `json`。 |

仅供辅助脚本和测试使用的变量：`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN`、`TIMEVAULT_TEST_DATABASE_URL`（见[测试](#测试)）。[configs/timevault.example.env](../configs/timevault.example.env) 是带占位符的模板。

## 不使用开发脚本时的运行方式

1. 为 TimeVault 创建 PostgreSQL 角色和数据库（请使用你自己的密码）：

   ```sql
   CREATE ROLE timevault LOGIN PASSWORD '<choose-a-password>';
   CREATE DATABASE timevault OWNER timevault;
   ```

2. 生成密码哈希：`timevault-server hash-password`。
3. 设置环境变量（参见模板），然后运行 `timevault-server migrate`（或设置 `TIMEVAULT_AUTO_MIGRATE=true`）和 `timevault-server serve`。
4. 如需从其他计算机访问，请在前面放置 HTTPS 反向代理，让服务监听回环地址，并设置 `TIMEVAULT_COOKIE_SECURE=true`。直接监听非回环地址需要 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，并且依然没有 TLS。
5. 服务没有 Windows 服务封装。请用你信任的方式启动它（例如计划任务或服务管理器）；这一点没有测试过。收到 Ctrl+C 或 SIGTERM 时它会正常退出，最多等待 30 秒让 HTTP 服务和正在运行的任务结束。

启动时，服务会把上次进程结束时仍在运行的任务标记为失败（"interrupted by a service restart"），清除未完成恢复点的部分历史，然后继续处理排队的任务。

## 测试

`go test ./...` 运行全部测试。需要 PostgreSQL 的测试在 `TIMEVAULT_TEST_DATABASE_URL` 未指向某个数据库时会被跳过；每个测试都会创建名为 `tvtest_<random>` 的私有 schema 并在结束后删除，所以请使用 `dev-db.ps1` 创建的 `timevault_test` 数据库，不要使用正式使用的数据库。涉及联接点和 Windows 文件锁的测试只在 Windows 上运行。Everything 在线测试见 [Everything 搜索](#everything-搜索)。

```powershell
. .\scripts\dev-env.ps1      # also sets TIMEVAULT_TEST_DATABASE_URL from data\pgdev\db.env
go test ./... -count=1
```

## Everything 搜索

搜索是可选的。TimeVault 向**本机的 Everything 实例**（Everything 1.4 的 HTTP 服务）询问，在已登记的备份源之下*此刻*存在哪些文件和文件夹。它不是历史搜索：浏览过去的状态使用目录库。没有 Everything 时，搜索框显示"未配置"或"不可用"，其他功能照常工作。

**如何设置**

1. 使用启用了 HTTP 服务的 Everything 1.4.x：只绑定到 `127.0.0.1`（适配器拒绝任何其他主机），用用户名和密码保护，并**关闭**"允许文件下载"（`http_server_allow_file_download` 默认是开启的，而 TimeVault 并不需要它）。专用的命名实例比日常使用的实例更好；开发时 `scripts\dev-everything.ps1` 会为你创建一个。
2. 设置 `TIMEVAULT_EVERYTHING_URL`（例如 `http://127.0.0.1:<port>/`），如果设置了凭据，还要设置 `TIMEVAULT_EVERYTHING_USERNAME` 和 `TIMEVAULT_EVERYTHING_PASSWORD`。并确保 Everything 已为备份源文件夹建立索引。
3. 打开**文件浏览**并在搜索框中输入。**状态**页会把搜索引擎显示为 `ok`、`degraded`（可达但应答错误或过慢）或 `disabled`。

**一次查询会经历什么**

- 输入的文本会被拆分成词（空白分隔，双引号表示一个短语；最多 256 字节和 16 个词）。`*` 和 `?` 被当作分隔符，因为即使在引号里 Everything 仍把它们当作通配符。
- 每个词都会被加上双引号，所以 `|`、`!`、`<>`、`regex:`、`size:`、`content:` 这样的 Everything 操作符都被当作普通文本来搜索。
- 查询通过 `path:"<备份源根目录>\"` 限定在备份源之内。末尾的反斜杠很重要：`path:` 是普通的子串匹配，没有它的话，备份源 `D:\src` 也会匹配 `D:\src2`。没有使用 `infolder:`，因为它只匹配直接子项。
- 每个结果都会再次对照备份源根目录检查。范围之外的结果会被丢弃并计数，绝不会显示。
- 每页最多 200 条结果；Everything 没有索引文件夹大小时，文件夹大小显示为未知。

**开发用实例**（`scripts\dev-everything.ps1`，需要已安装 Everything 1.4.x；找不到时请把 `TIMEVAULT_EVERYTHING_EXE` 指向 `Everything.exe`）：

| 动作 | 效果 |
|---|---|
| `-Action start` | 用 `data\everything\bin` 中 `Everything.exe` 的私有副本启动名为 `TimeVault` 的实例，它的 ini 和数据库放在 `data\everything`。它只把 `-IndexRoot`（默认 `data\dev\source`）作为文件夹索引来建立索引，监听 `127.0.0.1:38480`（可用 `-Port` 修改），关闭文件下载，不显示托盘图标，也不保留历史记录。随机凭据保存在 `data\everything\http-credentials.txt`；服务所需的设置写入 `data\everything\everything.env`，`dev-env.ps1` 和 `dev-run.ps1` 会加载它。 |
| `-Action stop`、`status`、`reset` | 停止它（只停止它自己的进程）、显示它是否应答，或停止并删除它的 ini、数据库和 `everything.env`。 |
| `-Action fixture` | 在 `-IndexRoot` 之下创建在线测试使用的测试目录树。 |

该脚本绝不会触碰你日常使用的 Everything 实例，也绝不会写入 Everything 的安装文件夹。它是在普通（非提升权限）的 shell 中测试的。`start` 之后，请重启 TimeVault 服务，使其读取 `everything.env`。

针对该实例的在线测试：

```powershell
$s = 'powershell -NoProfile -ExecutionPolicy Bypass -File scripts\dev-everything.ps1'
Invoke-Expression "$s -Action fixture -IndexRoot data\everything-test"
Invoke-Expression "$s -Action start -IndexRoot data\everything-test"
. .\scripts\dev-env.ps1
go test ./internal/search/... -run Live -count=1 -v
Invoke-Expression "$s -Action stop"
```

没有 `TIMEVAULT_TEST_EVERYTHING_ROOT` 时在线测试会被跳过，所以 `go test ./...` 从不需要 Everything。

**已验证与未验证。** 已在运行中的 Everything 1.4.1.1024 上观察到：JSON 结构（`size` 和 `date_modified` 是字符串；日期是 Windows FILETIME）、使用 `path:"<root>\"` 限定范围、操作符的字面引用、分页以及空查询的结构。未验证：Everything 1.5、带 USN 监控的 NTFS 卷索引（开发实例使用文件夹索引）、UNC 或驱动器根目录的备份源（只有单元测试），以及数百万条目时的速度（`path:` 会扫描完整路径）。

## 运维

- **健康检查。** `GET /healthz` 无需登录即返回 `ok`，只能证明进程在运行。**状态**页检查数据库、仓库（写入测试和剩余空间；剩余不足 5% 时为"降级"）、搜索引擎、恢复目录和任务队列，并把 VSS 和 USN 列为"已禁用"。只有 `/healthz`、登录页、语言切换和静态资源无需会话即可访问。
- **备份。** 在**备份**页添加备份源（本地文件夹；不能与仓库、恢复目录或其他备份源重叠），然后点击**立即备份**。普通备份依据大小和修改时间跳过未变化的文件；**完整重读**会重新读取并哈希每个文件。每个备份源同一时间只运行一个备份。无法读取的文件（被锁定、读取时发生变化、链接、特殊文件）会被记录为"跳过"并写明原因，保留之前的良好版本，备份本身仍以警告完成。
- **校验。** 对恢复点运行校验任务，会对照清单摘要检查目录库，并检查每个被引用的对象都存在且大小正确；"深度"校验还会重新哈希内容。复制或恢复仓库之后请运行一次。
- **恢复。**
  1. 打开**时间机器**，选择备份源和恢复点，然后勾选文件或文件夹并选择**恢复所选项…**，或选择**恢复此文件夹…** / **恢复全部内容…**。单个文件的版本页面有**恢复…**。
  2. 查看预览：数量、大小、冲突、受阻项目（内容缺失、不安全的名称、从未备份成功）以及计划的变更。
  3. 选择目标文件夹名称（`TIMEVAULT_RESTORE_ROOT` 之下的文件夹，不存在时会创建）、冲突策略（跳过、改名、覆盖），以及在接受只恢复所选内容的一部分时勾选的部分恢复选项。覆盖需要额外确认。
  4. 在预览后 15 分钟内点击**开始恢复**（令牌会过期，并与你的用户和精确的计划绑定；任何更改都需要重新预览）。在任务页面查看进度。在它结束之前，不要向同一个文件夹再启动另一个恢复。请自行把结果从 `<TIMEVAULT_RESTORE_ROOT>\<destination>` 复制到最终位置。恢复绝不会写入备份源。
- **审计。** **审计**页列出登录、备份源变更、备份、校验、恢复预览与结果以及取消操作。应用程序没有修改或删除这些记录的途径，并且数据库触发器会拒绝对它们的 UPDATE 和 DELETE 语句。
- **备份 TimeVault 自身。** 需要同时备份 PostgreSQL 数据库（例如用 `pg_dump`）和整个仓库文件夹。仓库只会增长，所以较旧的数据库副本配上较新的仓库是一致的，而较新的数据库配上较旧的仓库会报告对象缺失（校验会列出，恢复会被阻止）。无论恢复了哪一个，之后都请运行一次深度校验。

## 故障排查

| 现象 | 原因与处理 |
|---|---|
| 服务以"is not a loopback address"退出 | 在反向代理之后使用 `127.0.0.1`，或在明确知晓风险的情况下设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`。 |
| 服务以数据库结构相关的提示退出 | 运行 `timevault-server migrate`，或设置 `TIMEVAULT_AUTO_MIGRATE=true`。 |
| 服务以"another TimeVault service is already running against this database"退出 | 另一个 `timevault-server` 正在使用同一个数据库（schema）。请先停止那一个；它的锁会在其进程结束时释放。 |
| 服务以"not a TimeVault repository"退出 | `TIMEVAULT_REPOSITORY_PATH` 指向的非空文件夹中没有 `timevault-repository.json`。请使用空文件夹。 |
| 脚本找不到 `go` | 把 Go 加入 `PATH`，或设置 `TIMEVAULT_GO_ROOT`。 |
| `dev-db.ps1` 找不到 PostgreSQL | 设置 `TIMEVAULT_PG_BIN` 或传入 `-PgBin`。 |
| 登录返回 429 | 每个地址 10 分钟内失败 5 次。请等待，或重启服务（计数保存在内存中）。 |
| "Restore is not enabled" | `TIMEVAULT_RESTORE_ROOT` 为空。 |
| 恢复预览被判定为过期或失效 | 重新预览；令牌有效期 15 分钟，并与精确的计划绑定。在没有设置 `TIMEVAULT_SESSION_KEY` 的情况下重启，密钥也会变化。 |
| 文件被列为跳过（被锁定） | 文件被其他程序打开，或在读取时发生变化。请稍后再次备份；MVP 没有 VSS。 |
| 恢复报告对象缺失或损坏 | 仓库丢失或损坏了文件。请运行深度校验，从备份恢复仓库，或选择其他恢复点。不会从损坏的对象恢复任何内容。 |
| 中断的恢复留下了 `.tvpart-*` 文件 | 这是崩溃的恢复在目标文件夹中留下的临时文件，可以删除。 |
| 搜索显示"未配置"或"不可用" | 见 [Everything 搜索](#everything-搜索)。历史和恢复仍可使用。 |
| 任务一直显示"failed: interrupted by a service restart" | 任务运行时服务被停止。请重新启动该任务；未完成的恢复点已经被清理。 |
