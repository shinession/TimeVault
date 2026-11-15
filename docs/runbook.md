# English

## Overview

How to set up, configure, run, operate and troubleshoot TimeVault (version 0.1.1). It describes what exists today; the service is a single Go program, `timevault-server`, that needs PostgreSQL and a repository folder, and, for company sign-in, the Redis user directory of another application. There is no installer and no Windows service wrapper yet.

## Prerequisites

| Item | Notes |
|---|---|
| Windows with NTFS | Developed and tested on Windows 11 Pro. The server also compiles on other systems (some path rules and tests are Windows specific), but only Windows was exercised. |
| Go 1.25 or newer | Needed to build (`go.mod` declares `go 1.25.0`); the restore code uses `os.Root`. A portable Go works: set `TIMEVAULT_GO_ROOT` to the Go folder (the one that contains `bin\go.exe`) and the dev scripts add it to `PATH`. To keep it across terminals without touching the system `PATH`, put the line `TIMEVAULT_GO_ROOT=<Go folder>` into `data\local.env` (git-ignored; you create it). |
| PostgreSQL | Tested with 16.4 only. The SQL needs at least PostgreSQL 11 (identity columns, `hashtextextended`), but older versions than 16 were not tried. |
| Redis (optional) | The user directory of another (dash) application: hashes named `app_user_<ee_code>_<name>_<email>`. Without it only the local administrator can sign in. Tested against an in-process fake and pickle data written by Python, not against the company's own Redis: run `timevault-server check-redis-users` on it once. |
| Everything 1.4.x (optional) | Only for search. Without it search shows "not configured" and everything else works. See [Everything search](#everything-search). |

## Quick start for development

The scripts keep everything under the git-ignored `data\` folder, never touch an existing PostgreSQL service and print no secrets. If PowerShell blocks the scripts, run them as `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1`. If Go is not on your `PATH`, first create `data\local.env` with the line `TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>`; without it `dev-run.ps1` stops at once with "The go command was not found".

1. `scripts\dev-db.ps1 start` creates (first time) and starts a private PostgreSQL cluster in `data\pgdev` on `127.0.0.1:55432` with the databases `timevault` and `timevault_test`. The random password is stored in `data\pgdev\db.env`. It needs the installed PostgreSQL binaries (`TIMEVAULT_PG_BIN` or `-PgBin` to point at them). Other actions: `stop`, `status`, `init`, `reset` (deletes the cluster). **You can skip this step and use a PostgreSQL server you already have**: edit `data\pgdev\db.env` so that `TIMEVAULT_DATABASE_URL` (and `TIMEVAULT_TEST_DATABASE_URL`, for the tests) point at it, after creating the database and the role yourself. The scripts then start no private cluster, and `dev-db.ps1` refuses to overwrite or delete a `db.env` that points at another server.
2. `scripts\dev-run.ps1` starts the private database when the database settings point at it (port 55432) and otherwise expects the server you configured to be running, builds `data\dev\bin\timevault-server.exe`, and on the first run creates `data\dev` (a demo source folder, repository, restore folder, `server.env`, and a random password for the local administrator `admin` in `data\dev\admin-password.txt`) and `.env` in the repository folder, which is the service's own settings file (commented, git-ignored). It then runs the server in the foreground on `http://127.0.0.1:8660/` (`-Port` changes the port; without it `TIMEVAULT_LISTEN_ADDR` from `.env` applies) and prints whether the Redis user directory is configured. Stop it with Ctrl+C.
3. `scripts\dev-demo.ps1` (with the server running) registers `data\dev\source` as the monitored folder "Demo files" and creates three recovery points through the API, leaving some uncommitted changes so the Explorer shows changed, new and deleted items.
4. Open the address. Anyone can browse. To back up, restore or change settings, sign in as `admin` with the password from `data\dev\admin-password.txt`, or with a company account after you put `TIMEVAULT_REDIS_URL` into `.env` (check it with `data\dev\bin\timevault-server.exe check-redis-users`, run from the repository folder). The header has the language switch (English / 中文) and a light/dark theme toggle (the page follows the system setting until you use the toggle; the browser then remembers your choice).

5. Optional, for the search box: `scripts\dev-everything.ps1 -Action start`, then restart the server (see [Everything search](#everything-search)).

`. .\scripts\dev-env.ps1` loads the same settings into your own PowerShell session (`data\pgdev\db.env`, `data\dev\server.env`, `data\everything\everything.env` and `data\local.env`, each when present) so you can run `go test` or `timevault-server` by hand; it also puts Go on the `PATH` of that session when `TIMEVAULT_GO_ROOT` is set.

## Commands

| Command | Purpose |
|---|---|
| `timevault-server serve` | Run the service (the default when no command is given). |
| `timevault-server migrate` | Apply the database migrations and exit. |
| `timevault-server hash-password` | Read a password (hidden when typed in a terminal, or one line from standard input) and print the value for `TIMEVAULT_ADMIN_PASSWORD_HASH` (the optional local administrator). |
| `timevault-server check-redis-users` | Connect to the Redis user directory (`TIMEVAULT_REDIS_URL`) and print how much of it can be used: the number of keys, readable and unreadable records, activated accounts, types `admin` and `user`, accounts without a password, employee IDs that appear on more than one key, and per field how many values could be decoded. It prints no name, e-mail address, key or password. Exit code 1 when Redis does not answer or no record is readable. Run it from the folder that holds `.env`, or set `TIMEVAULT_ENV_FILE`. |
| `timevault-server version` | Print the version. |

Build: `go build -o timevault-server.exe ./cmd/timevault-server`. The web templates and assets are embedded in the executable, and so are the migrations.

## Where settings live

| What | Where | Changed by | Takes effect |
|---|---|---|---|
| Deployment: database URL, Redis URL, listen address, backup folder (`TIMEVAULT_REPOSITORY_PATH`), restore folder (`TIMEVAULT_RESTORE_ROOT`), session key, search, workers, logging | The process environment and/or the `.env` file | Whoever installs the service, by editing the file | After a restart |
| Monitored folders: name, folder, exclude patterns, on or off, automatic schedule | The database (table `sources`) | Administrators, on the **Settings** page or with `PATCH /api/v1/sources/{id}` | At once |
| Accounts and roles | Redis (`app_user_*`), written by another application | That application | Within a minute for a session that is already signed in |

- The `.env` file is the one named by `TIMEVAULT_ENV_FILE` (it must exist), otherwise `.env` in the working directory, otherwise `.env` next to `timevault-server.exe`. The first one found is used; files are not merged. **A variable that is set in the process environment wins over the same key in the file.** In development `scripts\dev-run.ps1` first loads `data\pgdev\db.env`, `data\dev\server.env`, `data\everything\everything.env` and `data\local.env` into the environment, so a key set there cannot be changed in `.env`.
- Syntax: one `KEY=VALUE` per line. Blank lines and lines that start with `#` are ignored, a leading `export ` is allowed, and a UTF-8 byte order mark and Windows line ends are accepted. The value is taken literally up to the end of the line, so a path with `\` or a password with `#` needs no escaping (and a trailing ` # comment` would become part of the value); one pair of matching single or double quotes around the value is removed; the last assignment of a key wins. An error names the line number and never shows the line.
- The file holds secrets (the database and Redis passwords, the session key). `/.env` is in `.gitignore`; keep the file readable only by the account that runs the service, and never commit a filled-in copy. [configs/timevault.example.env](../configs/timevault.example.env) is the template.
- The **Settings** page (any signed-in account may open it) shows the monitored folders, the backup folder and restore folder read-only with the free space of the repository, and the running configuration: version, listen address, database and Redis as `host:port/name`, the settings file, the local administrator's name, the search server. Secrets are never shown. Only administrators see the forms that change the monitored folders.

## Configuration reference

Every setting is an environment variable or a line of the `.env` file (see [Where settings live](#where-settings-live); the environment wins). Invalid values stop the program with a message that names the variable. Secrets are never logged.

| Variable | Default | Meaning |
|---|---|---|
| `TIMEVAULT_ENV_FILE` | none | Path of the settings file to read instead of looking for `.env`. It must exist. Only the process environment can set it. |
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8660` | Address to listen on. A non-loopback address is refused unless `TIMEVAULT_ALLOW_NON_LOOPBACK=true`. |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | Confirms that a TLS reverse proxy protects a non-loopback listener. The service logs a warning at start when it listens beyond this computer. |
| `TIMEVAULT_DATABASE_URL` | none (required) | PostgreSQL connection URL. Secret. |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | Apply migrations at start. When `false` the service refuses to start if the schema is not current; run `migrate` first. |
| `TIMEVAULT_REPOSITORY_PATH` | none (required) | Backup folder (the repository). A new or empty folder is initialised; a non-empty folder that is not a TimeVault repository is refused. Needs a restart to change. |
| `TIMEVAULT_RESTORE_ROOT` | empty | Folder below which restores write (one folder per restore, created when needed). Empty disables restore. It must not be inside or contain the repository. |
| `TIMEVAULT_AUTH_DISABLED` | `false` | Turns sign-in off: everyone is the local administrator. Only accepted on a loopback listener. For development only. |
| `TIMEVAULT_REDIS_URL` | empty | The Redis user directory, `redis://[:password@]host:port/db` (`rediss://` for TLS). Empty means no company accounts. Secret. |
| `TIMEVAULT_REDIS_KEY_PREFIX` | `app_user_` | Prefix of the keys of the user hashes. |
| `TIMEVAULT_REDIS_TIMEOUT` | `3s` | Connect, read and write timeout for Redis, `100ms` to `30s`. |
| `TIMEVAULT_ADMIN_USER` | `admin` | Name of the optional local administrator. |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | none (optional) | Output of `timevault-server hash-password`. Creates a local administrator who can sign in even when Redis is down; the name is never looked up in Redis. Without it the service needs `TIMEVAULT_REDIS_URL` (unless sign-in is disabled). Secret. |
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

2. Copy [configs/timevault.example.env](../configs/timevault.example.env) to `.env` next to `timevault-server.exe`, replace every `<placeholder>` and delete the lines you do not need. Signing in needs `TIMEVAULT_REDIS_URL` (company accounts; check it with `timevault-server check-redis-users`) and/or a local administrator: `timevault-server hash-password` creates the value for `TIMEVAULT_ADMIN_PASSWORD_HASH`.
3. Run `timevault-server migrate` (or set `TIMEVAULT_AUTO_MIGRATE=true`) and `timevault-server serve`. Open `http://127.0.0.1:8660/`; an administrator adds the monitored folders on the **Settings** page.
4. To let other computers use it, put an HTTPS reverse proxy in front, listen on loopback, and set `TIMEVAULT_COOKIE_SECURE=true`. Listening on a non-loopback address directly needs `TIMEVAULT_ALLOW_NON_LOOPBACK=true` and still has no TLS. Anyone who can reach the address can browse the file names, sizes and history of every monitored folder without an account, and company passwords are typed into the site, so HTTPS is not optional. The sign-in limits count the address that connects, so behind a reverse proxy they count the proxy's address (see [security.md](./security.md)).
5. The service has no Windows service wrapper. Start it with a method you trust (for example a scheduled task or a service manager); this was not tested. It stops cleanly on Ctrl+C or SIGTERM, waiting up to 30 seconds for the HTTP server and running jobs.

At start the service marks jobs that were running when the previous process ended as failed ("interrupted by a service restart"), removes the partial history of unfinished recovery points, and then resumes queued jobs.

## Tests

`go test ./...` runs everything. Tests that need PostgreSQL are skipped unless `TIMEVAULT_TEST_DATABASE_URL` points at a database; each test creates a private schema named `tvtest_<random>` and drops it, so use a separate test database such as `timevault_test` from `dev-db.ps1`, never the working database. If `data\pgdev\db.env` points at your own PostgreSQL server instead of the private cluster, create the test database there first (`CREATE DATABASE timevault_test OWNER timevault;`); the role needs no other rights. Tests that exercise junctions and Windows file locking run only on Windows. Everything live tests are described in [Everything search](#everything-search). The Redis tests need no Redis: they use an in-process fake with pickle data written by Python.

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

- **Who may do what.** Anyone can browse (Explorer, Time Machine, file versions, the state of the Backups page and of backup jobs) without signing in. Everything else needs an active account: backing up, verifying, cancelling a job, restoring, the restore jobs, why a job failed and its log, who asked for it, the audit log, the status page, the settings page. Only an `admin` account (Redis `Type` is `admin`, or the local administrator) can add, edit, switch off and schedule monitored folders. Visitors do not see the folders' paths on the server's disk. See [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md) and [api.md](./api.md#who-may-call-what).
- **Health.** `GET /healthz` returns `ok` without signing in and only proves the process is up. The **Status** page (accounts only) checks the database, the repository (a write test and free space; "degraded" below 5% free), the search engine, the Redis user directory ("degraded" when it does not answer: browsing, existing sessions and the local administrator still work), the restore folder and the job queue, and lists VSS and USN as "disabled". Without a session only `/healthz`, the sign-in page, the language switch, the static assets and the browsing pages work.
- **Monitored folders.** An administrator adds them on the **Settings** page (a local folder; it must not overlap the repository, the restore folder or another monitored folder) and can edit the name, the exclude patterns, the on/off switch and the schedule at any time. The folder itself can be corrected until the first recovery point exists (in any state, even a failed one); after that, switch the old one off and add the new one. A switched-off folder keeps its history and is still browsable; it cannot be backed up.
- **Backups.** Any signed-in account can press **Back up now** in **Backups**. A normal backup trusts size and modification time to skip unchanged files; **Full re-read** reads and hashes every file again. Only one backup per source runs at a time. A backup that cannot read a file (locked, changed while reading, link, special file) records it as skipped with the reason, keeps the previous good version, and still completes with a warning.
- **Automatic backups.** Tick "Back up automatically every N minutes" for a folder (1 to 43200). The service looks every 15 seconds; a folder is due when it never ran or its last run ended N minutes ago, whatever the outcome, and a run that finds nothing changed records nothing: no recovery point, no job, no audit event. Setting a schedule, or switching a folder on again, starts a backup at once when the last run is older than the interval. There is no retention yet, so a short interval on a folder that keeps changing grows the repository without bound. See [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md).
- **Verify.** The verify job on a recovery point checks the catalog against the manifest digest and that every referenced object exists with the right size; "deep" also re-hashes the content. Run it after copying or restoring the repository.
- **Restore.**
  1. Sign in, open **Time Machine**, choose a source and a recovery point, then tick files or folders and choose **Restore selected…**, or choose **Restore this folder…** / **Restore everything…**. The version page of a single file has **Restore…**. Visitors who are not signed in see a **Sign in to restore files** link instead.
  2. Review the preview: counts, size, conflicts, blocked items (missing content, unsafe names, never captured) and the planned changes.
  3. Choose the destination folder name (a folder below `TIMEVAULT_RESTORE_ROOT`, created if it does not exist), the conflict policy (skip, rename, overwrite) and, if you accept restoring only part of the selection, the partial option. Overwriting needs an extra confirmation.
  4. **Start restore** within 15 minutes of the preview (the token expires and is bound to your user and the exact plan; any change needs a new preview). Follow the job page. Do not start another restore into the same folder until it has finished. Copy the result from `<TIMEVAULT_RESTORE_ROOT>\<destination>` to its final place yourself. Restores never write into a source.
- **Audit.** The **Audit** page (accounts only) lists sign-ins and failed sign-ins (with the account name as it was typed, cut to 64 characters), refused requests, changes to monitored folders (the old and new value of every field that changed), backups, verifies, restore previews and results, and cancellations. The application has no way to change or delete these records, and a database trigger rejects UPDATE and DELETE statements on them. Automatic runs that find nothing changed leave no record.
- **Backing up TimeVault itself.** Back up both the PostgreSQL database (for example with `pg_dump`) and the whole repository folder. The repository only grows, so an older database copy with a newer repository is consistent, but a newer database with an older repository reports missing objects (verify shows them and restores block). After restoring either, run a deep verify.

## Troubleshooting

| Symptom | Cause and action |
|---|---|
| The service stops with "is not a loopback address" | Use `127.0.0.1` behind a reverse proxy, or set `TIMEVAULT_ALLOW_NON_LOOPBACK=true` knowingly. |
| The service stops with a schema message | Run `timevault-server migrate`, or set `TIMEVAULT_AUTO_MIGRATE=true`. |
| The service stops with "no way to sign in" | Set `TIMEVAULT_REDIS_URL` (company accounts) and/or `TIMEVAULT_ADMIN_PASSWORD_HASH` (a local administrator; create the value with `timevault-server hash-password`). |
| The service stops with an error about `.env` | The message names the file and the line number, never the line. `TIMEVAULT_ENV_FILE` must name an existing file. |
| The service stops with "another TimeVault service is already running against this database" | A second `timevault-server` uses the same database (schema). Stop the other one first; its lock is released when its process ends. |
| The service stops with "not a TimeVault repository" | `TIMEVAULT_REPOSITORY_PATH` points at a non-empty folder without `timevault-repository.json`. Use an empty folder. |
| `go` is not found by the scripts | Add Go to `PATH`, or put the line `TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>` into `data\local.env`. Setting the variable in one terminal only lasts for that terminal. |
| `dev-db.ps1` cannot find PostgreSQL | Set `TIMEVAULT_PG_BIN` or pass `-PgBin`. |
| Sign-in answers 429 | Too many attempts: 5 per address or 10 failures per account name within 10 minutes (`Retry-After` says how long). Behind a reverse proxy all visitors share the proxy's address. Wait, or restart the service (the counters are in memory). |
| Sign-in says the user accounts cannot be reached (503) | Redis does not answer. Browsing, sessions that already exist and the local administrator keep working. Check `TIMEVAULT_REDIS_URL` and run `timevault-server check-redis-users`; the **Status** page shows the directory as "degraded". Attempts made meanwhile count for the address, not against the account. |
| A company account cannot sign in | Wrong password, account not activated, no password stored, or the login matches more than one account (employee ID, name or e-mail address; the service log says so without naming anyone). Run `check-redis-users`: unreadable records, records without a password and employee IDs on more than one key are counted there. Try the employee ID, which is the least ambiguous. |
| `check-redis-users` reports unreadable records | A hash that the decoder cannot read (a field is missing or has an unexpected type). The report gives the position of the key among all keys sorted by name, so it can be found with `redis-cli`. Such a record cannot sign in. |
| Settings says "Only an administrator can change the monitored folders" | The account's `Type` in Redis is not `admin`. A change of role applies within a minute. |
| Back up now answers that the monitored folder is switched off (409) | An administrator has to switch it on in Settings. |
| Moving a folder answers 409 or says it already has backups | A backup of that folder is running (try again when it has finished), or the folder has a recovery point in some state and can no longer be changed: switch it off and add the new folder. |
| An automatic backup does not run | The folder is switched off or has no schedule; or its last run ended less than the interval ago (the Backups and Settings pages show the next time); or the service is not running. The scheduler looks every 15 seconds. |
| The job I was watching says "not found" | An automatic backup that finds nothing changed leaves no job behind. |
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

介绍如何安装、配置、运行、运维和排查 TimeVault（0.1.1 版）。它描述的是目前实际存在的内容：服务是一个 Go 程序 `timevault-server`，需要 PostgreSQL 和一个仓库文件夹；公司账号登录还需要另一个应用的 Redis 用户目录。目前没有安装程序，也没有 Windows 服务封装。

## 前置条件

| 项目 | 说明 |
|---|---|
| 带 NTFS 的 Windows | 在 Windows 11 Pro 上开发和测试。服务也能在其他系统上编译（部分路径规则和测试仅适用于 Windows），但只在 Windows 上实际运行过。 |
| Go 1.25 或更新版本 | 构建所需（`go.mod` 声明了 `go 1.25.0`）；恢复代码使用了 `os.Root`。可以使用便携版 Go：把 `TIMEVAULT_GO_ROOT` 设为 Go 文件夹（包含 `bin\go.exe` 的那个），开发脚本会把它加入 `PATH`。若要在不同终端间长期生效、又不修改系统 `PATH`，请把 `TIMEVAULT_GO_ROOT=<Go 文件夹>` 这一行写入 `data\local.env`（已被 git 忽略；需自行创建）。 |
| PostgreSQL | 只测试过 16.4。所用 SQL 至少需要 PostgreSQL 11（identity 列、`hashtextextended`），但没有试过低于 16 的版本。 |
| Redis（可选） | 另一个应用（dash）的用户目录：名为 `app_user_<ee_code>_<name>_<email>` 的哈希。没有它时只有本地管理员可以登录。测试使用的是进程内的模拟服务和 Python 生成的 pickle 数据，没有连接过公司自己的 Redis：请在它上面运行一次 `timevault-server check-redis-users`。 |
| Everything 1.4.x（可选） | 仅用于搜索。没有它时搜索显示"未配置"，其他功能正常。见 [Everything 搜索](#everything-搜索)。 |

## 开发环境快速开始

脚本把所有内容放在被 git 忽略的 `data\` 文件夹下，不会触碰已有的 PostgreSQL 服务，也不会打印任何机密信息。如果 PowerShell 拦截脚本，请用 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1` 运行。如果 Go 不在 `PATH` 中，请先创建 `data\local.env`，写入 `TIMEVAULT_GO_ROOT=<包含 bin\go.exe 的文件夹>` 这一行；否则 `dev-run.ps1` 会立即以"The go command was not found"退出。

1. `scripts\dev-db.ps1 start`（首次）创建并启动一个私有 PostgreSQL 集群，位于 `data\pgdev`，监听 `127.0.0.1:55432`，包含数据库 `timevault` 和 `timevault_test`。随机密码保存在 `data\pgdev\db.env`。它需要已安装的 PostgreSQL 二进制文件（用 `TIMEVAULT_PG_BIN` 或 `-PgBin` 指定位置）。其他动作：`stop`、`status`、`init`、`reset`（会删除该集群）。**也可以跳过这一步，改用你已有的 PostgreSQL 服务器**：自己创建好数据库和角色后，修改 `data\pgdev\db.env`，让 `TIMEVAULT_DATABASE_URL`（以及测试用的 `TIMEVAULT_TEST_DATABASE_URL`）指向它。这样脚本不会启动私有集群，并且 `dev-db.ps1` 会拒绝覆盖或删除指向其他服务器的 `db.env`。
2. `scripts\dev-run.ps1` 在数据库设置指向私有集群（端口 55432）时启动它，否则认为你配置的服务器已在运行；它会构建 `data\dev\bin\timevault-server.exe`，首次运行时创建 `data\dev`（示例备份源文件夹、仓库、恢复目录、`server.env`，以及保存在 `data\dev\admin-password.txt` 的本地管理员 `admin` 的随机密码）和仓库文件夹下的 `.env`（服务自己的设置文件，带注释，已被 git 忽略）。然后在前台运行服务，地址为 `http://127.0.0.1:8660/`（用 `-Port` 修改端口；不带该参数时使用 `.env` 中的 `TIMEVAULT_LISTEN_ADDR`），并打印是否已配置 Redis 用户目录。按 Ctrl+C 停止。
3. `scripts\dev-demo.ps1`（服务运行时）把 `data\dev\source` 登记为监控文件夹"Demo files"，并通过 API 创建三个恢复点，同时留下一些未备份的改动，使文件浏览页能显示已变更、新增和已删除的项目。
4. 打开该地址。任何人都可以浏览。要备份、恢复或修改设置，请用 `admin` 和 `data\dev\admin-password.txt` 中的密码登录；或者在 `.env` 中写入 `TIMEVAULT_REDIS_URL` 后使用公司账号登录（在仓库文件夹中运行 `data\dev\bin\timevault-server.exe check-redis-users` 检查）。页眉有语言切换（English / 中文）和浅色/深色主题切换按钮（在使用该按钮之前，页面跟随系统设置；之后浏览器会记住你的选择）。

5. 可选，用于搜索框：`scripts\dev-everything.ps1 -Action start`，然后重启服务（见 [Everything 搜索](#everything-搜索)）。

`. .\scripts\dev-env.ps1` 会把同样的设置加载到你自己的 PowerShell 会话中（`data\pgdev\db.env`、`data\dev\server.env`、`data\everything\everything.env` 和 `data\local.env`，各自在存在时加载），这样可以手动运行 `go test` 或 `timevault-server`；设置了 `TIMEVAULT_GO_ROOT` 时，它也会把 Go 加入该会话的 `PATH`。

## 命令

| 命令 | 用途 |
|---|---|
| `timevault-server serve` | 运行服务（不带命令时的默认行为）。 |
| `timevault-server migrate` | 应用数据库迁移后退出。 |
| `timevault-server hash-password` | 读取密码（在终端中输入时不回显，或从标准输入读取一行），并输出可用于 `TIMEVAULT_ADMIN_PASSWORD_HASH`（可选的本地管理员）的值。 |
| `timevault-server check-redis-users` | 连接 Redis 用户目录（`TIMEVAULT_REDIS_URL`），并输出其中有多少可以使用：键的数量、可读和不可读的记录、已启用的账号、类型为 `admin` 和 `user` 的数量、没有密码的账号、出现在多个键上的工号，以及每个字段有多少值可以解码。它不输出任何姓名、邮箱、键或密码。Redis 没有应答或没有任何可读记录时退出码为 1。请在包含 `.env` 的文件夹中运行，或设置 `TIMEVAULT_ENV_FILE`。 |
| `timevault-server version` | 输出版本号。 |

构建：`go build -o timevault-server.exe ./cmd/timevault-server`。Web 模板、静态资源和迁移脚本都已嵌入可执行文件。

## 设置放在哪里

| 内容 | 位置 | 由谁修改 | 何时生效 |
|---|---|---|---|
| 部署设置：数据库 URL、Redis URL、监听地址、备份文件夹（`TIMEVAULT_REPOSITORY_PATH`）、恢复文件夹（`TIMEVAULT_RESTORE_ROOT`）、会话密钥、搜索、worker、日志 | 进程环境变量和/或 `.env` 文件 | 安装服务的人，通过编辑文件 | 重启之后 |
| 监控文件夹：名称、文件夹、排除规则、启用或停用、自动备份计划 | 数据库（表 `sources`） | 管理员，在**设置**页或通过 `PATCH /api/v1/sources/{id}` | 立即 |
| 账号和角色 | Redis（`app_user_*`），由另一个应用写入 | 那个应用 | 对已经登录的会话，在一分钟之内 |

- `.env` 文件是 `TIMEVAULT_ENV_FILE` 所指的文件（它必须存在），否则是工作目录下的 `.env`，否则是 `timevault-server.exe` 旁边的 `.env`。使用找到的第一个；不会合并多个文件。**进程环境变量中已设置的变量优先于文件中的同名键。**开发时 `scripts\dev-run.ps1` 会先把 `data\pgdev\db.env`、`data\dev\server.env`、`data\everything\everything.env` 和 `data\local.env` 加载到环境变量中，所以在那里设置的键不能在 `.env` 中更改。
- 语法：每行一个 `KEY=VALUE`。空行和以 `#` 开头的行会被忽略，允许行首的 `export `，接受 UTF-8 字节顺序标记和 Windows 行尾。值按字面一直取到行尾，所以带 `\` 的路径或带 `#` 的密码无需转义（行尾的 ` # 注释` 会成为值的一部分）；值两侧成对的单引号或双引号会被去掉；同一个键以最后一次赋值为准。出错时只指出行号，绝不显示该行内容。
- 该文件包含机密信息（数据库和 Redis 密码、会话密钥）。`/.env` 已在 `.gitignore` 中；请让它只能被运行服务的账户读取，并且绝不要提交填好的副本。[configs/timevault.example.env](../configs/timevault.example.env) 是模板。
- **设置**页（任何已登录账号都可以打开）以只读方式显示监控文件夹、备份文件夹和恢复文件夹（含仓库的剩余空间），以及当前运行的配置：版本、监听地址、数据库和 Redis（显示为 `host:port/name`）、设置文件、本地管理员的名称、搜索服务器。机密信息绝不显示。只有管理员能看到修改监控文件夹的表单。

## 配置参考

每个设置都是一个环境变量，或 `.env` 文件中的一行（见[设置放在哪里](#设置放在哪里)；环境变量优先）。无效的值会使程序退出并给出点名该变量的提示。机密信息绝不会写入日志。

| 变量 | 默认值 | 含义 |
|---|---|---|
| `TIMEVAULT_ENV_FILE` | 无 | 要读取的设置文件路径，用来代替查找 `.env`。该文件必须存在。只能通过进程环境变量设置。 |
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8660` | 监听地址。除非设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，否则会拒绝非回环地址。 |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | 确认由 TLS 反向代理保护非回环监听。服务在监听本机之外时，启动时会记录一条警告。 |
| `TIMEVAULT_DATABASE_URL` | 无（必填） | PostgreSQL 连接 URL。机密。 |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | 启动时应用迁移。为 `false` 时，如果数据库结构不是最新，服务会拒绝启动；请先运行 `migrate`。 |
| `TIMEVAULT_REPOSITORY_PATH` | 无（必填） | 备份文件夹（仓库）。新文件夹或空文件夹会被初始化；不是 TimeVault 仓库的非空文件夹会被拒绝。修改后需要重启。 |
| `TIMEVAULT_RESTORE_ROOT` | 空 | 恢复写入的位置（每次恢复一个文件夹，需要时创建）。为空表示禁用恢复。不得位于仓库之内，也不得包含仓库。 |
| `TIMEVAULT_AUTH_DISABLED` | `false` | 关闭登录：每个人都是本地管理员。只在回环监听时被接受。仅用于开发。 |
| `TIMEVAULT_REDIS_URL` | 空 | Redis 用户目录，`redis://[:password@]host:port/db`（TLS 用 `rediss://`）。为空表示没有公司账号。机密。 |
| `TIMEVAULT_REDIS_KEY_PREFIX` | `app_user_` | 用户哈希的键前缀。 |
| `TIMEVAULT_REDIS_TIMEOUT` | `3s` | Redis 的连接、读取和写入超时，范围 `100ms` 到 `30s`。 |
| `TIMEVAULT_ADMIN_USER` | `admin` | 可选的本地管理员的名称。 |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | 无（可选） | `timevault-server hash-password` 的输出。创建一个即使 Redis 不可用也能登录的本地管理员；它的名字永远不会到 Redis 中查找。没有它时，服务需要 `TIMEVAULT_REDIS_URL`（除非关闭了登录）。机密。 |
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

2. 把 [configs/timevault.example.env](../configs/timevault.example.env) 复制为 `timevault-server.exe` 旁边的 `.env`，替换每个 `<placeholder>`，并删除不需要的行。登录需要 `TIMEVAULT_REDIS_URL`（公司账号；用 `timevault-server check-redis-users` 检查）和/或一个本地管理员：`timevault-server hash-password` 会生成 `TIMEVAULT_ADMIN_PASSWORD_HASH` 的值。
3. 运行 `timevault-server migrate`（或设置 `TIMEVAULT_AUTO_MIGRATE=true`）和 `timevault-server serve`。打开 `http://127.0.0.1:8660/`；由管理员在**设置**页添加监控文件夹。
4. 如需让其他计算机使用，请在前面放置 HTTPS 反向代理，让服务监听回环地址，并设置 `TIMEVAULT_COOKIE_SECURE=true`。直接监听非回环地址需要 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，并且依然没有 TLS。能访问该地址的任何人都可以不用账号浏览每个监控文件夹的文件名、大小和历史，公司密码也是在这个网站上输入的，所以 HTTPS 不是可选项。登录限流统计的是连接过来的地址，所以放在反向代理之后时统计的是代理的地址（见 [security.md](./security.md)）。
5. 服务没有 Windows 服务封装。请用你信任的方式启动它（例如计划任务或服务管理器）；这一点没有测试过。收到 Ctrl+C 或 SIGTERM 时它会正常退出，最多等待 30 秒让 HTTP 服务和正在运行的任务结束。

启动时，服务会把上次进程结束时仍在运行的任务标记为失败（"interrupted by a service restart"），清除未完成恢复点的部分历史，然后继续处理排队的任务。

## 测试

`go test ./...` 运行全部测试。需要 PostgreSQL 的测试在 `TIMEVAULT_TEST_DATABASE_URL` 未指向某个数据库时会被跳过；每个测试都会创建名为 `tvtest_<random>` 的私有 schema 并在结束后删除，所以请使用单独的测试数据库，例如 `dev-db.ps1` 创建的 `timevault_test`，不要使用正式使用的数据库。如果 `data\pgdev\db.env` 指向的是你自己的 PostgreSQL 服务器而不是私有集群，请先在那里创建测试数据库（`CREATE DATABASE timevault_test OWNER timevault;`）；该角色不需要其他权限。涉及联接点和 Windows 文件锁的测试只在 Windows 上运行。Everything 在线测试见 [Everything 搜索](#everything-搜索)。Redis 测试不需要 Redis：它们使用进程内的模拟服务和 Python 生成的 pickle 数据。

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

- **谁可以做什么。** 任何人无需登录就可以浏览（文件浏览、时间机器、文件版本、备份页和备份任务的状态）。其他一切都需要已启用的账号：备份、校验、取消任务、恢复、恢复任务、任务失败的原因和日志、任务是谁发起的、审计日志、状态页、设置页。只有 `admin` 账号（Redis 中 `Type` 为 `admin`，或本地管理员）才能添加、编辑、停用监控文件夹并设置它们的计划。访问者看不到文件夹在服务器磁盘上的路径。见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md) 和 [api.md](./api.md#谁可以调用什么)。
- **健康检查。** `GET /healthz` 无需登录即返回 `ok`，只能证明进程在运行。**状态**页（仅限账号）检查数据库、仓库（写入测试和剩余空间；剩余不足 5% 时为"降级"）、搜索引擎、Redis 用户目录（没有应答时为"降级"：浏览、已有会话和本地管理员仍可使用）、恢复目录和任务队列，并把 VSS 和 USN 列为"已禁用"。没有会话时，只有 `/healthz`、登录页、语言切换、静态资源和浏览页面可以访问。
- **监控文件夹。** 管理员在**设置**页添加（本地文件夹；不能与仓库、恢复目录或其他监控文件夹重叠），并且可以随时编辑名称、排除规则、启停开关和计划。文件夹本身在第一个恢复点出现（任何状态，包括失败的）之前可以更正；之后请停用旧的并添加新的。停用的文件夹保留历史，仍可浏览，但不能被备份。
- **备份。** 任何已登录的账号都可以在**备份**页点击**立即备份**。普通备份依据大小和修改时间跳过未变化的文件；**完整重读**会重新读取并哈希每个文件。每个备份源同一时间只运行一个备份。无法读取的文件（被锁定、读取时发生变化、链接、特殊文件）会被记录为"跳过"并写明原因，保留之前的良好版本，备份本身仍以警告完成。
- **自动备份。** 为文件夹勾选"自动备份，间隔 N 分钟"（1 到 43200）。服务每 15 秒查看一次；文件夹从未运行过，或上次运行结束已满 N 分钟（无论结果如何）时到期，而没有发现变化的运行不记录任何内容：没有恢复点、没有任务、没有审计事件。设置计划或重新启用文件夹时，如果上次运行早于间隔，会立即启动备份。目前没有保留策略，所以对一直在变化的文件夹使用很短的间隔，会让仓库无限增长。见 [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md)。
- **校验。** 对恢复点运行校验任务，会对照清单摘要检查目录库，并检查每个被引用的对象都存在且大小正确；"深度"校验还会重新哈希内容。复制或恢复仓库之后请运行一次。
- **恢复。**
  1. 登录后打开**时间机器**，选择备份源和恢复点，然后勾选文件或文件夹并选择**恢复所选项…**，或选择**恢复此文件夹…** / **恢复全部内容…**。单个文件的版本页面有**恢复…**。未登录的访问者看到的是一个**登录后可恢复文件**的链接。
  2. 查看预览：数量、大小、冲突、受阻项目（内容缺失、不安全的名称、从未备份成功）以及计划的变更。
  3. 选择目标文件夹名称（`TIMEVAULT_RESTORE_ROOT` 之下的文件夹，不存在时会创建）、冲突策略（跳过、改名、覆盖），以及在接受只恢复所选内容的一部分时勾选的部分恢复选项。覆盖需要额外确认。
  4. 在预览后 15 分钟内点击**开始恢复**（令牌会过期，并与你的用户和精确的计划绑定；任何更改都需要重新预览）。在任务页面查看进度。在它结束之前，不要向同一个文件夹再启动另一个恢复。请自行把结果从 `<TIMEVAULT_RESTORE_ROOT>\<destination>` 复制到最终位置。恢复绝不会写入备份源。
- **审计。** **审计**页（仅限账号）列出登录和登录失败（账号名按输入的内容记录，截断到 64 个字符）、被拒绝的请求、监控文件夹的变更（每个发生变化的字段的旧值和新值）、备份、校验、恢复预览与结果以及取消操作。应用程序没有修改或删除这些记录的途径，并且数据库触发器会拒绝对它们的 UPDATE 和 DELETE 语句。没有发现变化的自动运行不留下记录。
- **备份 TimeVault 自身。** 需要同时备份 PostgreSQL 数据库（例如用 `pg_dump`）和整个仓库文件夹。仓库只会增长，所以较旧的数据库副本配上较新的仓库是一致的，而较新的数据库配上较旧的仓库会报告对象缺失（校验会列出，恢复会被阻止）。无论恢复了哪一个，之后都请运行一次深度校验。

## 故障排查

| 现象 | 原因与处理 |
|---|---|
| 服务以"is not a loopback address"退出 | 在反向代理之后使用 `127.0.0.1`，或在明确知晓风险的情况下设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`。 |
| 服务以数据库结构相关的提示退出 | 运行 `timevault-server migrate`，或设置 `TIMEVAULT_AUTO_MIGRATE=true`。 |
| 服务以"no way to sign in"退出 | 设置 `TIMEVAULT_REDIS_URL`（公司账号）和/或 `TIMEVAULT_ADMIN_PASSWORD_HASH`（本地管理员；用 `timevault-server hash-password` 生成该值）。 |
| 服务以与 `.env` 有关的错误退出 | 提示会指出文件和行号，绝不会显示该行内容。`TIMEVAULT_ENV_FILE` 必须指向一个存在的文件。 |
| 服务以"another TimeVault service is already running against this database"退出 | 另一个 `timevault-server` 正在使用同一个数据库（schema）。请先停止那一个；它的锁会在其进程结束时释放。 |
| 服务以"not a TimeVault repository"退出 | `TIMEVAULT_REPOSITORY_PATH` 指向的非空文件夹中没有 `timevault-repository.json`。请使用空文件夹。 |
| 脚本找不到 `go` | 把 Go 加入 `PATH`，或把 `TIMEVAULT_GO_ROOT=<包含 bin\go.exe 的文件夹>` 这一行写入 `data\local.env`。只在某一个终端里设置该变量，仅对那个终端有效。 |
| `dev-db.ps1` 找不到 PostgreSQL | 设置 `TIMEVAULT_PG_BIN` 或传入 `-PgBin`。 |
| 登录返回 429 | 尝试次数过多：10 分钟内每个地址 5 次，或每个账号名 10 次失败（`Retry-After` 说明要等多久）。放在反向代理之后时，所有访问者共用代理的地址。请等待，或重启服务（计数保存在内存中）。 |
| 登录提示无法连接用户账号服务（503） | Redis 没有应答。浏览、已有的会话和本地管理员仍可使用。请检查 `TIMEVAULT_REDIS_URL` 并运行 `timevault-server check-redis-users`；**状态**页会把该目录显示为"降级"。这期间的尝试计入地址，不会算到账号头上。 |
| 公司账号无法登录 | 密码错误、账号未启用、没有保存密码，或登录名匹配到多个账号（工号、姓名或邮箱；服务日志会说明这一点，但不会点名任何人）。请运行 `check-redis-users`：不可读的记录、没有密码的记录和出现在多个键上的工号都会在那里计数。可以试试工号，它是歧义最小的。 |
| `check-redis-users` 报告有不可读的记录 | 有解码器无法读取的哈希（缺少字段或字段类型出乎意料）。报告会给出该键在所有按名称排序的键中的位置，可据此用 `redis-cli` 找到它。这样的记录无法登录。 |
| 设置页显示"只有管理员可以修改监控文件夹" | 该账号在 Redis 中的 `Type` 不是 `admin`。角色变化在一分钟内生效。 |
| 点击立即备份提示监控文件夹已停用（409） | 需要管理员在设置页重新启用它。 |
| 迁移文件夹时返回 409，或提示它已有备份 | 该文件夹的备份正在运行（请在它结束后再试），或者该文件夹已有某种状态的恢复点，不能再更改：请停用它并添加新的文件夹。 |
| 自动备份没有运行 | 文件夹已停用或没有设置计划；或者它上次运行结束距今不到一个间隔（备份页和设置页会显示下次时间）；或者服务没有运行。调度器每 15 秒查看一次。 |
| 我正在查看的任务显示"未找到" | 没有发现变化的自动备份不会留下任务。 |
| "Restore is not enabled" | `TIMEVAULT_RESTORE_ROOT` 为空。 |
| 恢复预览被判定为过期或失效 | 重新预览；令牌有效期 15 分钟，并与精确的计划绑定。在没有设置 `TIMEVAULT_SESSION_KEY` 的情况下重启，密钥也会变化。 |
| 文件被列为跳过（被锁定） | 文件被其他程序打开，或在读取时发生变化。请稍后再次备份；MVP 没有 VSS。 |
| 恢复报告对象缺失或损坏 | 仓库丢失或损坏了文件。请运行深度校验，从备份恢复仓库，或选择其他恢复点。不会从损坏的对象恢复任何内容。 |
| 中断的恢复留下了 `.tvpart-*` 文件 | 这是崩溃的恢复在目标文件夹中留下的临时文件，可以删除。 |
| 搜索显示"未配置"或"不可用" | 见 [Everything 搜索](#everything-搜索)。历史和恢复仍可使用。 |
| 任务一直显示"failed: interrupted by a service restart" | 任务运行时服务被停止。请重新启动该任务；未完成的恢复点已经被清理。 |
