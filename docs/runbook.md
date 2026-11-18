# English

## Overview

How to set up, configure, run, operate and troubleshoot TimeVault (version 0.1.8). It describes what exists today; the service is a single Go program, `timevault-server`, that needs PostgreSQL and a repository folder, and, for company sign-in, the Redis user directory of another application. There is no installer and no Windows service wrapper yet.

## Prerequisites

| Item | Notes |
|---|---|
| Windows with NTFS | Developed and tested on Windows 11 Pro. The server also compiles on other systems (some path rules and tests are Windows specific), but only Windows was exercised. |
| Go 1.25 or newer | Needed to build (`go.mod` declares `go 1.25.0`); the restore code uses `os.Root`. A portable Go works: put the line `TIMEVAULT_GO_ROOT=<Go folder>` (the folder that contains `bin\go.exe`) into `.env` and the dev scripts add it to `PATH` in every terminal, without touching the system `PATH`. |
| PostgreSQL | Tested with 16.4 only. The SQL needs at least PostgreSQL 11 (identity columns, `hashtextextended`), but older versions than 16 were not tried. |
| Redis (optional) | The user directory of another (dash) application: hashes named `app_user_<ee_code>_<name>_<email>`. Without it only the local administrator can sign in. Tested against an in-process fake and pickle data written by Python, not against the company's own Redis: run `timevault-server check-redis-users` on it once. |
| Everything 1.4.x (optional) | Only for search. Without it search shows "not configured" and everything else works. See [Everything search](#everything-search). |

## Quick start for development

The scripts keep their data under the git-ignored `data\` folder and **all settings in one git-ignored file, `.env` in the repository folder** (see [Where settings live](#where-settings-live)). They never touch an existing PostgreSQL service and print no secrets. If PowerShell blocks the scripts, run them as `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1`. If Go is not on your `PATH`, first put the line `TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>` into `.env`; without it `dev-run.ps1` stops at once with "The go command was not found". Earlier versions kept settings in four more files (`data\local.env`, `data\pgdev\db.env`, `data\dev\server.env`, `data\everything\everything.env`); the first time a script runs it moves their settings into `.env` and deletes them.

1. `scripts\dev-db.ps1 start` creates (first time) and starts a private PostgreSQL cluster in `data\pgdev` on `127.0.0.1:55432` with the databases `timevault` and `timevault_test`. The random password is written into `.env` (`TIMEVAULT_DATABASE_URL` and `TIMEVAULT_TEST_DATABASE_URL`). It needs the installed PostgreSQL binaries (`TIMEVAULT_PG_BIN` in `.env` or `-PgBin` to point at them). Other actions: `stop`, `status`, `init`, `reset` (deletes the cluster). **You can skip this step and use a PostgreSQL server you already have**: create the database and the role yourself, then put `TIMEVAULT_DATABASE_URL` (and `TIMEVAULT_TEST_DATABASE_URL`, for the tests) into `.env`. The scripts then start no private cluster, and `dev-db.ps1` refuses to create or reset one while `.env` points at another server.
2. `scripts\dev-run.ps1` creates `.env` (commented, git-ignored) when it is missing; starts the private database when `TIMEVAULT_DATABASE_URL` points at it (port 55432) and otherwise expects the server you configured to be running; builds `data\dev\bin\timevault-server.exe`; and adds to `.env` what the service needs and `.env` does not have yet: a repository and a restore folder under `data\dev`, `TIMEVAULT_AUTO_MIGRATE=true`, a session key and, on the very first run, a random password for the local administrator `admin` (the password goes to `data\dev\admin-password.txt`, only its hash to `.env`). **It removes `TIMEVAULT_*` variables that are left in your terminal** before it starts the server, because they would override `.env`, and it names them. It then runs the server in the foreground on `http://127.0.0.1:8660/` (`-Port` changes the port for that run; without it `TIMEVAULT_LISTEN_ADDR` from `.env` applies) and prints whether the Redis user directory is configured. Stop it with Ctrl+C.
3. `scripts\dev-demo.ps1` (with the server running) registers `data\dev\source` as the monitored folder "Demo files" and creates three recovery points through the API, leaving some uncommitted changes so the Explorer shows changed, new and deleted items. It takes the address from `TIMEVAULT_LISTEN_ADDR` in `.env`; after `dev-run.ps1 -Port` pass `-BaseUrl http://127.0.0.1:<port>`.
4. Open the address. Anyone can browse. To back up, restore or change settings, sign in as `admin` with the password from `data\dev\admin-password.txt`, or with a company account after you put `TIMEVAULT_REDIS_URL` into `.env` (check it with `data\dev\bin\timevault-server.exe check-redis-users`, run from the repository folder). The header has the language switch (English / 中文) and a light/dark theme toggle (the page follows the system setting until you use the toggle; the browser then remembers your choice).

5. Optional, for the search box: `scripts\dev-everything.ps1 -Action start`, then restart the server (see [Everything search](#everything-search)).

`. .\scripts\dev-env.ps1` prepares your own PowerShell session for `go test` and for running `timevault-server` by hand. It removes `TIMEVAULT_*` settings that are left in the session (they would override `.env`), sets `TIMEVAULT_ENV_FILE` to the `.env` of the repository, loads what only tests and tools read (`TIMEVAULT_TEST_*`, `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN`, `TIMEVAULT_EVERYTHING_EXE`) and puts Go on the `PATH` of that session. The settings of the service are not copied into the environment: the service reads `.env` itself.

## Commands

| Command | Purpose |
|---|---|
| `timevault-server serve` | Run the service (the default when no command is given). |
| `timevault-server migrate` | Apply the database migrations and exit. |
| `timevault-server hash-password` | Read a password (hidden when typed in a terminal, or one line from standard input) and print the value for `TIMEVAULT_ADMIN_PASSWORD_HASH` (the optional local administrator). |
| `timevault-server check-redis-users` | Connect to the Redis user directory (`TIMEVAULT_REDIS_URL`) and print how much of it can be used: the number of keys, readable and unreadable records, activated accounts, types `admin` and `user`, accounts without a password, employee IDs that appear on more than one key, and per field how many values could be decoded. It prints no name, e-mail address, key or password. Exit code 1 when Redis does not answer or no record is readable. Run it from the folder that holds `.env`, or set `TIMEVAULT_ENV_FILE`. |
| `timevault-server version` | Print the version. |
| `timevault-server check-config` | Check that the settings (the environment and the `.env` file) are valid for the service, print `the settings are valid` or the reasons they are not (exit code 1). It connects to nothing. A service that updates itself runs it with the new program before it replaces itself (see [Releasing and automatic updates](#releasing-and-automatic-updates)). |

## Building the executable

`timevault-server.exe` is the only file the service needs besides its `.env`: the web templates, the assets and the database migrations are embedded in it. The program is the package `./cmd/timevault-server`; the repository root is not a program (`go build .` fails with "no Go files").

```powershell
cd <repository folder>
go build -trimpath -ldflags "-s -w" -o timevault-server.exe ./cmd/timevault-server
.\timevault-server.exe version
```

`go build -o timevault-server.exe ./cmd/timevault-server` is enough. `-trimpath -ldflags "-s -w"` only makes the file smaller (about 22 MB instead of 31 MB). Go 1.25 or newer is needed (see `go.mod`).

**The version number** is written in one place, `var version` in `cmd/timevault-server/main.go`, and the build takes it from there, so the command above always gives the version of the sources it is run on. The `version` command, the start-up log, the footer of every page, the Settings page and `GET /api/v1/settings` show it. Run `.\timevault-server.exe version` after every build. Do not add `-X main.version=<number>` to the command: it replaces the number in `main.go`, so a copy of the command that was written for an older version keeps stamping the old number. A build of the 0.1.5 sources with `-X main.version=0.1.4` has all the features of 0.1.5 but calls itself 0.1.4.

**Dependencies and the `vendor\` folder.** The build needs the Go modules listed in `go.mod`, either from the module cache (`go env GOMODCACHE`, which Go fills from the module proxy) or from a `vendor\` folder next to `go.mod`. While a `vendor\` folder exists, Go uses it automatically (the same as `-mod=vendor`; `go build`, `go vet` and `go test` all do). That suits a server without internet access: run `go mod vendor` once on a computer that has the modules (it writes about 600 files, 7 MB), and copy the `vendor\` folder together with the sources.

- `vendor\` is not part of the repository, so a clone of the repository does not have it. Copy it with the sources, or build on the computer that has it.
- `-mod=vendor` without a `vendor\` folder fails with `go: inconsistent vendoring ... is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt`. The same message appears when `vendor\` is older than `go.mod` or `go.sum`. Run `go mod vendor` again (do it after every change of the dependencies), or build from the module cache with `-mod=readonly`, which ignores `vendor\`.
- The simplest way to deploy to a server without internet access is to build `timevault-server.exe` on the development computer and copy that file, with the `.env`, to the server.
- A server that updates itself does not need that copy more than once: `scripts\release.ps1` builds the release folder that such servers read (see [Releasing and automatic updates](#releasing-and-automatic-updates)).

## Where settings live

| What | Where | Changed by | Takes effect |
|---|---|---|---|
| Deployment: database URL, Redis URL, listen address, backup folder (`TIMEVAULT_REPOSITORY_PATH`), restore folder (`TIMEVAULT_RESTORE_ROOT`), this server's host name (`TIMEVAULT_SERVER_HOST`), the restore folder's display path (`TIMEVAULT_RESTORE_DISPLAY_ROOT`), session key, search, workers, logging | The `.env` file (a process environment variable overrides it) | Whoever installs the service, by editing the file | After a restart |
| Monitored folders: name, display folder, folder on the server, exclude patterns, on or off, automatic schedule, versions kept per file | The database (table `sources`) | Administrators add folders and can change everything about them; whoever has Full on a folder changes its settings, but not the folder on the server, on the **Settings** page or with `PATCH /api/v1/sources/{id}` | At once |
| Who may do what with each folder (Browse, Back up, Full; granted to guests, to everybody who is signed in or to single employee IDs) | The database (table `source_access`) | Administrators, and whoever has Full on the folder, in the permissions part of the folder's form on the **Settings** page or with `access` in the API (see [Folder permissions](#folder-permissions)) | At once, on the next request of every visitor |
| Accounts and roles | Redis (`app_user_*`), written by another application | That application | Within a minute for a session that is already signed in |

- The `.env` file is the one named by `TIMEVAULT_ENV_FILE` (it must exist), otherwise `.env` in the working directory, otherwise `.env` next to `timevault-server.exe`. The first one found is used; files are not merged. In development it is the `.env` in the repository folder, and it is the only settings file: the dev scripts read and write it, and `dev-run.ps1` points the service at it.
- **A `TIMEVAULT_*` variable in the process environment wins over the same key in the file**, and nothing tells you unless you look: a variable left in a terminal makes the service ignore what you wrote in `.env` (typically a port that does not change). The service therefore names every setting that the environment overrides in a warning when it starts (`check-redis-users` prints them too), and the dev scripts remove such variables from the terminal. If the warning keeps coming back, the variable is set in your Windows user or system environment.
- Syntax: one `KEY=VALUE` per line. Blank lines and lines that start with `#` are ignored, a leading `export ` is allowed, and a UTF-8 byte order mark and Windows line ends are accepted. The value is taken literally up to the end of the line, so a path with `\` or a password with `#` needs no escaping (and a trailing ` # comment` would become part of the value); one pair of matching single or double quotes around the value is removed; the last assignment of a key wins. An error names the line number and never shows the line.
- The file holds secrets (the database and Redis passwords, the session key). `/.env` is in `.gitignore`; keep the file readable only by the account that runs the service, and never commit a filled-in copy. [configs/timevault.example.env](../configs/timevault.example.env) is the template.
- The **Settings** page (any signed-in account may open it) shows the monitored folders the visitor may browse, each with what the visitor may do with it, the backup storage and the restore folder with the free space of the repository, and the running configuration: version, this server's host name, listen address, database and Redis as `host:port/name`, the single sign-on service, the local administrator's name, the search server, and the state of the automatic update (see [Releasing and automatic updates](#releasing-and-automatic-updates): the address of the release and the last problem are only for administrators, who can also press **Check now**). Secrets are never shown. The form that adds a monitored folder is for administrators; the form that changes one is for administrators and for whoever has Full on that folder (without the folder on the server's disk, which only administrators see and change). Only administrators see the folders on the server's disk (the real folder of each monitored folder, the backup folder, the real restore folder and the settings file); everybody else sees the display folders.
- **Backup folder and restore folder.** They are two different places. The backup folder (`TIMEVAULT_REPOSITORY_PATH`, the repository) is where TimeVault keeps the contents of everything it backed up. The restore folder (`TIMEVAULT_RESTORE_ROOT`) is where restored files are put: a restore never overwrites the original files and never writes into a monitored folder, so it writes the restored copies into a new folder below the restore folder (named on the restore form, by default `restore-<date>-<time>`), and you copy back what you need from there. Neither may lie inside a monitored folder.
- **Display folders.** People reach the monitored folders and the restore folder over the network, not as `D:\TimeVault\monitor`. Every monitored folder therefore has a **display folder**, the network path people open it with (for `D:\TimeVault\monitor` shared as `TimeVault`: `\\sgmeslt595.co.murata.local\TimeVault\monitor`), and the restore folder has `TIMEVAULT_RESTORE_DISPLAY_ROOT` (`\\sgmeslt595.co.murata.local\TimeVault\restore`). Set `TIMEVAULT_SERVER_HOST` to the name of this server (`sgmeslt595.co.murata.local`): then the Settings page and the API refuse a monitored folder without a display folder, and one whose display folder is on another server (its search, backup and restore only work with this server's own folders). Without `TIMEVAULT_SERVER_HOST` a display folder is optional and is not compared with a host. Administrators fill the field on the Settings page, which also marks a monitored folder that has none ("No display folder yet"; after setting the host name every folder that was added before needs one before it can be edited again). The service checks the host, not that the share really leads to the monitored folder: share the folder first, then write the path you open it with. Where a display folder is shown, the Explorer, the Time Machine and the page of a file's versions have a **Location** line with a **Copy** button.
- **Who sees which folder.** Administrators see both the display folder and the real folder. Everybody else, signed in or not, sees only the display folder, and texts that quote a real folder (job errors and logs, restore messages, audit details, status details, the reasons a file was not captured) show the display folder, `[<name of the monitored folder>]`, `[backup folder]` or `[restore folder]` instead. See [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md).

## Configuration reference

Every setting is an environment variable or a line of the `.env` file (see [Where settings live](#where-settings-live); the environment wins). Invalid values stop the program with a message that names the variable. Secrets are never logged.

| Variable | Default | Meaning |
|---|---|---|
| `TIMEVAULT_ENV_FILE` | none | Path of the settings file to read instead of looking for `.env`. It must exist. Only the process environment can set it. |
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8660` | Address to listen on. A non-loopback address is refused unless `TIMEVAULT_ALLOW_NON_LOOPBACK=true`. |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | Confirms that a TLS reverse proxy protects a non-loopback listener. The service logs a warning at start when it listens beyond this computer. |
| `TIMEVAULT_DATABASE_URL` | none (required) | PostgreSQL connection URL. Secret. |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | Apply migrations at start. When `false` the service refuses to start if the schema is not current; run `migrate` first. |
| `TIMEVAULT_REPOSITORY_PATH` | none (required) | Backup folder (the repository): where the contents of the backed-up files are stored. A new or empty folder is initialised; a non-empty folder that is not a TimeVault repository is refused. Needs a restart to change. |
| `TIMEVAULT_RESTORE_ROOT` | empty | Restore folder. A restore never overwrites the original files and never writes into a monitored folder: it creates a new folder below this one for each restore (named on the restore form) and writes the restored copies there. Empty disables restore. It must not be inside or contain the repository or a monitored folder. |
| `TIMEVAULT_SERVER_HOST` | empty | The name of this server as people type it in network paths, for example `sgmeslt595.co.murata.local`. Letters, digits, `-`, `_` and `.` only. When set, every monitored folder needs a display folder on this host (compared without regard to case), and `TIMEVAULT_RESTORE_DISPLAY_ROOT` must be on it. When empty, display folders are optional and not compared with a host. |
| `TIMEVAULT_RESTORE_DISPLAY_ROOT` | empty | The restore folder as people reach it over the network, `\\server\share\folder` (either slash). Checked at start: it must be a network path, on `TIMEVAULT_SERVER_HOST` when that is set; ignored when there is no restore folder. Without it, ordinary accounts are told only that the restore folder is on the server. |
| `TIMEVAULT_AUTH_DISABLED` | `false` | Turns sign-in off: everyone is the local administrator. Only accepted on a loopback listener. For development only. |
| `TIMEVAULT_REDIS_URL` | empty | The Redis user directory, `redis://[:password@]host:port/db` (`rediss://` for TLS). Empty means no company accounts. Secret. |
| `TIMEVAULT_REDIS_KEY_PREFIX` | `app_user_` | Prefix of the keys of the user hashes. |
| `TIMEVAULT_REDIS_TIMEOUT` | `3s` | Connect, read and write timeout for Redis, `100ms` to `30s`. |
| `TIMEVAULT_ADMIN_USER` | `admin` | Name of the optional local administrator. |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | none (optional) | Output of `timevault-server hash-password`. Creates a local administrator who can sign in even when Redis is down; the name is never looked up in Redis. Without it the service needs `TIMEVAULT_REDIS_URL` (unless sign-in is disabled). Secret. |
| `TIMEVAULT_SESSION_KEY` | random per start | Base64 text that decodes to at least 32 bytes. Set it so sessions and restore confirmations survive a restart. Secret. |
| `TIMEVAULT_SESSION_TTL` | `8h` | Session lifetime, from `5m` to `720h`. |
| `TIMEVAULT_COOKIE_SECURE` | `false` | Adds `Secure` to cookies. Set it to `true` behind HTTPS. |
| `TIMEVAULT_SSO_HOST` | empty | The company sign-on service, as `host`, `host:port` or `http(s)://host[:port]` (the scheme defaults to `http`). When set, the login page offers "Sign in with Windows (SSO)"; see [Single sign-on](#single-sign-on). Needs `TIMEVAULT_REDIS_URL`. |
| `TIMEVAULT_SSO_REQUIRE_SAME_IP` | `true` | The address that the sign-on service saw must be the address of the browser that asked for the sign-in. Turn it off only when the address of the browser cannot be known (`TIMEVAULT_TRUSTED_PROXIES` is the better way). |
| `TIMEVAULT_TRUSTED_PROXIES` | empty | Reverse proxies, as addresses or CIDR ranges separated by commas (`127.0.0.1,::1,10.0.0.0/24`), whose `X-Forwarded-For` header is believed. The client address then comes from that header; it is used by the single sign-on check, the **From** column of the audit log and the sign-in throttle. The proxy must set that header itself; see [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path), and [Two proxies in a row](#two-proxies-in-a-row) when another proxy sits in front of it. |
| `TIMEVAULT_BASE_PATH` | empty | The path prefix the site is served under behind a reverse proxy, such as `/tv` for `http://host/tv/`. Empty means the site is at the root of its address. `tv`, `/tv` and `/tv/` all mean `/tv`; letters, digits and `- _ . ~` only, and it must not start with a path of the application itself (`api`, `static`, `login`, `explorer`, `history`, `versions`, `backups`, `jobs`, `restore`, `audit`, `status`, `settings`, `lang`, `logout`, `auth`, `healthz`). Every address the pages, the redirects and the cookies give out carries it; a request with or without it is accepted. See [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path). |
| `TIMEVAULT_AVATAR_URL` | `/api/static/hr/{id}.webp` | Where the picture of an account is: a path on the host that serves the site (it is not put under `TIMEVAULT_BASE_PATH`), starting with `/`, with `{id}` once where the employee ID goes. `off` or `none` (an empty value means the default) turns the pictures off. See [Pictures of the accounts](#pictures-of-the-accounts). |
| `TIMEVAULT_UPDATE_URL` | empty | Where the release manifest `timevault.json` is: an `http(s)` address, or the absolute path of the file, of its folder or of a share (`D:\TimeVault\release`, `\\server\share\tv-release`); an address that ends in a slash, and a path of a folder, name the folder. No user name, password, query or fragment. Empty means the service never updates itself. Needs `TIMEVAULT_AUTO_MIGRATE=true`. See [Releasing and automatic updates](#releasing-and-automatic-updates). |
| `TIMEVAULT_UPDATE_INTERVAL` | `10m` | How often the manifest is read, `1m` to `24h`. The first look is about 10 seconds after the start; an administrator can ask for one at once (**Check now**). |
| `TIMEVAULT_UPDATE_RESTART` | `supervise` | How the new program is started after an update: `supervise` (the process that was started stays as a small launcher and starts it) or `exit` (the process ends with the exit code 75 and a service manager starts it). |
| `TIMEVAULT_EVERYTHING_URL` | empty | Base URL of the Everything HTTP server, for example `http://127.0.0.1:<port>/`. Empty disables search. |
| `TIMEVAULT_EVERYTHING_USERNAME`, `TIMEVAULT_EVERYTHING_PASSWORD` | empty | Optional HTTP Basic credentials for Everything. The password is a secret. |
| `TIMEVAULT_EVERYTHING_TIMEOUT` | `5s` | Per-search timeout, `100ms` to `30s`. |
| `TIMEVAULT_SCHEDULER_PARALLEL` | `1` | How many automatic backups may be queued or running at the same time (1 to 4). Backups started by hand are not limited. See [Scheduler settings](#scheduler-settings). |
| `TIMEVAULT_SCHEDULER_GAP` | `auto` | The least time between the starts of two automatic backups: `auto`, or a duration from `0s` to `6h` (`0s` turns the spacing off). |
| `TIMEVAULT_SCHEDULER_REST` | `auto` | The pause after an automatic backup before the next one starts, only while `TIMEVAULT_SCHEDULER_PARALLEL` is 1: `auto`, or a duration from `0s` to `6h` (`0s` turns it off). |
| `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` | `false` | Ask Everything whether a folder changed before scanning it, and skip the scan when it did not. Needs `TIMEVAULT_EVERYTHING_URL`. |
| `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY` | `24h` | With the probe on, the longest a folder goes without a complete scan, from `1h` to `720h`. |
| `TIMEVAULT_PROCESS_PRIORITY` | `below_normal` | Priority of the service among the programs of this computer: `normal`, `below_normal` or `background` (which also lowers the disk priority). Windows only. |
| `TIMEVAULT_JOB_WORKERS` | `2` | Jobs that run at the same time (1 to 16). The database connection pool is sized to match (2 × workers + 8, at least 16 connections); PostgreSQL's `max_connections` must allow that. |
| `TIMEVAULT_HASH_WORKERS` | smaller of 4 and the CPU count | Files hashed and copied in parallel inside one backup (1 to 32). |
| `TIMEVAULT_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `TIMEVAULT_LOG_FORMAT` | `text` | `text` or `json`. |

Variables used only by the helper scripts and tests, kept in the same `.env` (the service ignores them): `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN`, `TIMEVAULT_EVERYTHING_EXE`, `TIMEVAULT_TEST_DATABASE_URL` and `TIMEVAULT_TEST_EVERYTHING_*` (see [Tests](#tests)). [configs/timevault.example.env](../configs/timevault.example.env) is a template with placeholders.

## Running outside the dev scripts

Build `timevault-server.exe` first (see [Building the executable](#building-the-executable)).

1. Create a PostgreSQL role and database for TimeVault (use your own password):

   ```sql
   CREATE ROLE timevault LOGIN PASSWORD '<choose-a-password>';
   CREATE DATABASE timevault OWNER timevault;
   ```

2. Copy [configs/timevault.example.env](../configs/timevault.example.env) to `.env` next to `timevault-server.exe`, replace every `<placeholder>` and delete the lines you do not need. Signing in needs `TIMEVAULT_REDIS_URL` (company accounts; check it with `timevault-server check-redis-users`) and/or a local administrator: `timevault-server hash-password` creates the value for `TIMEVAULT_ADMIN_PASSWORD_HASH`.
3. Run `timevault-server migrate` (or set `TIMEVAULT_AUTO_MIGRATE=true`) and `timevault-server serve`. Open `http://127.0.0.1:8660/`; an administrator adds the monitored folders on the **Settings** page.
4. To let other computers use it, put an HTTPS reverse proxy in front, listen on loopback, and set `TIMEVAULT_COOKIE_SECURE=true`. Listening on a non-loopback address directly needs `TIMEVAULT_ALLOW_NON_LOOPBACK=true` and still has no TLS. Anyone who can reach the address can browse the file names, sizes and history of every monitored folder without an account, and company passwords are typed into the site, so HTTPS is not optional. The sign-in limits count the address that connects, so behind a reverse proxy they count the proxy's address unless `TIMEVAULT_TRUSTED_PROXIES` lists the proxy (see [Single sign-on](#single-sign-on) and [security.md](./security.md)). When the proxy serves TimeVault under a path of a host name that other applications share, such as `/tv/`, see [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path).
5. The service has no Windows service wrapper. Start it with a method you trust (for example a scheduled task or a service manager). A scheduled task of a user account (no administrator rights) was tried with 0.1.8: it starts the service, which keeps running, also through an automatic update (see [Releasing and automatic updates](#releasing-and-automatic-updates)); a Windows service wrapper such as NSSM was not tried. Switch off the task setting **Stop the task if it runs longer than** (the Task Scheduler sets it to 3 days for a new task), or the service is stopped when the time is up. It stops cleanly on Ctrl+C or SIGTERM, waiting up to 30 seconds for the HTTP server and running jobs.

At start the service marks jobs that were running when the previous process ended as failed ("interrupted by a service restart"), removes the partial history of unfinished recovery points, and then resumes queued jobs.

## Behind a reverse proxy under a path

Use this when one host name serves several applications and TimeVault is one of them: for example `http://prod_eng.co.murata.local/tv/` next to `/qdb/`, `/mer/` and `/gitea/`. Up to 0.1.4 TimeVault wrote every address with a leading slash (`Location: /explorer`, `/static/...`, the forms, the requests of its script). That is right at the root of a host and wrong under `/tv/`: the first redirect leaves the prefix and ends on whatever the proxy serves at the root of the host (in the first deployment, the portal), and the pages lose their style. The TimeVault log then shows one request, and the browser is on another application. Since 0.1.5 the service knows its prefix; the design is in [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md).

**Turn it on**

1. Put the prefix into `.env`: `TIMEVAULT_BASE_PATH=/tv`, and restart the service. The start-up log says `serving under a path prefix` and the Settings page shows the **Address prefix**. Without the setting nothing changes.
2. In the proxy, send `/tv/` to the service. The service accepts a request with the prefix and without it, so the proxy may pass the address on as it is (recommended, the simplest) or take the prefix off.
3. The proxy next to TimeVault must tell it the address of the browser, and it has to be listed in `TIMEVAULT_TRUSTED_PROXIES` (`127.0.0.1` when the proxy runs on the same computer). The block below is for a proxy that the browser reaches itself; when another proxy sits in front of it, add what [Two proxies in a row](#two-proxies-in-a-row) says. An nginx block that was run against this version:

   ```nginx
   location = /tv {
       return 301 /tv/;
   }

   location ^~ /tv/ {
       # No address after the port: the request goes on as it came, prefix included.
       proxy_pass http://127.0.0.1:8660;

       proxy_http_version 1.1;
       proxy_set_header Connection "";

       # TimeVault compares the Origin of a form post with Host.
       proxy_set_header Host $host;

       # The address of the browser, as this nginx saw it. Never $http_x_forwarded_for here: that passes on whatever
       # the browser sent, and TimeVault believes this proxy, so anyone could choose the address it records.
       proxy_set_header X-Real-IP $remote_addr;
       proxy_set_header X-Forwarded-For $remote_addr;
       proxy_set_header X-Forwarded-Proto $scheme;

       client_max_body_size 2m;   # TimeVault reads at most 1 MiB of a form
       proxy_read_timeout 300s;
   }
   ```

   A block that takes the prefix off before it passes the request on works as well (`rewrite ^ $request_uri;` and `rewrite ^/tv(/.*) $1 break;` with `proxy_pass http://127.0.0.1:8660$uri;`, the usual pattern for Gitea). Both were run against this version. The address headers are the part to copy from the block above: a block written for a second proxy, with `X-Forwarded-For $http_x_forwarded_for`, lets a browser choose the address that TimeVault records.

**Check it.** `curl -I http://<host>/tv` answers `301` with `Location: http://<host>/tv/`. `curl -I http://<host>/tv/` answers `303` with `Location: /tv/explorer` (`/tv/backups` while there is no monitored folder). In a browser the address bar stays under `/tv/` and the pages have their style. The Audit page shows the address of the browser in **From**, not `127.0.0.1`.

**What it covers.** Every link, form and file of the pages; every redirect, also those of the API (the `Location` of `POST /api/v1/sources` is `/tv/api/v1/sources`); the progress polling of a job; single sign-on; and the cookies `tv_session`, `tv_lang` and `tv_flash`, which are limited to `/tv`, so the other applications of the host name do not receive them. The API is at `/tv/api/v1/...`.

**Things to know**

- The service can still be opened on its own address: `http://127.0.0.1:8660/` leads to `/tv/...`, and the pages stay under the prefix there too. An address typed there without the prefix shows its page, but the browser does not send the session cookie to it, so you appear signed out until you use the prefix.
- The prefix is part of the addresses people bookmark and send. Changing it later breaks those.
- TimeVault shares its origin (scheme, host and port) with the other applications of that host name. A host name or a port of its own is the stronger setup; see [security.md](./security.md).

## Two proxies in a row

Use this when another nginx sits in front of the one that serves TimeVault. In the first deployment the server `s2` (`http://10.135.36.81/tv/`) forwards `/tv/` to the nginx of `prod_eng.co.murata.local`, which forwards it to TimeVault: browser → s2 → prod_eng → TimeVault. The decision and the alternatives are in the update of [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md).

**What goes wrong.** The nginx next to TimeVault sees the address of the nginx in front of it, not the address of the browser. With `proxy_set_header X-Forwarded-For $remote_addr;` it writes that address into the header, so TimeVault takes the front server for the browser. Single sign-on compares that address with the one the sign-on service recorded, and the browser opened the sign-on service directly, so the service recorded the real address. Every single sign-in then ends with "The sign-in could not be matched to this browser" (`address_mismatch`). It was reproduced on 2026-11-17 with the real nginx 1.24 and the `/tv/` blocks of both servers. Through the nginx next to TimeVault alone (`http://prod_eng.co.murata.local/tv/`) nothing is hidden, so it works there.

**Set it up.** The front nginx keeps the block of [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path) (`X-Forwarded-For $remote_addr`, so a browser cannot choose the address). The nginx next to TimeVault has to take the address of the browser from the header that the front nginx sends, and from nobody else's. Add the three `real_ip` lines to its `/tv/` block, with the address that the front nginx connects from, and leave the rest of the block as it is:

```nginx
location ^~ /tv/ {
    client_max_body_size 512M;

    # The nginx in front: one line for each. Its X-Forwarded-For is believed, anybody else's is not.
    set_real_ip_from 10.135.36.81;
    real_ip_header X-Forwarded-For;
    real_ip_recursive on;

    # ... the rest of the block, as before (X-Forwarded-For $remote_addr included)
}
```

For a request that comes from the front nginx, nginx now makes `$remote_addr` the address of the browser; a request from anybody else keeps the address it connects from, and the header that it sends is ignored. `TIMEVAULT_TRUSTED_PROXIES` stays as it is (`127.0.0.1`, the nginx next to TimeVault). Reload nginx; TimeVault does not need a restart. The address after `set_real_ip_from` is the one the front nginx connects from, as the nginx next to TimeVault sees it (its access log shows it, and so does `started_from` of a refused single sign-on); it is not necessarily the address people type.

**Another way.** In the nginx next to TimeVault append instead of replace (`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`), put both proxies into `.env` (`TIMEVAULT_TRUSTED_PROXIES=127.0.0.1,10.135.36.81`) and restart TimeVault. TimeVault takes the right-most address of the header that is not a listed proxy, so what a browser writes on the left is not believed. The `real_ip` way keeps `.env` as it is and makes the access log of that nginx show the browser.

Both ways were run against this version, through the front nginx and directly, with and without a forged `X-Forwarded-For`: TimeVault was told the address of the browser every time.

**Check it.** Open TimeVault through the front server and sign in: the **From** column of the Audit page shows the address of the browser, not that of a server. When a single sign-on is refused because of the addresses, the **Sign-in failed** entry and a warning in the service log say what TimeVault saw: `started_from` (the address it took for the browser), `sso_address` (the one the sign-on service recorded), `connection` (the address that connected: the nearest proxy), `trusted_proxy` (whether that address is listed in `TIMEVAULT_TRUSTED_PROXIES`) and `forwarded` (the `X-Forwarded-For` header, cut to 256 characters). If `started_from` is the address of a server, a proxy in front hides the browser: set it up as above. If `trusted_proxy` is `false` and `forwarded` is set, the proxy that connects is not in `TIMEVAULT_TRUSTED_PROXIES`. A browser that reaches the front server over IPv4 and the sign-on service over IPv6 (or the reverse) is also seen with two addresses; the two values show it.

**Not verified.** The company's own servers: the steps were run on one computer with a copy of the two `/tv/` blocks and loopback addresses for the servers.

## Tests

`go test ./...` runs everything. Tests that need PostgreSQL are skipped unless `TIMEVAULT_TEST_DATABASE_URL` points at a database; each test creates a private schema named `tvtest_<random>` and drops it, so use a separate test database such as `timevault_test`, never the working database. `dev-db.ps1` writes the URL for the private cluster into `.env`; if `.env` points at your own PostgreSQL server instead, create the test database there first (`CREATE DATABASE timevault_test OWNER timevault;`) and set `TIMEVAULT_TEST_DATABASE_URL` in `.env`; the role needs no other rights. Tests that exercise junctions and Windows file locking run only on Windows. Everything live tests are described in [Everything search](#everything-search). The Redis tests need no Redis: they use an in-process fake with pickle data written by Python.

```powershell
. .\scripts\dev-env.ps1      # loads TIMEVAULT_TEST_DATABASE_URL (and the other TIMEVAULT_TEST_* settings) from .env
go test ./... -count=1
```

## Folder permissions

What an account may do is decided for each monitored folder, not for the whole server. The decision and the alternatives are in [ADR 0013](./adr/0013-per-folder-access-levels.md).

**Levels.** Each level includes the ones before it.

| Level (label on the page) | May |
|---|---|
| Browse | See the folder: files, history, versions, search, the state of its backups, its jobs, the audit events about it. Restore from it (needs a signed-in account; the copies go below the restore folder). |
| Back up | Also: **Back up now**, **Full re-read**, **Verify** a recovery point, and stop the jobs of the folder. |
| Full control | Also: change the folder's settings (name, display folder, exclude patterns, on or off, schedule, versions kept) and who may do what with it. Not the folder on the server and not adding folders: those are for administrators. |

**Who a level is given to.** To **Guests (not signed in)** (only Browse), to **Everyone who is signed in**, and to single people by employee ID. A signed-in account has the highest level that any of these gives it, and never less than a guest has. Administrators (`admin` accounts and the local administrator) have full control of every folder without being listed. Whoever started a restore can also stop it.

**Set them.**

1. An administrator adds a folder on the **Settings** page (**Add a folder**). The form has a **Who may do what** part with three lists: Browse, Back up and Full control. Tick **Guests (not signed in)** and/or **Everyone who is signed in** where they belong, or tick people. People are listed by name and employee ID (the accounts of the user directory; type in the box to find one; ticked people come first). A person who is in more than one list gets the highest level. A folder that is added without ticking anybody is seen by administrators only.
2. To change it later, open **Settings**, press **Edit** on the folder (only folders on which you have full control have the button) and change the lists; press **Save changes**. The change applies to the next request of every visitor.
3. Rules the form enforces: guests only in the Browse list; "Guests" and "Everyone who is signed in" cannot be combined with each other or with named people in one list (they already include them); every named employee ID has to be an active account of the user directory when it is added (an account that already has access stays even if it has been deactivated since or the directory is down; the form shows it as "not in the directory"); at most 500 named people per folder. Somebody who has full control but is not an administrator, and who saves lists that no longer give them full control, is first asked to tick **I understand that I will no longer be able to change this folder (an administrator can give the access back)** (in the API: `409 lockout_not_confirmed` until `confirm_lockout` is `true`). If somebody else changed who may do what with the folder while the form was open and you changed the lists too, nothing is saved: the form comes back saying **Somebody else changed who may do what with this folder while you were editing it**, with the lists as they are now and with what else you typed; check them, make your change again and save (in the API: `409 access_changed` when `access_rev` is sent with `access`). A form in which you did not touch the lists never changes them, so saving only a schedule keeps what the other person did. Enter in the box that finds a person does not save the form.
4. The same through the API: `access` in `POST /api/v1/sources` (administrators) and `PATCH /api/v1/sources/{id}` (full control), see [api.md](./api.md). A person is written as the employee ID, or `user:<employee ID>` (needed when the ID is spelled `guest` or `everyone`).

**What a visitor without any level sees: nothing.** The folder is not in the Explorer or Time Machine selector, on the Backups, Settings or Audit pages, or in the API; its jobs and audit events are not shown; asking for it directly gives the same "not found" as for a folder that does not exist (a visitor who has not signed in is told that signing in may show it). A visitor who can see a folder but may not do something gets "You are not allowed to do this", and the attempt is in the audit log as **Access denied**, with the folder and the level it needed (`needs`).

**Examples.**

| Wanted | Browse | Back up | Full control |
|---|---|---|---|
| Anybody may look, signed-in people back up (what every folder allowed until 0.1.6) | Guests | Everyone who is signed in | |
| A team folder that the company may look at, the team manages | Everyone who is signed in | the team members | the manager |
| A private folder | the people who may see it | | the manager |

**Upgrade to 0.1.7.** Apply migration `0005` with `timevault-server migrate` (or start the service with `TIMEVAULT_AUTO_MIGRATE=true`). It gives every folder that exists the first row of the table (guests browse, everybody who is signed in may back up), which is what everybody could do until 0.1.6: nothing changes for existing folders until an administrator narrows them. A folder that is added afterwards has no access until somebody is given it. **Do not go back to 0.1.6 against that database:** 0.1.6 does not know the table, so it would let every visitor see every folder and every signed-in account back up and restore them again; if you have to go back, put the database from before the upgrade back.

**Check it.** Sign in with another account (or look at the folder list as a guest): the Explorer and Backups pages list exactly the folders it may browse, and the Settings page shows the level next to each name. `GET /api/v1/sources` has `level` for each folder. The Audit page shows every change of the lists: **Monitored folder changed** with `access` (old and new); only those who have full control of the folder read the lists, for everybody else they are shown as `[hidden]`. If a person cannot see a folder they should, check that their employee ID is in a list (or **Everyone who is signed in** is ticked) and that their account is active; a visitor who has not signed in only has what **Guests** give.

## Single sign-on

Optional. A person who is signed in to Windows at the company can sign in to TimeVault without typing a password, through the same company sign-on service that the dash application uses. The design and its limits are in [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md).

**Turn it on**

1. Put the address of the sign-on service into `.env`: `TIMEVAULT_SSO_HOST=<sso.example.local>` (`host`, `host:port` or `http(s)://host[:port]`). It is one line because the address may change. Restart the service. The login page now has the button **Sign in with Windows (SSO)**, and the start-up log and the Settings page show the sign-on service (`sso_host`). Without the setting the login page is as before.
2. The sign-on service has to write its results (`sso_login_<token>`) to the Redis that `TIMEVAULT_REDIS_URL` points at (the same database number), and the Redis account of TimeVault has to be allowed to read them. TimeVault only reads them; it never deletes the key, and the sign-on service lets it expire after 5 minutes.
3. Behind a reverse proxy (the usual case) choose one of two ways:
   - **Recommended.** List the proxies in `TIMEVAULT_TRUSTED_PROXIES` (addresses or CIDR ranges separated by commas). The proxy has to put the address of the browser into the `X-Forwarded-For` header, and to pass the `Host` header unchanged, because the check that a request comes from the site itself compares `Origin` with `Host`. TimeVault then also writes the real address into the **From** column of the audit log and counts sign-in attempts per browser instead of per proxy. A tested nginx block is in [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path).
   - Set `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`. The address of the browser is then not compared with the one that the sign-on service saw. This is weaker; see ADR 0009.

   With neither, every single sign-in ends with "The sign-in could not be matched to this browser", because TimeVault compares the address of the proxy with the address of the browser.

   The same happens on a development PC that opens TimeVault directly (for example `http://localhost:8660`): TimeVault sees a loopback address (`127.0.0.1` or `::1`), while the sign-on service sees the network address of the PC. Open the site through the proxy address instead, or set `TIMEVAULT_SSO_REQUIRE_SAME_IP=false` on that PC only.

**What a person sees.** The button opens a small window with the sign-on page, and the login page says "Waiting for the Windows sign-in". After a few seconds the window closes itself and the person is signed in, with the role of the account in Redis (`admin` or not), and lands on the page they came from. When something goes wrong the page says what: the browser blocked the window (allow pop-ups for the site), Windows could not tell who you are, the Windows account has no active account here, the sign-in could not be matched to this browser, the sign-in took too long or was already used, the window was closed, or the user accounts cannot be reached. The password form keeps working.

**Check it.** Open the login page and press the button. The **Audit** page then shows **Signed in** with `method: sso` and the Windows user; a refused attempt shows **Sign-in failed** with a `reason` (`no_active_account`, `no_windows_user`, `address_mismatch`, `browser_address_changed` or `result_unreadable`). For the two reasons about the addresses it also shows the addresses that were compared and how the request reached the server (`started_from`, `sso_address`, `connection`, `trusted_proxy`, `forwarded`; see [Two proxies in a row](#two-proxies-in-a-row)), and the service log has a warning with the same.

## Everything search

Search is optional. TimeVault asks a **local Everything instance** (the HTTP server of Everything 1.4) which files and folders exist below a registered source *right now*. It is not a history search: browsing the past uses the catalog. Without Everything the search box shows "not configured" or "unavailable" and everything else keeps working.

**Set it up**

1. Use Everything 1.4.x with its HTTP server enabled, bound to `127.0.0.1` only (the adapter refuses any other host), protected with a user name and password, and with "Allow file download" switched **off** (`http_server_allow_file_download` is on by default and TimeVault does not need it). A dedicated named instance is better than your everyday one; for development `scripts\dev-everything.ps1` creates one.
2. Set `TIMEVAULT_EVERYTHING_URL` (for example `http://127.0.0.1:<port>/`) and, if you set credentials, `TIMEVAULT_EVERYTHING_USERNAME` and `TIMEVAULT_EVERYTHING_PASSWORD`. Make sure Everything indexes the source folders.
3. Open **Explorer** and type in the search box. The **Status** page shows the search engine as `ok`, `degraded` (reachable but answering wrongly or slowly) or `disabled`.

**Turn it on for an Everything you already run.** Search stays "not configured" until `TIMEVAULT_EVERYTHING_URL` is set, and an Everything that is running does not answer over HTTP until its HTTP server is switched on (the default is off), so both have to be done:

1. In Everything open **Tools → Options → HTTP Server** (the same settings are the `http_server_*` keys of `Everything.ini`): tick **Enable HTTP server**, choose a port that is free (for example `38480`), bind it to `127.0.0.1` only (`http_server_bindings`), set a user name and a password, and switch **Allow file download** off (`http_server_allow_file_download=0`). Apply. Everything has to keep running, and it has to index the drive of the monitored folders (an NTFS volume, or the folders themselves).
2. Put these lines into `.env` (use your own port, user name and password):

   ```env
   TIMEVAULT_EVERYTHING_URL=http://127.0.0.1:38480/
   TIMEVAULT_EVERYTHING_USERNAME=<username>
   TIMEVAULT_EVERYTHING_PASSWORD=<password>
   ```

3. Restart the TimeVault service, open the **Status** page (the search engine should be `ok`) and search in **Explorer**. If it says "unavailable", open `http://127.0.0.1:38480/` in a browser: it must ask for the user name and password and show Everything's page.

**What happens to a query**

- The typed text is split into terms (whitespace separates them, double quotes make a phrase; at most 256 bytes and 16 terms). `*` and `?` act as separators because Everything keeps treating them as wildcards even inside quotes.
- Every term is put in double quotes, so Everything operators such as `|`, `!`, `<>`, `regex:`, `size:` or `content:` are searched as plain text.
- The query is confined to the source with `path:"<source root>\"`. The trailing backslash matters: `path:` is a plain substring match, so without it a source `D:\src` would also match `D:\src2`. `infolder:` was not used because it only matches direct children.
- Every result is checked again against the source root. Anything outside is dropped and counted, never shown.
- Pages hold at most 200 results (50, 100 or 200 can be chosen) with page links; they are not sorted or filtered by type, because Everything answers one page at a time in its own order. Folder sizes are shown as unknown when Everything does not index them.

**Folder sizes in the Explorer** (0.1.4). The Explorer asks Everything for the folders inside the folder on screen and shows their size (everything inside, recursively), so a folder row is no longer empty in the size column and folders can be sorted by size. The modified time of a folder is its own, read from the disk, as before. In Everything turn on **Tools → Options → Indexes → Index folder size** (and keep the volume monitored); without it the size cells show "-". The answer is the index of Everything, which can lag a little behind the disk. TimeVault keeps an answer for 10 seconds, waits at most 2 seconds for it and, when Everything does not answer, shows "-" and does not ask again for 15 seconds, so a slow or stopped Everything never slows the page down much. The Time Machine and the versions page read the catalog and show no folder sizes. The same Everything can also tell the scheduler that a folder did not change; see [Scheduler settings](#scheduler-settings).

**Development instance** (`scripts\dev-everything.ps1`, needs Everything 1.4.x installed; point `TIMEVAULT_EVERYTHING_EXE` (environment or `.env`) at `Everything.exe` if it is not found):

| Action | Effect |
|---|---|
| `-Action start` | Starts the named instance `TimeVault` from a private copy of `Everything.exe` in `data\everything\bin`, with its own ini and database in `data\everything`. It indexes only `-IndexRoot` (default `data\dev\source`) as a folder index, listens on `127.0.0.1:38480` (`-Port`), has file download off, shows no tray icon and keeps no history. Random credentials are kept in `data\everything\http-credentials.txt`; the settings for the server (`TIMEVAULT_EVERYTHING_*`) and for the live tests (`TIMEVAULT_TEST_EVERYTHING_*`) are written into `.env`. |
| `-Action stop`, `status`, `reset` | Stop it (only its own process), show whether it answers, or stop and delete its ini and database and remove its settings from `.env`. |
| `-Action fixture` | Creates the test tree used by the live tests under `-IndexRoot`. |

The script never touches your normal Everything instance and never writes into the Everything installation folder. It was tested from a normal (not elevated) shell. After `start`, restart the TimeVault server so it picks up the new settings in `.env`.

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

## Scheduler settings

Automatic backups are set per folder on the **Settings** page ("Back up automatically every N minutes"). The settings below only decide how the backups that are due are spread out, so that a server with, say, 10 monitored folders does not scan them all at once. The reasons are in [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md).

- **One at a time.** `TIMEVAULT_SCHEDULER_PARALLEL=1` (1 to 4). A backup started with **Back up now** is not limited and does not count.
- **Gap.** `TIMEVAULT_SCHEDULER_GAP=auto` is the gap that just keeps up with all the schedules, `1 / Σ(1 / interval)`, within 30 seconds and 10 minutes: ten folders with an interval of 60 minutes start one every 6 minutes. A duration such as `5m` fixes it; `0s` turns the spacing off.
- **Rest.** `TIMEVAULT_SCHEDULER_REST=auto` waits after a run for as long as it took (30 seconds to 10 minutes), so the long scan of a big folder is followed by a long pause. The next start is at the later of the gap after the previous start and the rest after the previous end. `0s` turns it off. It only applies while `TIMEVAULT_SCHEDULER_PARALLEL` is 1.
- **Order.** Folders without a recovery point first, then the most overdue in proportion to its own interval, then the one whose last run was the quickest, then the lowest id. How long the last run took is shown on the Settings page ("Last run took").
- **Priority.** `TIMEVAULT_PROCESS_PRIORITY=below_normal` is the default, so that backup scans give way to the file shares of the same computer. `background` also lowers the disk priority; `normal` is how it was before 0.1.4.
- **Everything probe.** `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE=true` makes the scheduler ask Everything first and skip the scan of a folder that did not change. It needs `TIMEVAULT_EVERYTHING_URL`, an Everything that monitors the volume of the folder (an NTFS volume, through its change journal) with **Tools → Options → Indexes → Index recently changed date** on, and the folder inside that index. A folder is scanned anyway when Everything does not answer within 3 seconds or does not know the folder, when the number of entries below it is not the number at the last complete scan (this is how a deletion shows), when anything below it was created, modified, renamed, moved or had its attributes changed since 5 minutes before that scan, when the last recovery point has items that were not captured, when the exclude patterns of the folder were changed since that scan, and at least every `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY` (24 hours). A tick asks Everything at most 25 times, and an Everything that does not answer is left alone for a minute, so a stopped Everything costs one timeout and not one for every due folder. With `TIMEVAULT_LOG_LEVEL=debug` the log says "scheduled scan skipped: the search engine sees no change" for every skip. A skipped folder leaves nothing in the lists, like any run that finds nothing changed, and its interval starts again.
- The pace is kept in memory: after a restart the first automatic backup may start at once.

## Releasing and automatic updates

A server whose `.env` has `TIMEVAULT_UPDATE_URL` replaces its own program when a newer release is published: it waits for the jobs that run, swaps the file and restarts, without anybody at the server. The decisions and the alternatives are in [ADR 0014](./adr/0014-self-update-from-a-release-manifest.md).

**Make a release (on the build computer).**

```powershell
cd <repository folder>
.\scripts\release.ps1
.\scripts\release.ps1 -OutDir D:\TimeVault\release -BaseUrl https://tools.example.local/tv-release/
```

- Raise `var version` in `cmd\timevault-server\main.go` first. A service updates when the version in the manifest is **not** the version it runs, so a program with the version of the one before it is no update (the script warns when it sees that).
- The script runs `go vet ./...` (skip it with `-SkipChecks`), builds the program (`-trimpath -ldflags "-s -w"`, no `-X`) into a temporary name, asks it for its version, measures and hashes it, puts it in the release folder (default `<repository>\release`, git-ignored) as `timevault.exe`, and writes `timevault.json` **last**, atomically, as UTF-8 without a byte order mark:

  ```json
  {
      "latest_version":  "0.1.8",
      "sha256":  "<64 hexadecimal characters>",
      "package_url":  "timevault.exe",
      "size":  23352320
  }
  ```

  `package_url` is the file name, which a service looks up next to the manifest it read (right for a folder, a share, or a web folder that holds both files), or, with `-BaseUrl`, the full address of the program. A hand-written manifest works too: a leading `v` in the version and unknown fields are accepted.
- Copy `timevault.exe`, then `timevault.json`, into the **release folder** that the servers read. A service that reads the folder while it is being copied sees a size or a hash that does not match and tries again at the next look; it never installs a half-copied file.

**Where the servers read it.** `TIMEVAULT_UPDATE_URL` is one of:

- a folder or a share, `D:\TimeVault\release` or `\\server\share\tv-release` (the account that runs the service needs read access to it; a share that cannot be reached when the service starts is found as soon as it can be); or
- an `http(s)` address of the manifest or of its folder, `https://tools.example.local/tv-release/`, served by any web server that serves the two files as they are, for example nginx: `location /tv-release/ { alias D:/TimeVault/release/; }`. Redirects are followed (at most five). An answer other than `200` (a `202` or a `404`, say) is an error.

**Turn it on at a server (once).**

1. Copy `timevault.exe` of version 0.1.8 or later to the server, next to the `.env`. This is the last manual copy: older versions cannot update themselves.
2. Put into the `.env`: `TIMEVAULT_AUTO_MIGRATE=true` (a service with an update address refuses to start without it, because nobody runs `migrate` when the service updates itself), `TIMEVAULT_UPDATE_URL=<the release folder>` and, if you want, `TIMEVAULT_UPDATE_INTERVAL=5m`.
3. The account that runs the service needs **Modify** on the folder of `timevault.exe` (it writes `timevault.new.exe`, `timevault.old-<version>.exe` and `timevault.exe.update.json` there) and **Read** on the release folder.
4. Start the service. The start-up log says `automatic updates are on` with the address and the interval, and warns when the folder of the program cannot take a new file. The **Settings** page has a card **Program updates**: the state, the released version, when the manifest was last read, and, for administrators, the address of the release, the last problem and **Check now**.

**What happens when a release is published.**

1. At the next look (within the interval, at once when an administrator presses **Check now** or calls `POST /api/v1/update/check`, and 10 seconds after the start) the service finds a version that is not the running one. It downloads the program to `timevault.new.exe`, checks its size and sha256, and runs it with `version` (it must print the version of the manifest) and `check-config` (it must accept the settings of this service).
2. It stops taking jobs: the Settings page says **Waiting for the running jobs** with their number, and every signed-in account sees a notice under the header (_Version 0.1.8 will be installed as soon as the running jobs have finished. New jobs wait until then._). The jobs that run finish; new ones, also those of the scheduler, wait in the queue. There is no time limit.
3. With no job running, it renames the running `timevault.exe` to `timevault.old-<version>.exe`, puts the new file at the name `timevault.exe`, writes the note, stops the scheduler, the web server and the job runner, and starts the new program. The queued jobs run then.
4. The new program logs `automatic update: this program has taken over` and writes the audit event; the **Audit** page lists the **Program update** events of the actor `updater` (`staged`, `restart`, `done`, and `cancelled` or `failed`), with no path and no error text in the details (the log of the service has them).

If the release goes back to the version that runs, the waiting ends and the jobs are taken again. A problem (a manifest that cannot be read, a program with the wrong size, hash or version, settings that the new program refuses, a swap that fails) is shown on the Settings page (the text only to administrators), logged once, and tried again at the next look; the service goes on with the old program. A wrong size or hash is tried again too, because it is what a release that is still being copied looks like; a program whose version is not the one of the manifest is not tried again until the manifest changes. A download of an `http(s)` address that receives no data for two minutes is given up, and a program that was removed after the download (a virus scanner, say) is downloaded again, both at the next look.

**How the program is started after an update (`TIMEVAULT_UPDATE_RESTART`).**

- `supervise` (default): the process that was started stays alive as a small **launcher** and runs the new program as its child, with the same arguments, environment, folder and console. In a console window Ctrl+C stops both; as a scheduled task the task keeps one running process and needs no "restart on failure". When the launcher ends in any other way, for example when the task is ended (the scheduler kills the process at once), the program notices that the launcher is gone, shuts down in order and frees the port, so ending the task leaves nothing behind. The launcher is the first program that was started and keeps its code: an update replaces the file on disk, not the launcher, so a change in the launcher itself needs a restart of the launcher. While it lives, the file it was started from (`timevault.old-<version>.exe`) stays locked.
- `exit`: the process ends with the exit code **75**, and whatever started it has to start it again. Use it when a service manager runs the program, for example NSSM with `AppExit 75 Restart` (not tried: NSSM was not available for the tests). With the Windows Task Scheduler, a task whose trigger repeats every minute with **Do not start a new instance** (`MultipleInstances IgnoreNew`) starts the new program at the next repetition, and a repetition while the program runs starts nothing: this was tried. The setting **If the task fails, restart every …** was tried too, and it did **not** start the program again after it ended with 75, so do not rely on it. A program that was started by a launcher always exits with 75, whatever the setting says, because the launcher waits for that code.

**Going back.** The previous program is `timevault.old-<version>.exe` (the newest three are kept). **Put the older release in the release folder first, or empty `TIMEVAULT_UPDATE_URL`:** otherwise the service updates itself to the newer one again at the next look, because the version in the manifest is not the one that runs.

- If the new program runs, publishing the older release is enough: the service goes back the same way (a version that differs is an update, in either direction).
- If the new program does not start, stop what is left (the launcher), rename `timevault.exe`, copy `timevault.old-<version>.exe` to `timevault.exe` and start it.
- A migration that the new version applied stays in the database. Read the upgrade notes of the version in [changelog.md](./changelog.md) (0.1.7 says that 0.1.6 must not be started against its database) and put the database from before the upgrade back when they say so.

**Limits and trust.**

- **The release folder decides what runs on the server.** There is no signature: the size and the sha256 only protect against a half-copied or damaged file, not against somebody who can write both files. Let only administrators write the folder (and its share), and use `https` for a web address. See [security.md](./security.md).
- The update waits for the end of the running jobs without a limit. If a restart cannot wait, stop the service by hand.
- Versions are compared for equality, not for order.
- `check-config` checks the settings, not the world (it connects to nothing). A program that passes it can still fail later, for example in a migration: the launcher then ends with the exit code of the program, and the log says why; the old program is still there.
- Left in the folder of the program: `timevault.new.exe` and `timevault.new.exe.part` (a download in progress; removed at the next start), `timevault.old-<version>.exe` (the newest three; one that is still in use is deleted later), `timevault.exe.update.json` (the note for the new program; deleted when it starts).

## Pictures of the accounts

The header, the audit log, the job page and the job lists, the summary of who has access to a folder and the people lists of the permission editor show the picture of an account next to its name. The picture is read from `TIMEVAULT_AVATAR_URL`, by default `/api/static/hr/{id}.webp` (the pictures of the employees that the other company applications keep; `{id}` is the employee ID).

- The address is a path on the host that serves the site and is not put under `TIMEVAULT_BASE_PATH`: behind the company's nginx `/api/` and `/tv/` are on the same host name, so the browser finds `/api/static/hr/E1001.webp` next to `/tv/`. `off` turns the pictures off.
- The browser asks for each picture itself, once for as long as the page lives and only for the pictures that are about to be seen (the people list of the permission editor is long). A person without a picture (the application answers `202` with a placeholder instead of `404`), a failed request and an answer that is not an image leave the default picture, and a missing picture is remembered for the browser session.
- No picture is asked for the local administrator, for the names that the service uses itself (`scheduler`, `updater`, `system`) or for a name that cannot be an employee ID. TimeVault stores nothing and the content security policy is unchanged (the picture is shown from the data the request returned).
- When the service is opened directly on its own port there is no `/api/static/hr/`, so everybody has the default picture.

## Operating

- **Who may do what.** Anyone can browse (Explorer, Time Machine, file versions, the state of the Backups page and of backup jobs) the folders that let guests browse, without signing in; a folder that does not is not there for a visitor at all. Everything else needs an active account, and what an account may do with a folder is decided for each folder: **Browse** (also restoring, and the jobs and audit events of the folder), **Back up** (back up now, full re-read, verify, stop jobs) and **Full control** (change the folder's settings and who may do what with it); see [Folder permissions](#folder-permissions). The audit log (events of the folders the account may browse, and the sign-ins and other events that belong to no folder), the status page and the settings page are for every signed-in account. Only an `admin` account (Redis `Type` is `admin`, or the local administrator) adds monitored folders and has full control of all of them, and only an administrator sees the folders on the server's disk; everybody else sees display folders. See [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md), [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md), [ADR 0013](./adr/0013-per-folder-access-levels.md) and [api.md](./api.md#who-may-call-what).
- **Browsing long folders.** The Explorer and the Time Machine list a folder in pages (100 items by default; 50, 100, 200 or 500 can be chosen, with the count and page links above and below). The **Type** column shows the extension in capitals ("Folder" for folders, "File" for a file without an extension); the **Type** drop-down above the list keeps only one kind (all folders, all files, a file without an extension, or one extension, each with its count). Click **Name**, **Type**, **Size** or **Modified** to sort the whole folder, and click again to reverse it; folders always come first. The choice is in the address, so a link keeps it. **Select all** in the Time Machine selects the items on the current page, and a restore can take up to 500 paths. On a long page two round buttons at the bottom right jump to the top and to the bottom. A folder of 10,000 files opens in about 0.1 s and one of 50,000 in about 0.5 s (measured on the development machine, with all files in one folder); several hundred thousand entries in one folder take seconds, because every page reads the whole folder.
- **Health.** `GET /healthz` returns `ok` without signing in and only proves the process is up. The **Status** page (accounts only) checks the database, the repository (a write test and free space; "degraded" below 5% free), the search engine, the Redis user directory ("degraded" when it does not answer: browsing, existing sessions and the local administrator still work), the restore folder and the job queue, and lists VSS and USN as "disabled". Without a session only `/healthz`, the sign-in page, the language switch, the static assets and the browsing pages work.
- **Monitored folders.** An administrator adds them on the **Settings** page (a local folder; it must not overlap the repository, the restore folder or another monitored folder; give it its display folder too, see [Where settings live](#where-settings-live); and say who may see and use it, see [Folder permissions](#folder-permissions)). An administrator, or whoever has full control of the folder, can edit the name, the display folder, the exclude patterns, the on/off switch, the schedule, the versions kept and the permissions at any time. The folder on the server itself can be corrected, by an administrator, until the first recovery point exists (in any state, even a failed one); after that, switch the old one off and add the new one. A switched-off folder keeps its history and is still browsable; it cannot be backed up.
- **Backups.** Any signed-in account that has Back up (or Full control) on a folder can press **Back up now** in **Backups**. A normal backup trusts size and modification time to skip unchanged files; **Full re-read** reads and hashes every file again. Only one backup per source runs at a time. A backup that cannot read a file (locked, changed while reading, link, special file) records it as skipped with the reason, keeps the previous good version, and still completes with a warning.
- **Automatic backups.** Tick "Back up automatically every N minutes" for a folder (1 to 43200). The service looks every 15 seconds; a folder is due when it never ran or its last run ended N minutes ago, whatever the outcome, and a run that finds nothing changed records nothing: no recovery point, no job, no audit event. Setting a schedule, or switching a folder on again, starts a backup at once when the last run is older than the interval. Folders that are due start one at a time and spread out (see [Scheduler settings](#scheduler-settings)), so a folder can start later than the moment it is due. Without a limit of versions per file (next item) a short interval on a folder that keeps changing grows the repository without bound. See [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md) and [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md).
- **Versions kept per file.** Each monitored folder has a limit (Settings, "Versions kept per file", 1 to 10000; 0 or empty keeps everything). After each backup the files it changed are looked at: when a file has more versions than the limit, its older ones are deleted for good, and repository content that no version needs any more is freed (only while no backup runs, so it can take a while to show in the free space). A recovery point that lost versions is marked **Partial** on the Backups and Time Machine pages: it no longer shows the files whose versions were deleted, so a restore from it can be incomplete, and verifying it checks the objects but not the manifest. Setting a limit on a folder that has history, or lowering it, queues a **Version cleanup** job for the whole folder; lowering it first asks for a tick ("I understand that these versions are deleted for good") next to a count of the versions and files that would go. A cleanup is also queued when the deletion right after a backup could not run, because a restore or verification of the folder was queued or running or because it failed: it would otherwise be forgotten, as the next backup only looks at the files it changed itself. A queued cleanup holds no worker; it starts when no backup, restore or verification of the folder is running, and while it runs the backups of the folder wait in the queue. While a cleanup runs, restores and verifications of the folder are refused, and so is a change of its limit; a restore or verification that was queued before waits for it to end. The audit log records **Old versions deleted**. See [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md).
- **Verify.** The verify job on a recovery point checks the catalog against the manifest digest and that every referenced object exists with the right size; "deep" also re-hashes the content. Run it after copying or restoring the repository.
- **Restore.**
  1. Sign in, open **Time Machine**, choose a source and a recovery point, then tick files or folders and choose **Restore selected…**, or choose **Restore this folder…** / **Restore everything…**. The version page of a single file has **Restore…**. Visitors who are not signed in see a **Sign in to restore files** link instead. Whoever may browse a folder may restore from it.
  2. Review the preview: counts, size, conflicts, blocked items (missing content, unsafe names, never captured) and the planned changes.
  3. Choose the destination folder name (a folder below `TIMEVAULT_RESTORE_ROOT`, created if it does not exist), the conflict policy (skip, rename, overwrite) and, if you accept restoring only part of the selection, the partial option. Overwriting needs an extra confirmation.
  4. **Start restore** within 15 minutes of the preview (the token expires and is bound to your user and the exact plan; any change needs a new preview). Follow the job page. Do not start another restore into the same folder until it has finished. Copy the result from the destination to its final place yourself: the preview and the job page name it by its display folder (`<TIMEVAULT_RESTORE_DISPLAY_ROOT>\<destination>`), which is where you find it over the network; administrators also see `<TIMEVAULT_RESTORE_ROOT>\<destination>`. Restores never write into a source.
- **Audit.** The **Audit** page (accounts only) lists sign-ins and failed sign-ins (with the account name as it was typed, cut to 64 characters; a single sign-in shows its method and the Windows user), refused requests, changes to monitored folders (the old and new value of every field that changed, the access lists included), backups, verifies, restore previews and results, the deletion of old versions, cancellations, and the steps of a program update (**Program update**, by the actor `updater`; see [Releasing and automatic updates](#releasing-and-automatic-updates)). An administrator sees all of them; everybody else sees the events of the folders they may browse and the events that belong to no folder (sign-ins and the like). The **Duration** column shows how long a backup, a verification, a restore or a cleanup ran, and for a backup the speed of the data it copied: the first backup of a big folder tells how fast the disk and the repository are (a later backup copies little, so its speed says little). The application has no way to change or delete these records, and a database trigger rejects UPDATE and DELETE statements on them. Automatic runs that find nothing changed leave no record.
- **Backing up TimeVault itself.** Back up both the PostgreSQL database (for example with `pg_dump`) and the whole repository folder. The repository only grows, so an older database copy with a newer repository is consistent, but a newer database with an older repository reports missing objects (verify shows them and restores block). After restoring either, run a deep verify.

## Troubleshooting

| Symptom | Cause and action |
|---|---|
| The service stops with "is not a loopback address" | Use `127.0.0.1` behind a reverse proxy, or set `TIMEVAULT_ALLOW_NON_LOOPBACK=true` knowingly. |
| The service stops with a schema message | Run `timevault-server migrate`, or set `TIMEVAULT_AUTO_MIGRATE=true`. |
| The service stops with "no way to sign in" | Set `TIMEVAULT_REDIS_URL` (company accounts) and/or `TIMEVAULT_ADMIN_PASSWORD_HASH` (a local administrator; create the value with `timevault-server hash-password`). |
| The service stops with an error about `.env` | The message names the file and the line number, never the line. `TIMEVAULT_ENV_FILE` must name an existing file. |
| The service stops with an error about `TIMEVAULT_SERVER_HOST` or `TIMEVAULT_RESTORE_DISPLAY_ROOT` | The message names the setting and what is wrong, never the value. The host name may only hold letters, digits, `-`, `_` and `.`; the display root must look like `\\server\share\folder`, with a share that does not end in `$`, and must be on `TIMEVAULT_SERVER_HOST`. |
| Adding or editing a monitored folder is refused with "Enter the display folder" or "Folders on other servers cannot be monitored here" | `TIMEVAULT_SERVER_HOST` is set, so the display folder is required and its host must be that server (the message names both). Folders that were added before the setting existed have no display folder; give each one on the Settings page. |
| The service stops with "another TimeVault service is already running against this database" | A second `timevault-server` uses the same database (schema). Stop the other one first; its lock is released when its process ends. |
| The service stops with "not a TimeVault repository" | `TIMEVAULT_REPOSITORY_PATH` points at a non-empty folder without `timevault-repository.json`. Use an empty folder. |
| `go` is not found by the scripts | Add Go to `PATH`, or put the line `TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>` into `.env`. Setting the variable in one terminal only lasts for that terminal. |
| After a rebuild the footer, the Settings page or `timevault-server version` still shows an old version number | Either the build command still has `-X main.version=<old number>` (a copy of an older command), so the program has the new code under the old number: build with the command in [Building the executable](#building-the-executable), which has no `-X`. Or the service still runs the old file: Windows does not let a running program file be replaced, so stop the service, copy the new file, start it, and run `version` on the file that is really started. A build made with `-trimpath` does not record `-ldflags`, so `go version -m` cannot show the flag; `version` is the check. |
| The service listens on another port (or uses another setting) than the one written in `.env` | A `TIMEVAULT_*` variable in the process environment beats `.env`. The start-up log has a warning that names every setting it overrides (`TIMEVAULT_LISTEN_ADDR`, for a port). The usual cause is a variable left in the terminal by an older `dev-run.ps1`: start the service with `scripts\dev-run.ps1`, or load the session with `. .\scripts\dev-env.ps1`, which remove such variables. If it comes back, it is set in your Windows user or system environment. |
| `dev-db.ps1` cannot find PostgreSQL | Set `TIMEVAULT_PG_BIN` or pass `-PgBin`. |
| Sign-in answers 429 | Too many attempts: 5 per address or 10 failures per account name within 10 minutes (`Retry-After` says how long). Behind a reverse proxy all visitors share the proxy's address unless `TIMEVAULT_TRUSTED_PROXIES` lists the proxy. Wait, or restart the service (the counters are in memory). |
| Sign-in says the user accounts cannot be reached (503) | Redis does not answer. Browsing, sessions that already exist and the local administrator keep working. Check `TIMEVAULT_REDIS_URL` and run `timevault-server check-redis-users`; the **Status** page shows the directory as "degraded". Attempts made meanwhile count for the address, not against the account. |
| A company account cannot sign in | Wrong password, account not activated, no password stored, or the login matches more than one account (employee ID, name or e-mail address; the service log says so without naming anyone). Run `check-redis-users`: unreadable records, records without a password and employee IDs on more than one key are counted there. Try the employee ID, which is the least ambiguous. |
| `check-redis-users` reports unreadable records | A hash that the decoder cannot read (a field is missing or has an unexpected type). The report gives the position of the key among all keys sorted by name, so it can be found with `redis-cli`. Such a record cannot sign in. |
| There is no "Sign in with Windows (SSO)" button | `TIMEVAULT_SSO_HOST` is empty, or there is no Redis user directory (`TIMEVAULT_REDIS_URL`). The start-up log and the Settings page show `sso_host`. |
| The single sign-in says "The sign-in could not be matched to this browser" | The address that the sign-on service recorded is not the address TimeVault sees for the browser. Behind a reverse proxy set `TIMEVAULT_TRUSTED_PROXIES` to the proxy (the proxy has to send `X-Forwarded-For`), or set `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`. The Audit page shows the failure with the reason `address_mismatch` (`browser_address_changed` when the address of the browser changed during the sign-in), with what TimeVault saw: `started_from` (the address it took for the browser), `sso_address`, `connection`, `trusted_proxy` and `forwarded`. If `started_from` is the address of a server, another proxy sits in front of the one next to TimeVault and hides the browser: see [Two proxies in a row](#two-proxies-in-a-row). If `trusted_proxy` is `false` and `forwarded` is set, the proxy that connects is not in `TIMEVAULT_TRUSTED_PROXIES`. |
| Opening `/tv/` ends on another application (for example the portal), or the pages have no style and their links leave `/tv/` | TimeVault does not know its prefix. Use version 0.1.5 or later, set `TIMEVAULT_BASE_PATH=/tv` in `.env` and restart; the start-up log must say "serving under a path prefix". Without it every redirect and link starts at the root of the host, outside `/tv/`. |
| Behind `/tv/` the audit log shows `127.0.0.1` for everybody, or single sign-on says it could not be matched to this browser | The proxy does not pass the address of the browser on, or TimeVault does not believe the proxy. Set `proxy_set_header X-Forwarded-For $remote_addr;` in the proxy block, and `TIMEVAULT_TRUSTED_PROXIES=127.0.0.1` (the address the proxy connects from) in `.env`. |
| After signing in, a page whose address has no `/tv` shows you as signed out | With a base path the cookies are limited to it. Use the addresses under `/tv/`; every link and redirect of the pages already leads there. |
| The single sign-in says the browser blocked the window | Allow pop-ups for the TimeVault site. The window can only be opened by the click itself. |
| The single sign-in waits and then says it did not finish in time | The sign-on service did not record a result. Check that `TIMEVAULT_SSO_HOST` is the service people really reach, and that it writes `sso_login_<token>` to the Redis of `TIMEVAULT_REDIS_URL` (the same database number). |
| The single sign-in says "Your Windows account has no active account here" | The employee ID that Windows gave has no active account in Redis (the Audit page shows `no_active_account` and the employee ID). Check the account with `check-redis-users`. |
| Settings says "Only an administrator can add monitored folders" | That is how it is: adding a folder is for administrators (the account's `Type` in Redis is `admin`; a change of role applies within a minute). Changing a folder's settings needs **Full control** of that folder, which an administrator can give on the Settings page. |
| A person cannot see a monitored folder that they should, or sees "not found" for it | Nobody sees a folder that nobody was given. Open the folder's **Edit** form on the Settings page (as an administrator, or with full control of the folder) and check that the person's employee ID is in a list, or that **Everyone who is signed in** (or, for visitors who have not signed in, **Guests**) is ticked. A visitor who has not signed in only has what **Guests** gives. See [Folder permissions](#folder-permissions). |
| Backing up (or saving a folder's settings) answers "You are not allowed to do this" | The account can see the folder but has too low a level: **Back up** is needed to back up, verify or stop a job, **Full control** to change the settings. The Audit page shows the attempt as **Access denied** with the folder and `needs`. Only an administrator can change the folder on the server. |
| The permissions part of a folder's form says the user directory cannot list its accounts, or refuses to add a person | Redis does not answer (or `TIMEVAULT_REDIS_URL` is not set). People who already have access are shown and can stay; nobody else can be added until the directory answers. Check the **Status** page. |
| Back up now answers that the monitored folder is switched off (409) | An administrator has to switch it on in Settings. |
| Moving a folder answers 409 or says it already has backups | A backup of that folder is running (try again when it has finished), or the folder has a recovery point in some state and can no longer be changed: switch it off and add the new folder. |
| An automatic backup does not run | The folder is switched off or has no schedule; or its last run ended less than the interval ago (the Backups and Settings pages show the next time); or the pace does not allow it yet (one automatic backup runs at a time, with a gap and a rest; see [Scheduler settings](#scheduler-settings)); or, with the Everything probe on, Everything says nothing changed; or the service is not running. The scheduler looks every 15 seconds. |
| The job I was watching says "not found" | An automatic backup that finds nothing changed leaves no job behind. |
| Folders show "-" instead of a size on the Explorer | Everything is not configured or not answering, does not index folder sizes (turn on "Index folder size" in Everything), or does not know the folder. Files are not affected. The Time Machine and the versions page never show folder sizes. |
| Changing the limit of versions is refused with "This limit permanently deletes … older version(s)" (409 `prune_not_confirmed`) | Lowering the limit would delete versions. Tick the confirmation box and save again (API: `confirm_prune: true`). |
| A restore or a verification is refused with "old versions of this folder are being deleted" (409 `prune_running`) | A version cleanup of the folder is running; changing the folder's version limit is refused meanwhile too. Try again in a minute; the Backups page lists the job. A cleanup that is only queued, behind a backup or a verification of the folder, refuses nothing. |
| The repository does not shrink after versions were deleted | The content is only freed while no backup runs, and within a limited time per sweep; the scheduler repeats it. A server that is always backing something up delays it. |
| "Restore is not enabled" | `TIMEVAULT_RESTORE_ROOT` is empty. |
| The restore preview is rejected as stale or expired | Preview again; tokens last 15 minutes and are tied to the exact plan. After a restart without `TIMEVAULT_SESSION_KEY` the key changes too. |
| Files are listed as skipped (locked) | The file was open by another program, or changed while it was read. Run the backup again later; there is no VSS in the MVP. |
| A restore reports a missing or corrupt object | The repository lost or damaged a file. Run a deep verify, restore the repository from its backup, or choose another recovery point. Nothing is restored from a bad object. |
| An interrupted restore left `.tvpart-*` files | Temporary files in the destination folder from a crashed restore. They can be deleted. |
| Search says "not configured" or "unavailable" | See [Everything search](#everything-search). History and restore keep working. "Not configured" means `TIMEVAULT_EVERYTHING_URL` is empty; "unavailable" means the Everything it names does not answer (its HTTP server is switched off, or the port, user name or password differ). |
| A job stays "failed: interrupted by a service restart" | The service was stopped while the job ran. Start the job again; unfinished recovery points were already cleaned up. An update never does this: it waits for the running jobs. |
| The Settings page says **Waiting for the running jobs**, and the notice under the header stays | A job is running (see the Backups page) and the update waits for it, without a limit. New jobs wait in the queue until the service has restarted. If the restart cannot wait, stop the service by hand; the job is then recovered as failed when the service starts again, and the update starts over at the first look after that start (10 seconds later). |
| **Program updates** says **Problem**, and the administrator sees "read the manifest: …" | The service cannot read the manifest at `TIMEVAULT_UPDATE_URL`: the file is not there yet, the share or the web address is wrong or not reachable from the server, the account of the service cannot read it, or an `http(s)` address answers something other than `200` (a `404`, a redirect to a login page, a `202`). Open the address from the server. |
| **Problem**: "the program is not the one the manifest describes" | The size or the sha256 of `timevault.exe` is not the one in `timevault.json`: the release is still being copied (it is tried again at every look and resolves itself), or the two files are from different builds, or `package_url` leads to another file. Copy both files again, the manifest last, or run `scripts\release.ps1` again. |
| **Problem**: "the program of the release says it is version X, but the manifest says Y" | `latest_version` in the manifest is not the version that is in the program (a hand-edited manifest, or a build of sources whose `var version` was not raised). It is not tried again until the manifest changes. Run `scripts\release.ps1`, which takes the version from the program. |
| **Problem**: "the new program does not accept the settings of this service: …" | `timevault.exe check-config` of the new version refuses the `.env` (a setting that the new version validates more strictly, or one that it requires). Read the reason, change the `.env`; the next look tries again. The service keeps running with the old program meanwhile. |
| **Problem**: "the new program could not be put in place: … Access is denied" | The account that runs the service cannot rename or create files in the folder of the program, or an antivirus holds the file. Give the account **Modify** on that folder (the start-up log warns about this). The service keeps running with the old program and does not try this release again until the manifest changes or the service is restarted. |
| After an update the console window or the scheduled task shows the program as ended, with the exit code 75 | `TIMEVAULT_UPDATE_RESTART=exit` and nothing starts the program again. Use `supervise` (the default), or make the service manager restart the program when it ends (NSSM: `AppExit 75 Restart`). |
| After an update the old program file `timevault.old-<version>.exe` cannot be deleted | With `supervise` the launcher still runs from it (see [Releasing and automatic updates](#releasing-and-automatic-updates)). It is deleted at a later update when the launcher has ended; it can be deleted by hand after the service has been stopped and started again. |
| The service updated itself back to the newer version after a manual way back | The release folder still has the newer manifest. Put the older release there (or empty `TIMEVAULT_UPDATE_URL`) before you go back. |
| The pictures of the accounts are not shown | Check `TIMEVAULT_AVATAR_URL` (the start-up log has `avatar_url`; `off` means no pictures), and open `/api/static/hr/<employee ID>.webp` on the same host name in the browser: it must answer `200` with an image. A `202` (the placeholder for a person without a picture) and a `404` leave the default picture on purpose. Reaching the service directly on its own port (`http://host:8660/`) has no such address. |

---

# 中文

## 概述

介绍如何安装、配置、运行、运维和排查 TimeVault（0.1.8 版）。它描述的是目前实际存在的内容：服务是一个 Go 程序 `timevault-server`，需要 PostgreSQL 和一个仓库文件夹；公司账号登录还需要另一个应用的 Redis 用户目录。目前没有安装程序，也没有 Windows 服务封装。

## 前置条件

| 项目 | 说明 |
|---|---|
| 带 NTFS 的 Windows | 在 Windows 11 Pro 上开发和测试。服务也能在其他系统上编译（部分路径规则和测试仅适用于 Windows），但只在 Windows 上实际运行过。 |
| Go 1.25 或更新版本 | 构建所需（`go.mod` 声明了 `go 1.25.0`）；恢复代码使用了 `os.Root`。可以使用便携版 Go：把 `TIMEVAULT_GO_ROOT=<Go 文件夹>`（包含 `bin\go.exe` 的那个文件夹）这一行写入 `.env`，开发脚本就会在每个终端里把它加入 `PATH`，而不必修改系统 `PATH`。 |
| PostgreSQL | 只测试过 16.4。所用 SQL 至少需要 PostgreSQL 11（identity 列、`hashtextextended`），但没有试过低于 16 的版本。 |
| Redis（可选） | 另一个应用（dash）的用户目录：名为 `app_user_<ee_code>_<name>_<email>` 的哈希。没有它时只有本地管理员可以登录。测试使用的是进程内的模拟服务和 Python 生成的 pickle 数据，没有连接过公司自己的 Redis：请在它上面运行一次 `timevault-server check-redis-users`。 |
| Everything 1.4.x（可选） | 仅用于搜索。没有它时搜索显示"未配置"，其他功能正常。见 [Everything 搜索](#everything-搜索)。 |

## 开发环境快速开始

脚本把数据放在被 git 忽略的 `data\` 文件夹下，**所有设置放在同一个被 git 忽略的文件里：仓库文件夹中的 `.env`**（见[设置放在哪里](#设置放在哪里)）。脚本不会触碰已有的 PostgreSQL 服务，也不会打印任何机密信息。如果 PowerShell 拦截脚本，请用 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1` 运行。如果 Go 不在 `PATH` 中，请先把 `TIMEVAULT_GO_ROOT=<包含 bin\go.exe 的文件夹>` 这一行写入 `.env`；否则 `dev-run.ps1` 会立即以"The go command was not found"退出。早期版本还把设置放在另外四个文件里（`data\local.env`、`data\pgdev\db.env`、`data\dev\server.env`、`data\everything\everything.env`）；脚本第一次运行时会把它们的设置移入 `.env` 并删除这些文件。

1. `scripts\dev-db.ps1 start`（首次）创建并启动一个私有 PostgreSQL 集群，位于 `data\pgdev`，监听 `127.0.0.1:55432`，包含数据库 `timevault` 和 `timevault_test`。随机密码写入 `.env`（`TIMEVAULT_DATABASE_URL` 和 `TIMEVAULT_TEST_DATABASE_URL`）。它需要已安装的 PostgreSQL 二进制文件（用 `.env` 中的 `TIMEVAULT_PG_BIN` 或 `-PgBin` 指定位置）。其他动作：`stop`、`status`、`init`、`reset`（会删除该集群）。**也可以跳过这一步，改用你已有的 PostgreSQL 服务器**：自己创建好数据库和角色后，把 `TIMEVAULT_DATABASE_URL`（以及测试用的 `TIMEVAULT_TEST_DATABASE_URL`）写入 `.env`。这样脚本不会启动私有集群，并且只要 `.env` 指向其他服务器，`dev-db.ps1` 就会拒绝创建或重置私有集群。
2. `scripts\dev-run.ps1` 在 `.env` 不存在时创建它（带注释，已被 git 忽略）；在 `TIMEVAULT_DATABASE_URL` 指向私有集群（端口 55432）时启动它，否则认为你配置的服务器已在运行；构建 `data\dev\bin\timevault-server.exe`；并把服务需要而 `.env` 里还没有的内容加入 `.env`：`data\dev` 下的仓库和恢复文件夹、`TIMEVAULT_AUTO_MIGRATE=true`、会话密钥，以及在第一次运行时为本地管理员 `admin` 生成的随机密码（密码放在 `data\dev\admin-password.txt`，只有它的哈希进入 `.env`）。**在启动服务之前，它会移除终端里遗留的 `TIMEVAULT_*` 变量**，因为它们会覆盖 `.env`，并且会把它们的名字列出来。然后在前台运行服务，地址为 `http://127.0.0.1:8660/`（用 `-Port` 修改这一次运行的端口；不带该参数时使用 `.env` 中的 `TIMEVAULT_LISTEN_ADDR`），并打印是否已配置 Redis 用户目录。按 Ctrl+C 停止。
3. `scripts\dev-demo.ps1`（服务运行时）把 `data\dev\source` 登记为监控文件夹"Demo files"，并通过 API 创建三个恢复点，同时留下一些未备份的改动，使文件浏览页能显示已变更、新增和已删除的项目。它从 `.env` 中的 `TIMEVAULT_LISTEN_ADDR` 取得地址；用 `dev-run.ps1 -Port` 启动之后，请传入 `-BaseUrl http://127.0.0.1:<port>`。
4. 打开该地址。任何人都可以浏览。要备份、恢复或修改设置，请用 `admin` 和 `data\dev\admin-password.txt` 中的密码登录；或者在 `.env` 中写入 `TIMEVAULT_REDIS_URL` 后使用公司账号登录（在仓库文件夹中运行 `data\dev\bin\timevault-server.exe check-redis-users` 检查）。页眉有语言切换（English / 中文）和浅色/深色主题切换按钮（在使用该按钮之前，页面跟随系统设置；之后浏览器会记住你的选择）。

5. 可选，用于搜索框：`scripts\dev-everything.ps1 -Action start`，然后重启服务（见 [Everything 搜索](#everything-搜索)）。

`. .\scripts\dev-env.ps1` 为你自己的 PowerShell 会话做好运行 `go test` 和手动运行 `timevault-server` 的准备。它会移除会话里遗留的 `TIMEVAULT_*` 设置（它们会覆盖 `.env`），把 `TIMEVAULT_ENV_FILE` 设为仓库的 `.env`，加载只有测试和工具才读取的内容（`TIMEVAULT_TEST_*`、`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN`、`TIMEVAULT_EVERYTHING_EXE`），并把 Go 加入该会话的 `PATH`。服务自己的设置不会被复制到环境变量中：服务直接读取 `.env`。

## 命令

| 命令 | 用途 |
|---|---|
| `timevault-server serve` | 运行服务（不带命令时的默认行为）。 |
| `timevault-server migrate` | 应用数据库迁移后退出。 |
| `timevault-server hash-password` | 读取密码（在终端中输入时不回显，或从标准输入读取一行），并输出可用于 `TIMEVAULT_ADMIN_PASSWORD_HASH`（可选的本地管理员）的值。 |
| `timevault-server check-redis-users` | 连接 Redis 用户目录（`TIMEVAULT_REDIS_URL`），并输出其中有多少可以使用：键的数量、可读和不可读的记录、已启用的账号、类型为 `admin` 和 `user` 的数量、没有密码的账号、出现在多个键上的工号，以及每个字段有多少值可以解码。它不输出任何姓名、邮箱、键或密码。Redis 没有应答或没有任何可读记录时退出码为 1。请在包含 `.env` 的文件夹中运行，或设置 `TIMEVAULT_ENV_FILE`。 |
| `timevault-server version` | 输出版本号。 |
| `timevault-server check-config` | 检查设置（环境变量和 `.env` 文件）对服务是否有效，输出 `the settings are valid`，或输出无效的原因（退出码为 1）。它不连接任何东西。会自己更新的服务在替换自己之前，会用新程序运行它（见[发布与自动更新](#发布与自动更新)）。 |

## 构建可执行文件

除了 `.env`，服务只需要 `timevault-server.exe` 这一个文件：Web 模板、静态资源和数据库迁移脚本都已嵌入其中。程序是包 `./cmd/timevault-server`；仓库根目录不是程序（`go build .` 会报 "no Go files"）。

```powershell
cd <repository folder>
go build -trimpath -ldflags "-s -w" -o timevault-server.exe ./cmd/timevault-server
.\timevault-server.exe version
```

只需 `go build -o timevault-server.exe ./cmd/timevault-server` 就够了。`-trimpath -ldflags "-s -w"` 只是让文件更小（约 22 MB，而不是 31 MB）。需要 Go 1.25 或更新版本（见 `go.mod`）。

**版本号**只写在一个地方，即 `cmd/timevault-server/main.go` 中的 `var version`，构建时直接从那里取，所以上面的命令得到的总是它所在源码的版本。`version` 命令、启动日志、每个页面的页脚、设置页和 `GET /api/v1/settings` 都会显示它。每次构建之后请运行 `.\timevault-server.exe version`。不要在命令里加 `-X main.version=<number>`：它会替换 `main.go` 中的版本号，所以为旧版本写的命令副本会一直打上旧的版本号。用 0.1.5 的源码加上 `-X main.version=0.1.4` 构建出来的程序，具备 0.1.5 的全部功能，却自称 0.1.4。

**依赖和 `vendor\` 文件夹。** 构建需要 `go.mod` 中列出的 Go 模块，它们来自模块缓存（`go env GOMODCACHE`，由 Go 从模块代理下载填充），或者来自 `go.mod` 旁边的 `vendor\` 文件夹。存在 `vendor\` 文件夹时，Go 会自动使用它（等同于 `-mod=vendor`；`go build`、`go vet` 和 `go test` 都是如此）。这适合没有外网的服务器：在有这些模块的计算机上运行一次 `go mod vendor`（会写出约 600 个文件、7 MB），然后把 `vendor\` 文件夹和源码一起复制过去。

- `vendor\` 不属于仓库，所以克隆仓库得不到它。请把它和源码一起复制，或者在有它的计算机上构建。
- 没有 `vendor\` 文件夹时使用 `-mod=vendor` 会失败，提示 `go: inconsistent vendoring ... is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt`。`vendor\` 比 `go.mod` 或 `go.sum` 旧时也会出现同样的提示。请重新运行 `go mod vendor`（每次更改依赖之后都要运行），或者用 `-mod=readonly` 从模块缓存构建，它会忽略 `vendor\`。
- 部署到没有外网的服务器，最简单的做法是在开发电脑上构建 `timevault-server.exe`，然后把这个文件连同 `.env` 复制到服务器上。
- 会自己更新的服务器只需要复制这一次：`scripts\release.ps1` 会生成这类服务器读取的发布文件夹（见[发布与自动更新](#发布与自动更新)）。

## 设置放在哪里

| 内容 | 位置 | 由谁修改 | 何时生效 |
|---|---|---|---|
| 部署设置：数据库 URL、Redis URL、监听地址、备份文件夹（`TIMEVAULT_REPOSITORY_PATH`）、恢复文件夹（`TIMEVAULT_RESTORE_ROOT`）、本服务器的主机名（`TIMEVAULT_SERVER_HOST`）、恢复文件夹的显示路径（`TIMEVAULT_RESTORE_DISPLAY_ROOT`）、会话密钥、搜索、worker、日志 | `.env` 文件（进程环境变量会覆盖它） | 安装服务的人，通过编辑文件 | 重启之后 |
| 监控文件夹：名称、显示文件夹、服务器上的文件夹、排除规则、启用或停用、自动备份计划、每个文件保留的版本数 | 数据库（表 `sources`） | 管理员添加文件夹，并可以修改它们的一切；对某个文件夹拥有"完全"权限的人，在**设置**页或通过 `PATCH /api/v1/sources/{id}` 修改它的设置，但不能修改服务器上的文件夹 | 立即 |
| 每个文件夹上谁能做什么（浏览、备份、完全；可以授予访客、所有已登录的人或单个工号） | 数据库（表 `source_access`） | 管理员，以及对该文件夹拥有"完全"权限的人，在**设置**页该文件夹表单的权限部分，或通过 API 的 `access`（见[文件夹权限](#文件夹权限)） | 立即，对每个访问者的下一次请求生效 |
| 账号和角色 | Redis（`app_user_*`），由另一个应用写入 | 那个应用 | 对已经登录的会话，在一分钟之内 |

- `.env` 文件是 `TIMEVAULT_ENV_FILE` 所指的文件（它必须存在），否则是工作目录下的 `.env`，否则是 `timevault-server.exe` 旁边的 `.env`。使用找到的第一个；不会合并多个文件。开发时它就是仓库文件夹中的 `.env`，也是唯一的设置文件：开发脚本读写它，`dev-run.ps1` 让服务使用它。
- **进程环境变量中的 `TIMEVAULT_*` 变量优先于文件中的同名键**，而且除非你去查，否则没有任何提示：终端里遗留的某个变量会让服务无视你在 `.env` 里写的内容（典型表现是端口改不动）。因此服务在启动时会用一条警告点名所有被环境变量覆盖的设置（`check-redis-users` 也会打印它们），开发脚本则会从终端里移除这类变量。如果这条警告反复出现，说明该变量设在你的 Windows 用户或系统环境变量里。
- 语法：每行一个 `KEY=VALUE`。空行和以 `#` 开头的行会被忽略，允许行首的 `export `，接受 UTF-8 字节顺序标记和 Windows 行尾。值按字面一直取到行尾，所以带 `\` 的路径或带 `#` 的密码无需转义（行尾的 ` # 注释` 会成为值的一部分）；值两侧成对的单引号或双引号会被去掉；同一个键以最后一次赋值为准。出错时只指出行号，绝不显示该行内容。
- 该文件包含机密信息（数据库和 Redis 密码、会话密钥）。`/.env` 已在 `.gitignore` 中；请让它只能被运行服务的账户读取，并且绝不要提交填好的副本。[configs/timevault.example.env](../configs/timevault.example.env) 是模板。
- **设置**页（任何已登录账号都可以打开）显示访问者可以浏览的监控文件夹（每个都标明访问者对它能做什么）、备份存储和恢复文件夹（含仓库的剩余空间），以及当前运行的配置：版本、本服务器的主机名、监听地址、数据库和 Redis（显示为 `host:port/name`）、单点登录服务、本地管理员的名称、搜索服务器，以及自动更新的状态（见[发布与自动更新](#发布与自动更新)：发布地址和最近的问题只有管理员能看到，管理员还可以点击**立即检查**）。机密信息绝不显示。添加监控文件夹的表单只给管理员；修改文件夹的表单给管理员，以及对该文件夹拥有"完全"权限的人（其中没有服务器磁盘上的文件夹，那一项只有管理员能看到和修改）。只有管理员能看到服务器磁盘上的文件夹（每个监控文件夹的真实文件夹、备份文件夹、真实的恢复文件夹和设置文件）；其他人看到的是显示文件夹。
- **备份文件夹和恢复文件夹。** 它们是两个不同的地方。备份文件夹（`TIMEVAULT_REPOSITORY_PATH`，即仓库）是 TimeVault 保存所有被备份内容的地方。恢复文件夹（`TIMEVAULT_RESTORE_ROOT`）是放恢复出来的文件的地方：恢复不会覆盖原文件，也绝不会写入监控文件夹，所以它把恢复出来的副本写入恢复文件夹下的一个新文件夹（名称在恢复表单中填写，默认是 `restore-<日期>-<时间>`），需要哪些再从那里复制回去。两者都不能位于监控文件夹之内。
- **显示文件夹。** 人们通过网络访问监控文件夹和恢复文件夹，而不是 `D:\TimeVault\monitor` 这样的路径。因此每个监控文件夹都有一个**显示文件夹**，即人们打开它时使用的网络路径（`D:\TimeVault\monitor` 共享为 `TimeVault` 时是 `\\sgmeslt595.co.murata.local\TimeVault\monitor`），恢复文件夹则有 `TIMEVAULT_RESTORE_DISPLAY_ROOT`（`\\sgmeslt595.co.murata.local\TimeVault\restore`）。把 `TIMEVAULT_SERVER_HOST` 设为本服务器的名称（`sgmeslt595.co.murata.local`）：设置页和 API 就会拒绝没有显示文件夹的监控文件夹，也会拒绝显示文件夹位于另一台服务器上的监控文件夹（它的搜索、备份和恢复只能处理本服务器自己的文件夹）。没有设置 `TIMEVAULT_SERVER_HOST` 时，显示文件夹是可选的，也不会与主机名比较。管理员在设置页填写该字段，设置页还会标出没有显示文件夹的监控文件夹（"还没有显示文件夹"；设置主机名之后，之前添加的每个文件夹都要先填上一个，才能再次编辑）。服务检查的是主机，而不是共享是否真的通向该监控文件夹：请先共享文件夹，再写下你打开它所用的路径。显示文件夹出现的地方，文件浏览、时间机器和文件版本页面都有一行带**复制**按钮的**位置**。
- **谁能看到哪个文件夹。** 管理员同时看到显示文件夹和真实文件夹。其他人无论是否登录，都只看到显示文件夹；引用真实文件夹的文本（任务错误和日志、恢复消息、审计详情、状态详情、文件未能备份的原因）中，会显示显示文件夹、`[<监控文件夹名称>]`、`[backup folder]` 或 `[restore folder]`。见 [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md)。

## 配置参考

每个设置都是一个环境变量，或 `.env` 文件中的一行（见[设置放在哪里](#设置放在哪里)；环境变量优先）。无效的值会使程序退出并给出点名该变量的提示。机密信息绝不会写入日志。

| 变量 | 默认值 | 含义 |
|---|---|---|
| `TIMEVAULT_ENV_FILE` | 无 | 要读取的设置文件路径，用来代替查找 `.env`。该文件必须存在。只能通过进程环境变量设置。 |
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8660` | 监听地址。除非设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，否则会拒绝非回环地址。 |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | 确认由 TLS 反向代理保护非回环监听。服务在监听本机之外时，启动时会记录一条警告。 |
| `TIMEVAULT_DATABASE_URL` | 无（必填） | PostgreSQL 连接 URL。机密。 |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | 启动时应用迁移。为 `false` 时，如果数据库结构不是最新，服务会拒绝启动；请先运行 `migrate`。 |
| `TIMEVAULT_REPOSITORY_PATH` | 无（必填） | 备份文件夹（仓库）：保存被备份文件内容的地方。新文件夹或空文件夹会被初始化；不是 TimeVault 仓库的非空文件夹会被拒绝。修改后需要重启。 |
| `TIMEVAULT_RESTORE_ROOT` | 空 | 恢复文件夹。恢复不会覆盖原文件，也绝不会写入监控文件夹：每次恢复都会在此文件夹下新建一个文件夹（名称在恢复表单中填写），并把恢复出来的副本写到那里。为空表示禁用恢复。不得位于仓库或监控文件夹之内，也不得包含它们。 |
| `TIMEVAULT_SERVER_HOST` | 空 | 人们在网络路径中输入的本服务器名称，例如 `sgmeslt595.co.murata.local`。只能包含字母、数字、`-`、`_` 和 `.`。设置后，每个监控文件夹都需要一个位于该主机上的显示文件夹（比较时不区分大小写），并且 `TIMEVAULT_RESTORE_DISPLAY_ROOT` 也必须位于该主机上。为空时，显示文件夹是可选的，也不会与主机比较。 |
| `TIMEVAULT_RESTORE_DISPLAY_ROOT` | 空 | 人们通过网络访问恢复文件夹的路径，`\\server\share\folder`（两种斜杠都可以）。启动时检查：必须是网络路径，设置了 `TIMEVAULT_SERVER_HOST` 时还必须位于该主机上；没有恢复文件夹时会被忽略。没有它时，普通账号只会被告知恢复文件夹在服务器上。 |
| `TIMEVAULT_AUTH_DISABLED` | `false` | 关闭登录：每个人都是本地管理员。只在回环监听时被接受。仅用于开发。 |
| `TIMEVAULT_REDIS_URL` | 空 | Redis 用户目录，`redis://[:password@]host:port/db`（TLS 用 `rediss://`）。为空表示没有公司账号。机密。 |
| `TIMEVAULT_REDIS_KEY_PREFIX` | `app_user_` | 用户哈希的键前缀。 |
| `TIMEVAULT_REDIS_TIMEOUT` | `3s` | Redis 的连接、读取和写入超时，范围 `100ms` 到 `30s`。 |
| `TIMEVAULT_ADMIN_USER` | `admin` | 可选的本地管理员的名称。 |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | 无（可选） | `timevault-server hash-password` 的输出。创建一个即使 Redis 不可用也能登录的本地管理员；它的名字永远不会到 Redis 中查找。没有它时，服务需要 `TIMEVAULT_REDIS_URL`（除非关闭了登录）。机密。 |
| `TIMEVAULT_SESSION_KEY` | 每次启动随机生成 | 解码后至少 32 字节的 Base64 文本。设置它可使会话和恢复确认在重启后仍然有效。机密。 |
| `TIMEVAULT_SESSION_TTL` | `8h` | 会话有效期，范围 `5m` 到 `720h`。 |
| `TIMEVAULT_COOKIE_SECURE` | `false` | 给 Cookie 加上 `Secure`。在 HTTPS 之后请设为 `true`。 |
| `TIMEVAULT_SSO_HOST` | 空 | 公司单点登录服务，写成 `host`、`host:port` 或 `http(s)://host[:port]`（协议默认为 `http`）。设置后，登录页会提供"使用 Windows 账号登录（SSO）"；见[单点登录](#单点登录)。需要 `TIMEVAULT_REDIS_URL`。 |
| `TIMEVAULT_SSO_REQUIRE_SAME_IP` | `true` | 单点登录服务看到的地址必须与请求登录的浏览器的地址一致。只有在无法得知浏览器的地址时才关闭它（更好的做法是 `TIMEVAULT_TRUSTED_PROXIES`）。 |
| `TIMEVAULT_TRUSTED_PROXIES` | 空 | 反向代理，写成用逗号分隔的地址或 CIDR 范围（`127.0.0.1,::1,10.0.0.0/24`），它们的 `X-Forwarded-For` 头会被信任。此后客户端地址取自该头；它用于单点登录检查、审计日志的**来源**列和登录限流。代理必须自己设置这个头；见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)；它前面还有另一层代理时，见[两层代理](#两层代理)。 |
| `TIMEVAULT_BASE_PATH` | 空 | 站点放在反向代理之后时所处的路径前缀，例如 `http://host/tv/` 对应 `/tv`。为空表示站点位于其地址的根路径。`tv`、`/tv` 和 `/tv/` 都表示 `/tv`；只能包含字母、数字和 `- _ . ~`，并且不能以应用自己的路径开头（`api`、`static`、`login`、`explorer`、`history`、`versions`、`backups`、`jobs`、`restore`、`audit`、`status`、`settings`、`lang`、`logout`、`auth`、`healthz`）。页面、重定向和 Cookie 给出的每个地址都带有它；带或不带它的请求都会被接受。见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)。 |
| `TIMEVAULT_AVATAR_URL` | `/api/static/hr/{id}.webp` | 账号头像的地址：站点所在主机上的一个路径（不会放到 `TIMEVAULT_BASE_PATH` 之下），以 `/` 开头，其中 `{id}` 恰好出现一次，用来放工号。`off` 或 `none` 表示关闭头像（空值表示使用默认值）。见[账号头像](#账号头像)。 |
| `TIMEVAULT_UPDATE_URL` | 空 | 发布清单 `timevault.json` 所在的位置：`http(s)` 地址，或文件、其所在文件夹、共享的绝对路径（`D:\TimeVault\release`、`\\server\share\tv-release`）；以斜杠结尾的地址和文件夹路径都表示该文件夹。不能包含用户名、密码、查询串或片段。为空表示服务从不更新自己。需要 `TIMEVAULT_AUTO_MIGRATE=true`。见[发布与自动更新](#发布与自动更新)。 |
| `TIMEVAULT_UPDATE_INTERVAL` | `10m` | 读取清单的频率，范围 `1m` 到 `24h`。第一次查看在启动后约 10 秒；管理员可以立即要求查看一次（**立即检查**）。 |
| `TIMEVAULT_UPDATE_RESTART` | `supervise` | 更新之后怎样启动新程序：`supervise`（被启动的那个进程作为小型启动器保留，由它启动新程序）或 `exit`（进程以退出码 75 结束，由服务管理器启动）。 |
| `TIMEVAULT_EVERYTHING_URL` | 空 | Everything HTTP 服务的基础 URL，例如 `http://127.0.0.1:<port>/`。为空表示禁用搜索。 |
| `TIMEVAULT_EVERYTHING_USERNAME`、`TIMEVAULT_EVERYTHING_PASSWORD` | 空 | Everything 的可选 HTTP Basic 凭据。密码属于机密。 |
| `TIMEVAULT_EVERYTHING_TIMEOUT` | `5s` | 单次搜索的超时，范围 `100ms` 到 `30s`。 |
| `TIMEVAULT_SCHEDULER_PARALLEL` | `1` | 允许同时排队或运行的自动备份数量（1 到 4）。手动启动的备份不受限制。见[调度器设置](#调度器设置)。 |
| `TIMEVAULT_SCHEDULER_GAP` | `auto` | 两个自动备份启动之间的最短时间：`auto`，或 `0s` 到 `6h` 的时长（`0s` 关闭间隔）。 |
| `TIMEVAULT_SCHEDULER_REST` | `auto` | 一个自动备份结束之后到下一个启动之前的停顿，仅在 `TIMEVAULT_SCHEDULER_PARALLEL` 为 1 时生效：`auto`，或 `0s` 到 `6h` 的时长（`0s` 关闭）。 |
| `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` | `false` | 扫描文件夹之前先问 Everything 它是否有变化，没有变化就跳过扫描。需要 `TIMEVAULT_EVERYTHING_URL`。 |
| `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY` | `24h` | 开启探测后，一个文件夹最长可以多久不做完整扫描，范围 `1h` 到 `720h`。 |
| `TIMEVAULT_PROCESS_PRIORITY` | `below_normal` | 服务在这台电脑上各程序之间的优先级：`normal`、`below_normal` 或 `background`（同时降低磁盘优先级）。仅限 Windows。 |
| `TIMEVAULT_JOB_WORKERS` | `2` | 同时运行的任务数（1 到 16）。数据库连接池的大小与之匹配（2 × worker 数 + 8，至少 16 个连接）；PostgreSQL 的 `max_connections` 必须允许这个数量。 |
| `TIMEVAULT_HASH_WORKERS` | 4 与 CPU 数中较小者 | 一次备份内并行哈希和复制的文件数（1 到 32）。 |
| `TIMEVAULT_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error`。 |
| `TIMEVAULT_LOG_FORMAT` | `text` | `text` 或 `json`。 |

仅供辅助脚本和测试使用、同样放在这个 `.env` 里的变量（服务会忽略它们）：`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN`、`TIMEVAULT_EVERYTHING_EXE`、`TIMEVAULT_TEST_DATABASE_URL` 和 `TIMEVAULT_TEST_EVERYTHING_*`（见[测试](#测试)）。[configs/timevault.example.env](../configs/timevault.example.env) 是带占位符的模板。

## 不使用开发脚本时的运行方式

请先构建 `timevault-server.exe`（见[构建可执行文件](#构建可执行文件)）。

1. 为 TimeVault 创建 PostgreSQL 角色和数据库（请使用你自己的密码）：

   ```sql
   CREATE ROLE timevault LOGIN PASSWORD '<choose-a-password>';
   CREATE DATABASE timevault OWNER timevault;
   ```

2. 把 [configs/timevault.example.env](../configs/timevault.example.env) 复制为 `timevault-server.exe` 旁边的 `.env`，替换每个 `<placeholder>`，并删除不需要的行。登录需要 `TIMEVAULT_REDIS_URL`（公司账号；用 `timevault-server check-redis-users` 检查）和/或一个本地管理员：`timevault-server hash-password` 会生成 `TIMEVAULT_ADMIN_PASSWORD_HASH` 的值。
3. 运行 `timevault-server migrate`（或设置 `TIMEVAULT_AUTO_MIGRATE=true`）和 `timevault-server serve`。打开 `http://127.0.0.1:8660/`；由管理员在**设置**页添加监控文件夹。
4. 如需让其他计算机使用，请在前面放置 HTTPS 反向代理，让服务监听回环地址，并设置 `TIMEVAULT_COOKIE_SECURE=true`。直接监听非回环地址需要 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，并且依然没有 TLS。能访问该地址的任何人都可以不用账号浏览每个监控文件夹的文件名、大小和历史，公司密码也是在这个网站上输入的，所以 HTTPS 不是可选项。登录限流统计的是连接过来的地址，所以放在反向代理之后时统计的是代理的地址，除非 `TIMEVAULT_TRUSTED_PROXIES` 列出了该代理（见[单点登录](#单点登录)和 [security.md](./security.md)）。当代理把 TimeVault 放在多个应用共用的主机名的某个路径下（例如 `/tv/`）时，见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)。
5. 服务没有 Windows 服务封装。请用你信任的方式启动它（例如计划任务或服务管理器）。0.1.8 用普通用户账号（没有管理员权限）的计划任务试过：它能启动服务，服务会一直运行，自动更新期间也是如此（见[发布与自动更新](#发布与自动更新)）；NSSM 之类的 Windows 服务封装没有试过。请关闭任务设置**如果任务运行时间超过以下时间，停止任务**（任务计划程序对新任务默认设为 3 天），否则时间一到服务就会被停止。收到 Ctrl+C 或 SIGTERM 时它会正常退出，最多等待 30 秒让 HTTP 服务和正在运行的任务结束。

启动时，服务会把上次进程结束时仍在运行的任务标记为失败（"interrupted by a service restart"），清除未完成恢复点的部分历史，然后继续处理排队的任务。

## 放在带路径前缀的反向代理之后

当一个主机名下有多个应用、TimeVault 只是其中之一时使用：例如 `http://prod_eng.co.murata.local/tv/` 与 `/qdb/`、`/mer/`、`/gitea/` 并列。直到 0.1.4，TimeVault 写出的每个地址都以斜杠开头（`Location: /explorer`、`/static/...`、表单、脚本发出的请求）。这在主机的根路径下是对的，在 `/tv/` 下就是错的：第一次重定向就会离开前缀，落到代理在主机根路径上提供的内容（在第一次部署中是门户页），页面也会失去样式。此时 TimeVault 的日志里只有一次请求，浏览器却已经在另一个应用上。自 0.1.5 起，服务知道自己的前缀；设计见 [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md)。

**开启方法**

1. 在 `.env` 中写入前缀：`TIMEVAULT_BASE_PATH=/tv`，然后重启服务。启动日志会写出 `serving under a path prefix`，设置页会显示**地址前缀**。不设置这一项则什么都不变。
2. 在代理中把 `/tv/` 转给本服务。服务既接受带前缀的请求，也接受不带前缀的请求，所以代理可以原样转发地址（推荐，最简单），也可以去掉前缀。
3. 紧挨着 TimeVault 的代理必须把浏览器的地址告诉它，并且必须列入 `TIMEVAULT_TRUSTED_PROXIES`（代理和服务在同一台电脑上时填 `127.0.0.1`）。下面的配置块适用于浏览器直接访问的代理；如果它前面还有另一层代理，请再加上[两层代理](#两层代理)中所说的内容。下面是用本版本运行过的 nginx 配置块：

   ```nginx
   location = /tv {
       return 301 /tv/;
   }

   location ^~ /tv/ {
       # No address after the port: the request goes on as it came, prefix included.
       proxy_pass http://127.0.0.1:8660;

       proxy_http_version 1.1;
       proxy_set_header Connection "";

       # TimeVault compares the Origin of a form post with Host.
       proxy_set_header Host $host;

       # The address of the browser, as this nginx saw it. Never $http_x_forwarded_for here: that passes on whatever
       # the browser sent, and TimeVault believes this proxy, so anyone could choose the address it records.
       proxy_set_header X-Real-IP $remote_addr;
       proxy_set_header X-Forwarded-For $remote_addr;
       proxy_set_header X-Forwarded-Proto $scheme;

       client_max_body_size 2m;   # TimeVault reads at most 1 MiB of a form
       proxy_read_timeout 300s;
   }
   ```

   先去掉前缀再转发请求的写法同样可行（`rewrite ^ $request_uri;` 和 `rewrite ^/tv(/.*) $1 break;`，配合 `proxy_pass http://127.0.0.1:8660$uri;`，这是 Gitea 常用的写法）。两种写法都在本版本上运行过。上面配置块中需要照抄的是地址相关的头：为第二层代理写的配置块里的 `X-Forwarded-For $http_x_forwarded_for`，会让浏览器自己选择 TimeVault 记录的地址。

**检查。** `curl -I http://<主机>/tv` 返回 `301`，`Location: http://<主机>/tv/`。`curl -I http://<主机>/tv/` 返回 `303`，`Location: /tv/explorer`（没有监控文件夹时是 `/tv/backups`）。在浏览器中，地址栏始终在 `/tv/` 之下，页面样式正常。审计页的**来源**列显示的是浏览器的地址，而不是 `127.0.0.1`。

**覆盖范围。** 页面的每个链接、表单和文件；每个重定向，包括 API 的（`POST /api/v1/sources` 的 `Location` 是 `/tv/api/v1/sources`）；任务进度的轮询；单点登录；以及 Cookie `tv_session`、`tv_lang` 和 `tv_flash`，它们被限制在 `/tv` 之内，所以同一主机名下的其他应用收不到它们。API 位于 `/tv/api/v1/...`。

**需要知道的**

- 服务仍然可以通过自己的地址打开：`http://127.0.0.1:8660/` 会导向 `/tv/...`，在那里页面同样保持在前缀之下。在那里手动输入不带前缀的地址，会显示对应页面，但浏览器不会把会话 Cookie 发给它，所以你看起来是未登录的，直到改用带前缀的地址。
- 前缀是人们收藏和发送的地址的一部分。以后再更改它会让这些地址失效。
- TimeVault 与该主机名下的其他应用共享源（协议、主机和端口）。给它单独的主机名或端口是更强的做法；见 [security.md](./security.md)。

## 两层代理

当提供 TimeVault 的 nginx 前面还有另一个 nginx 时使用。在第一次部署中，服务器 `s2`（`http://10.135.36.81/tv/`）把 `/tv/` 转发给 `prod_eng.co.murata.local` 的 nginx，后者再转发给 TimeVault：浏览器 → s2 → prod_eng → TimeVault。决定和备选方案见 [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md) 中的更新。

**会出什么问题。** 紧挨着 TimeVault 的 nginx 看到的是它前面那个 nginx 的地址，而不是浏览器的地址。用 `proxy_set_header X-Forwarded-For $remote_addr;` 时，它把这个地址写进该头，于是 TimeVault 把前面的服务器当成了浏览器。单点登录会拿这个地址与单点登录服务记录的地址比较，而浏览器是直接打开单点登录服务的，所以服务记录的是真实地址。结果每次单点登录都以"无法把这次登录与当前浏览器对应起来"（`address_mismatch`）结束。这一点已于 2026-11-17 用真实的 nginx 1.24 和两台服务器的 `/tv/` 配置块复现。只经过紧挨着 TimeVault 的 nginx（`http://prod_eng.co.murata.local/tv/`）时没有什么被隐藏，所以那样是正常的。

**如何设置。** 前面的 nginx 保持[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)中的配置块（`X-Forwarded-For $remote_addr`，这样浏览器无法自己选择地址）。紧挨着 TimeVault 的 nginx 必须从前面的 nginx 发来的头中取得浏览器的地址，并且不信任任何其他来源。在它的 `/tv/` 配置块里加上三行 `real_ip` 配置，写上前面的 nginx 连接过来所用的地址，配置块的其余部分保持不变：

```nginx
location ^~ /tv/ {
    client_max_body_size 512M;

    # The nginx in front: one line for each. Its X-Forwarded-For is believed, anybody else's is not.
    set_real_ip_from 10.135.36.81;
    real_ip_header X-Forwarded-For;
    real_ip_recursive on;

    # ... the rest of the block, as before (X-Forwarded-For $remote_addr included)
}
```

对于来自前面 nginx 的请求，nginx 现在会把 `$remote_addr` 设为浏览器的地址；来自其他任何来源的请求保持它连接时所用的地址，它发来的头会被忽略。`TIMEVAULT_TRUSTED_PROXIES` 保持不变（`127.0.0.1`，即紧挨着 TimeVault 的 nginx）。重新加载 nginx 即可，TimeVault 不需要重启。`set_real_ip_from` 后面的地址是前面的 nginx 连接过来所用的地址，是紧挨着 TimeVault 的 nginx 看到的那个（它的访问日志里有，被拒绝的单点登录的 `started_from` 里也有）；它不一定是人们输入的地址。

**另一种做法。** 在紧挨着 TimeVault 的 nginx 里改为追加而不是替换（`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`），把两个代理都写进 `.env`（`TIMEVAULT_TRUSTED_PROXIES=127.0.0.1,10.135.36.81`），并重启 TimeVault。TimeVault 取该头中最右边的、不是已列出代理的地址，所以浏览器在左边写的内容不会被信任。`real_ip` 的做法不需要改 `.env`，并且让那个 nginx 的访问日志显示浏览器的地址。

两种做法都在本版本上运行过，经过前面的 nginx 和直接访问，有伪造的 `X-Forwarded-For` 和没有时都试过：每一次 TimeVault 得到的都是浏览器的地址。

**如何检查。** 通过前面的服务器打开 TimeVault 并登录：**审计**页的**来源**列显示的是浏览器的地址，而不是某台服务器的地址。当单点登录因为地址被拒绝时，**登录失败**记录和服务日志里的一条警告会说明 TimeVault 看到了什么：`started_from`（它当作浏览器的地址）、`sso_address`（单点登录服务记录的地址）、`connection`（连接过来的地址：最近的代理）、`trusted_proxy`（该地址是否列在 `TIMEVAULT_TRUSTED_PROXIES` 中）和 `forwarded`（`X-Forwarded-For` 头，截断为 256 个字符）。如果 `started_from` 是某台服务器的地址，说明前面有代理把浏览器隐藏了：按上面的方法设置。如果 `trusted_proxy` 是 `false` 且设置了 `forwarded`，说明连接过来的代理不在 `TIMEVAULT_TRUSTED_PROXIES` 中。如果浏览器通过 IPv4 访问前面的服务器、通过 IPv6 访问单点登录服务（或者相反），也会被看成两个地址；这两个值能看出这一点。

**未验证。** 公司自己的服务器：以上步骤是在一台电脑上，用两个 `/tv/` 配置块的副本和代表服务器的回环地址运行的。

## 测试

`go test ./...` 运行全部测试。需要 PostgreSQL 的测试在 `TIMEVAULT_TEST_DATABASE_URL` 未指向某个数据库时会被跳过；每个测试都会创建名为 `tvtest_<random>` 的私有 schema 并在结束后删除，所以请使用单独的测试数据库，例如 `timevault_test`，不要使用正式使用的数据库。`dev-db.ps1` 会把私有集群的 URL 写入 `.env`；如果 `.env` 指向的是你自己的 PostgreSQL 服务器，请先在那里创建测试数据库（`CREATE DATABASE timevault_test OWNER timevault;`），并在 `.env` 中设置 `TIMEVAULT_TEST_DATABASE_URL`；该角色不需要其他权限。涉及联接点和 Windows 文件锁的测试只在 Windows 上运行。Everything 在线测试见 [Everything 搜索](#everything-搜索)。Redis 测试不需要 Redis：它们使用进程内的模拟服务和 Python 生成的 pickle 数据。

```powershell
. .\scripts\dev-env.ps1      # loads TIMEVAULT_TEST_DATABASE_URL (and the other TIMEVAULT_TEST_* settings) from .env
go test ./... -count=1
```

## 文件夹权限

账号能做什么是按监控文件夹分别决定的，而不是对整个服务器统一决定。决定和备选方案见 [ADR 0013](./adr/0013-per-folder-access-levels.md)。

**级别。** 每个级别都包含它前面的级别。

| 级别（页面上的名称） | 可以 |
|---|---|
| 浏览 | 查看文件夹：文件、历史、版本、搜索、备份的状态、它的任务、与它有关的审计事件。从中恢复（需要已登录的账号；副本会写入恢复文件夹之下）。 |
| 备份 | 另外：**立即备份**、**完整重读**、**校验**恢复点，以及停止该文件夹的任务。 |
| 完全控制 | 另外：修改文件夹的设置（名称、显示文件夹、排除规则、启用或停用、计划、保留的版本数）以及谁能对它做什么。不能修改服务器上的文件夹，也不能添加文件夹：这两项只有管理员能做。 |

**级别授予谁。** 可以授予**访客（未登录）**（只能是"浏览"）、**所有已登录的人**，以及按工号指定的个人。已登录的账号拥有这些授权所给的最高级别，并且永远不会比访客权限更低。管理员（`admin` 账号和本地管理员）无需被列出，就对所有文件夹拥有完全控制权。启动了恢复的人也可以停止它。

**如何设置。**

1. 管理员在**设置**页添加文件夹（**添加文件夹**）。表单中有一个**谁能做什么**部分，包含三个列表：浏览、备份和完全控制。把**访客（未登录）**和/或**所有已登录的人**勾选在合适的列表中，或者勾选具体的人。人员按姓名和工号列出（来自用户目录的账号；在输入框中输入可以查找；已勾选的人排在最前面）。同时出现在多个列表中的人，取最高的级别。添加时没有勾选任何人的文件夹，只有管理员能看到。
2. 之后要修改时，打开**设置**，点击该文件夹的**编辑**（只有对其拥有完全控制权的文件夹才有这个按钮），修改列表，再点击**保存更改**。更改对每个访问者的下一次请求生效。
3. 表单强制执行的规则：访客只能出现在"浏览"列表中；在同一个列表里，"访客"和"所有已登录的人"不能互相搭配，也不能与指定的人员同时选择（它们已经包含了这些人）；添加时每个指定的工号都必须是用户目录中有效的账号（已经有权限的账号即使之后被停用、或者目录当前无法访问，也会保留，表单中会显示为"不在用户目录中"）；每个文件夹最多可以指定 500 个人。拥有完全控制权但不是管理员的人，如果保存的列表不再让他们拥有完全控制权，会先被要求勾选**我知道之后我将不能再修改这个文件夹（管理员可以把权限重新授予我）**（在 API 中：在 `confirm_lockout` 为 `true` 之前返回 `409 lockout_not_confirmed`）。如果在表单打开期间，别人修改了谁可以对该文件夹做什么，而你也修改了列表，就不会保存任何内容：表单会带着提示**在你编辑期间，有人修改了谁可以对这个文件夹做什么**、列表现在的样子和你已输入的其他内容重新出现；请检查它们，再做一次你的修改，然后保存（在 API 中：与 `access` 一起发送 `access_rev` 时返回 `409 access_changed`）。没有动过列表的表单永远不会改变它们，所以只保存计划时，别人所做的修改会保留。在"查找人员"的输入框中按回车不会保存表单。
4. 通过 API 也一样：`POST /api/v1/sources`（管理员）和 `PATCH /api/v1/sources/{id}`（完全控制权）中的 `access`，见 [api.md](./api.md)。人员写成工号，或者 `user:<工号>`（当工号拼写为 `guest` 或 `everyone` 时必须这样写）。

**没有任何级别的访问者看到的是：什么都没有。** 这个文件夹不会出现在文件浏览或时间机器的选择框中，不会出现在备份、设置或审计页面上，也不会出现在 API 中；它的任务和审计事件也不会显示；直接请求它会得到与请求不存在的文件夹相同的"未找到"（未登录的访问者会被告知登录后也许能看到）。能看到文件夹、但无权执行某项操作的访问者会看到"你没有权限执行此操作"，这次尝试会作为**访问被拒绝**记入审计日志，并带上该文件夹和所需的级别（`needs`）。

**示例。**

| 想要的效果 | 浏览 | 备份 | 完全控制 |
|---|---|---|---|
| 任何人都可以查看，已登录的人可以备份（到 0.1.6 为止每个文件夹都是这样） | 访客 | 所有已登录的人 | |
| 公司内部都可以看、由团队自己管理的团队文件夹 | 所有已登录的人 | 团队成员 | 负责人 |
| 私有文件夹 | 可以看到它的人 | | 负责人 |

**升级到 0.1.7。** 用 `timevault-server migrate` 应用迁移 `0005`（或者用 `TIMEVAULT_AUTO_MIGRATE=true` 启动服务）。它会给每个已有的文件夹设置表中的第一行（访客可以浏览，所有已登录的人可以备份），这正是到 0.1.6 为止所有人都能做的事：在管理员逐个收紧之前，已有的文件夹没有任何变化。之后添加的文件夹在有人被授权之前没有任何访问权限。**不要让 0.1.6 回头去使用这个数据库：** 0.1.6 不认识这张表，所以它会再次让每个访问者看到每个文件夹，让每个已登录账号对它们备份和恢复；如果必须回退，请把升级之前的数据库放回去。

**检查。** 用另一个账号登录（或者以访客身份查看文件夹列表）：文件浏览和备份页面恰好列出它可以浏览的文件夹，设置页在每个名称旁边显示级别。`GET /api/v1/sources` 的每个文件夹都有 `level`。审计页显示列表的每一次变更：**修改监控文件夹**，其中的 `access` 带有旧值和新值；只有对该文件夹拥有完全控制权的人才能读到这些列表，对其他人显示为 `[hidden]`。如果某人看不到本该看到的文件夹，请检查他的工号是否在某个列表中（或者是否勾选了**所有已登录的人**），以及他的账号是否有效；未登录的访问者只有**访客**所给的权限。

## 单点登录

可选。在公司里已登录 Windows 的人可以不输入密码就登录 TimeVault，使用的是与 dash 应用相同的公司单点登录服务。设计及其限制见 [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md)。

**如何开启**

1. 把单点登录服务的地址写入 `.env`：`TIMEVAULT_SSO_HOST=<sso.example.local>`（`host`、`host:port` 或 `http(s)://host[:port]`）。因为地址可能变更，所以它只有一行。重启服务。登录页现在有按钮**使用 Windows 账号登录（SSO）**，启动日志和设置页会显示单点登录服务（`sso_host`）。没有这个设置时，登录页与以前一样。
2. 单点登录服务必须把它的结果（`sso_login_<token>`）写入 `TIMEVAULT_REDIS_URL` 所指的 Redis（相同的数据库编号），并且 TimeVault 的 Redis 账号必须被允许读取它们。TimeVault 只读取它们；它从不删除这个键，单点登录服务会在 5 分钟后让它过期。
3. 在反向代理之后（通常的情况），二选一：
   - **推荐。** 把代理列在 `TIMEVAULT_TRUSTED_PROXIES` 中（用逗号分隔的地址或 CIDR 范围）。代理必须把浏览器的地址放进 `X-Forwarded-For` 头，并且原样传递 `Host` 头，因为"请求来自站点本身"的检查会比较 `Origin` 和 `Host`。这样 TimeVault 也会把真实地址写入审计日志的**来源**列，并按浏览器而不是按代理统计登录尝试。测试过的 nginx 配置块见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)。
   - 设置 `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`。这样就不再拿浏览器的地址与单点登录服务看到的地址比较。这样做较弱；见 ADR 0009。

   两者都没有时，每次单点登录都会以"无法把这次登录与当前浏览器对应起来"结束，因为 TimeVault 拿代理的地址与浏览器的地址比较。

   在直接访问 TimeVault 的开发机上（例如 `http://localhost:8660`）也会这样：TimeVault 看到的是回环地址（`127.0.0.1` 或 `::1`），而单点登录服务看到的是这台电脑的网络地址。请改用经过代理的站点地址访问，或者只在这台电脑上设置 `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`。

**人们会看到什么。** 按钮会打开一个带有单点登录页面的小窗口，登录页显示"正在等待 Windows 登录"。几秒钟后窗口自行关闭，此人登录成功，拥有 Redis 中该账号的角色（`admin` 或不是），并回到他来时的页面。出问题时页面会说明原因：浏览器拦截了窗口（请允许本站弹出窗口）、Windows 无法识别你的身份、你的 Windows 账号在这里没有可用的账号、无法把这次登录与当前浏览器对应起来、登录耗时过长或已被使用、窗口被关闭，或无法连接用户账号服务。密码表单照常可用。

**如何检查。** 打开登录页并点击按钮。**审计**页随后会显示**已登录**，带有 `method: sso` 和 Windows 用户；被拒绝的尝试显示**登录失败**，带有 `reason`（`no_active_account`、`no_windows_user`、`address_mismatch`、`browser_address_changed` 或 `result_unreadable`）。对于与地址有关的两种原因，它还会显示比较的两个地址和请求是怎样到达服务器的（`started_from`、`sso_address`、`connection`、`trusted_proxy`、`forwarded`；见[两层代理](#两层代理)），服务日志里也有一条包含同样内容的警告。

## Everything 搜索

搜索是可选的。TimeVault 向**本机的 Everything 实例**（Everything 1.4 的 HTTP 服务）询问，在已登记的备份源之下*此刻*存在哪些文件和文件夹。它不是历史搜索：浏览过去的状态使用目录库。没有 Everything 时，搜索框显示"未配置"或"不可用"，其他功能照常工作。

**如何设置**

1. 使用启用了 HTTP 服务的 Everything 1.4.x：只绑定到 `127.0.0.1`（适配器拒绝任何其他主机），用用户名和密码保护，并**关闭**"允许文件下载"（`http_server_allow_file_download` 默认是开启的，而 TimeVault 并不需要它）。专用的命名实例比日常使用的实例更好；开发时 `scripts\dev-everything.ps1` 会为你创建一个。
2. 设置 `TIMEVAULT_EVERYTHING_URL`（例如 `http://127.0.0.1:<port>/`），如果设置了凭据，还要设置 `TIMEVAULT_EVERYTHING_USERNAME` 和 `TIMEVAULT_EVERYTHING_PASSWORD`。并确保 Everything 已为备份源文件夹建立索引。
3. 打开**文件浏览**并在搜索框中输入。**状态**页会把搜索引擎显示为 `ok`、`degraded`（可达但应答错误或过慢）或 `disabled`。

**为你已经在运行的 Everything 启用搜索。** 在设置 `TIMEVAULT_EVERYTHING_URL` 之前，搜索一直是"未配置"；而正在运行的 Everything 在它的 HTTP 服务被打开之前（默认是关闭的）不会通过 HTTP 应答，所以两件事都要做：

1. 在 Everything 中打开**工具 → 选项 → HTTP 服务器**（相同的设置也是 `Everything.ini` 中的 `http_server_*` 键）：勾选**启用 HTTP 服务器**，选一个空闲的端口（例如 `38480`），只绑定到 `127.0.0.1`（`http_server_bindings`），设置用户名和密码，并把**允许文件下载**关掉（`http_server_allow_file_download=0`）。应用设置。Everything 必须一直运行，并且要为监控文件夹所在的磁盘建立索引（NTFS 卷，或这些文件夹本身）。
2. 把下面几行写入 `.env`（使用你自己的端口、用户名和密码）：

   ```env
   TIMEVAULT_EVERYTHING_URL=http://127.0.0.1:38480/
   TIMEVAULT_EVERYTHING_USERNAME=<username>
   TIMEVAULT_EVERYTHING_PASSWORD=<password>
   ```

3. 重启 TimeVault 服务，打开**状态**页（搜索引擎应为 `ok`），然后在**文件浏览**中搜索。如果显示"不可用"，请在浏览器中打开 `http://127.0.0.1:38480/`：它应该要求输入用户名和密码，并显示 Everything 的页面。

**一次查询会经历什么**

- 输入的文本会被拆分成词（空白分隔，双引号表示一个短语；最多 256 字节和 16 个词）。`*` 和 `?` 被当作分隔符，因为即使在引号里 Everything 仍把它们当作通配符。
- 每个词都会被加上双引号，所以 `|`、`!`、`<>`、`regex:`、`size:`、`content:` 这样的 Everything 操作符都被当作普通文本来搜索。
- 查询通过 `path:"<备份源根目录>\"` 限定在备份源之内。末尾的反斜杠很重要：`path:` 是普通的子串匹配，没有它的话，备份源 `D:\src` 也会匹配 `D:\src2`。没有使用 `infolder:`，因为它只匹配直接子项。
- 每个结果都会再次对照备份源根目录检查。范围之外的结果会被丢弃并计数，绝不会显示。
- 每页最多 200 条结果（可选 50、100 或 200），带页码链接；结果不按类型排序或筛选，因为 Everything 每次按它自己的顺序只返回一页。Everything 没有索引文件夹大小时，文件夹大小显示为未知。

**文件浏览中的文件夹大小**（0.1.4）。文件浏览会向 Everything 询问当前文件夹里的各个文件夹，并显示它们的大小（其中所有内容，递归计算），所以文件夹行的大小列不再是空的，文件夹也可以按大小排序。文件夹的修改时间仍然是它自己的，从磁盘读取，和以前一样。请在 Everything 中开启 **工具 → 选项 → 索引 → 索引文件夹大小**（并保持该卷处于监控状态）；没有它，大小单元格显示"-"。答案来自 Everything 的索引，可能比磁盘稍微滞后。TimeVault 会把一个答案保留 10 秒，最多等待 2 秒，当 Everything 没有响应时显示"-"，并且 15 秒内不再询问，所以缓慢或已停止的 Everything 不会让页面慢多少。时间机器和版本页面读取的是目录库，不显示文件夹大小。同一个 Everything 还可以告诉调度器某个文件夹没有变化；见[调度器设置](#调度器设置)。

**开发用实例**（`scripts\dev-everything.ps1`，需要已安装 Everything 1.4.x；找不到时请把 `TIMEVAULT_EVERYTHING_EXE`（环境变量或 `.env`）指向 `Everything.exe`）：

| 动作 | 效果 |
|---|---|
| `-Action start` | 用 `data\everything\bin` 中 `Everything.exe` 的私有副本启动名为 `TimeVault` 的实例，它的 ini 和数据库放在 `data\everything`。它只把 `-IndexRoot`（默认 `data\dev\source`）作为文件夹索引来建立索引，监听 `127.0.0.1:38480`（可用 `-Port` 修改），关闭文件下载，不显示托盘图标，也不保留历史记录。随机凭据保存在 `data\everything\http-credentials.txt`；服务所需的设置（`TIMEVAULT_EVERYTHING_*`）和在线测试所需的设置（`TIMEVAULT_TEST_EVERYTHING_*`）写入 `.env`。 |
| `-Action stop`、`status`、`reset` | 停止它（只停止它自己的进程）、显示它是否应答，或停止并删除它的 ini 和数据库，并从 `.env` 中移除它的设置。 |
| `-Action fixture` | 在 `-IndexRoot` 之下创建在线测试使用的测试目录树。 |

该脚本绝不会触碰你日常使用的 Everything 实例，也绝不会写入 Everything 的安装文件夹。它是在普通（非提升权限）的 shell 中测试的。`start` 之后，请重启 TimeVault 服务，使其读取 `.env` 中的新设置。

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

## 调度器设置

自动备份按文件夹在**设置**页设置（"自动备份，间隔 N 分钟"）。下面这些设置只决定到期的备份如何错开，这样有大约 10 个监控文件夹的服务器就不会一次把它们全部扫描。原因见 [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md)。

- **一次一个。** `TIMEVAULT_SCHEDULER_PARALLEL=1`（1 到 4）。用**立即备份**启动的备份不受限制，也不计入。
- **间隔。** `TIMEVAULT_SCHEDULER_GAP=auto` 是刚好跟得上所有计划的间隔，`1 / Σ(1 / 间隔)`，限制在 30 秒到 10 分钟之内：十个间隔为 60 分钟的文件夹每 6 分钟启动一个。可以用 `5m` 这样的时长固定它；`0s` 关闭间隔。
- **休息。** `TIMEVAULT_SCHEDULER_REST=auto` 在一次运行之后等待与它所花时间一样长的时间（30 秒到 10 分钟），所以大文件夹的长时间扫描之后会有一段长时间的停顿。下一次启动时间取"上次启动之后的间隔"和"上次结束之后的休息"中较晚的那个。`0s` 关闭它。只在 `TIMEVAULT_SCHEDULER_PARALLEL` 为 1 时生效。
- **顺序。** 没有恢复点的文件夹先来，然后是相对于自己的间隔超期最多的，再然后是上次运行最快的，最后是 id 最小的。上次运行花了多久显示在设置页上（"上次耗时"）。
- **优先级。** 默认是 `TIMEVAULT_PROCESS_PRIORITY=below_normal`，这样备份扫描会让位于同一台电脑上的文件共享。`background` 还会降低磁盘优先级；`normal` 就是 0.1.4 之前的样子。
- **Everything 探测。** `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE=true` 让调度器先问 Everything，并跳过没有变化的文件夹的扫描。它需要 `TIMEVAULT_EVERYTHING_URL`，需要一个监控着该文件夹所在卷（NTFS 卷，通过它的变更日志）并开启了 **工具 → 选项 → 索引 → 索引最近更改日期** 的 Everything，并且该文件夹在这个索引之内。在以下情况下文件夹仍然会被扫描：Everything 3 秒内没有响应或不认识该文件夹；其下的条目数量不等于上次完整扫描时的数量（删除就是这样被发现的）；自上次扫描前 5 分钟以来其下有任何内容被创建、修改、重命名、移动或更改了属性；最新的恢复点有未捕获的项目；自上次扫描以来更改过该文件夹的排除规则；以及至少每隔 `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY`（24 小时）。一个周期最多询问 Everything 25 次，没有应答的 Everything 在一分钟内不会再被询问，所以已停止的 Everything 只造成一次超时，而不是每个到期文件夹各一次。设置 `TIMEVAULT_LOG_LEVEL=debug` 后，每次跳过都会在日志中写下"scheduled scan skipped: the search engine sees no change"。被跳过的文件夹不会在列表中留下任何内容，就像任何没有发现变化的运行一样，它的间隔会重新开始计算。
- 间隔状态保存在内存中：重启后，第一个自动备份可能立即启动。

## 发布与自动更新

`.env` 中设置了 `TIMEVAULT_UPDATE_URL` 的服务器，会在发布了更新的版本时替换自己的程序：它等正在运行的任务结束，交换文件，然后重启，不需要任何人守在服务器旁。决定和备选方案见 [ADR 0014](./adr/0014-self-update-from-a-release-manifest.md)。

**制作发布（在构建用的电脑上）。**

```powershell
cd <repository folder>
.\scripts\release.ps1
.\scripts\release.ps1 -OutDir D:\TimeVault\release -BaseUrl https://tools.example.local/tv-release/
```

- 先提高 `cmd\timevault-server\main.go` 中的 `var version`。服务在清单中的版本**不是**它正在运行的版本时才会更新，所以与上一个发布版本号相同的程序不算更新（脚本发现这种情况会发出警告）。
- 脚本会运行 `go vet ./...`（用 `-SkipChecks` 跳过），用临时名称构建程序（`-trimpath -ldflags "-s -w"`，不带 `-X`），向它询问版本，测量大小并计算哈希，把它作为 `timevault.exe` 放入发布文件夹（默认是 `<repository>\release`，已被 git 忽略），并**最后**以不带字节顺序标记的 UTF-8 原子地写入 `timevault.json`：

  ```json
  {
      "latest_version":  "0.1.8",
      "sha256":  "<64 hexadecimal characters>",
      "package_url":  "timevault.exe",
      "size":  23352320
  }
  ```

  `package_url` 是文件名，服务会在它读取的清单旁边查找（适用于文件夹、共享，或同时存放这两个文件的 Web 文件夹）；使用 `-BaseUrl` 时则是程序的完整地址。手写的清单也可以：版本前面的 `v` 和未知字段都会被接受。
- 把 `timevault.exe`，再把 `timevault.json`，复制到服务器读取的**发布文件夹**。服务在复制过程中读取该文件夹时，会看到大小或哈希不一致，并在下次查看时再试；它绝不会安装只复制了一半的文件。

**服务器从哪里读取。** `TIMEVAULT_UPDATE_URL` 可以是：

- 文件夹或共享，`D:\TimeVault\release` 或 `\\server\share\tv-release`（运行服务的账号需要有读取权限；服务启动时无法访问的共享，在能访问之后就会被找到）；或者
- 清单或其所在文件夹的 `http(s)` 地址，`https://tools.example.local/tv-release/`，由任何能原样提供这两个文件的 Web 服务器提供，例如 nginx：`location /tv-release/ { alias D:/TimeVault/release/; }`。会跟随重定向（至多五次）。`200` 以外的应答（例如 `202` 或 `404`）都是错误。

**在服务器上启用（只需一次）。**

1. 把 0.1.8 或更新版本的 `timevault.exe` 复制到服务器上，放在 `.env` 旁边。这是最后一次手工复制：更早的版本不能自己更新。
2. 在 `.env` 中写入：`TIMEVAULT_AUTO_MIGRATE=true`（有更新地址的服务没有它会拒绝启动，因为服务自己更新时没有人会运行 `migrate`）、`TIMEVAULT_UPDATE_URL=<发布文件夹>`，如果需要，再加 `TIMEVAULT_UPDATE_INTERVAL=5m`。
3. 运行服务的账号需要对 `timevault.exe` 所在的文件夹有**修改**权限（`timevault.new.exe`、`timevault.old-<版本>.exe` 和 `timevault.exe.update.json` 都写在那里），并对发布文件夹有**读取**权限。
4. 启动服务。启动日志会写出 `automatic updates are on` 以及地址和间隔，并在程序所在的文件夹不能写入新文件时发出警告。**设置**页有一张**程序更新**卡片：状态、已发布的版本、清单最近一次读取的时间，以及（仅管理员能看到的）发布地址、最近的问题和**立即检查**。

**发布了新版本之后会发生什么。**

1. 在下一次查看时（在间隔之内；管理员点击**立即检查**或调用 `POST /api/v1/update/check` 时立即查看；启动后 10 秒也会查看），服务发现一个不是正在运行的版本。它把程序下载到 `timevault.new.exe`，检查大小和 sha256，并用 `version`（必须输出清单中的版本）和 `check-config`（必须接受本服务的设置）运行它。
2. 它停止取用任务：设置页显示**等待正在运行的任务**及其数量，每个已登录的账号都会在页眉下面看到提示（_版本 0.1.8 将在正在运行的任务结束后安装。新任务会等到那时再开始。_）。正在运行的任务继续运行到结束；新的任务（包括调度器的）在队列中等待。没有时间上限。
3. 没有任务运行时，它把正在运行的 `timevault.exe` 重命名为 `timevault.old-<版本>.exe`，把新文件放到 `timevault.exe` 这个名字上，写下说明文件，停止调度器、Web 服务器和任务运行器，然后启动新程序。之后排队的任务开始运行。
4. 新程序在日志中写出 `automatic update: this program has taken over`，并写入审计事件；**审计**页会列出操作者为 `updater` 的**程序更新**事件（`staged`、`restart`、`done`，以及 `cancelled` 或 `failed`），详情中没有路径，也没有错误文本（它们在服务的日志中）。

如果发布又回到了正在运行的版本，等待就会结束，任务会重新被取用。出现问题（读不到清单、程序的大小、哈希或版本不对、新程序拒绝这些设置、交换失败）时，会显示在设置页上（文本只给管理员看），在日志中只记录一次，并在下次查看时再试；服务继续使用旧程序运行。大小或哈希不对也会再试，因为还在复制中的发布就是这个样子；版本与清单不一致的程序在清单改变之前不会再试。对 `http(s)` 地址的下载如果两分钟内没有收到任何数据就会放弃；下载之后被移除的程序（例如被病毒扫描程序删除）会在下次查看时重新下载。

**更新之后怎样启动程序（`TIMEVAULT_UPDATE_RESTART`）。**

- `supervise`（默认）：被启动的那个进程作为一个小型**启动器**继续存在，并把新程序作为子进程运行，参数、环境变量、文件夹和控制台都与之前相同。在控制台窗口中 Ctrl+C 会同时停止两者；作为计划任务时，任务始终只有一个运行中的进程，也不需要"失败时重新启动"。启动器以其他任何方式结束时，例如任务被结束（任务计划程序会立即强制终止进程），程序会发现启动器已经不在，有序关闭并释放端口，所以结束任务不会留下任何东西。启动器是最先被启动的那个程序，它的代码保持不变：更新替换的是磁盘上的文件，而不是启动器，所以要改动启动器本身，需要重启启动器。它活着的时候，它所运行的那个文件（`timevault.old-<版本>.exe`）会一直被锁定。
- `exit`：进程以退出码 **75** 结束，启动它的东西必须再次启动它。由服务管理器运行程序时使用它，例如设置了 `AppExit 75 Restart` 的 NSSM（没有试过：测试时没有 NSSM）。使用 Windows 任务计划程序时，触发器每分钟重复一次并选择**不要启动新实例**（`MultipleInstances IgnoreNew`）的任务，会在下一次重复时启动新程序，而程序运行期间的重复不会启动任何东西：这一点试过。"如果任务失败，每隔……重新启动"这个设置也试过，它在程序以 75 结束之后**没有**再次启动程序，所以不要依赖它。由启动器启动的程序无论设置如何，始终以 75 退出，因为启动器在等待这个退出码。

**回退。** 上一个程序是 `timevault.old-<版本>.exe`（保留最新的三个）。**请先把较旧的发布放回发布文件夹，或者清空 `TIMEVAULT_UPDATE_URL`：** 否则服务会在下次查看时再次更新到较新的版本，因为清单中的版本不是正在运行的版本。

- 如果新程序在运行，把较旧的发布放进去就够了：服务会以同样的方式退回去（版本不同就是更新，不分方向）。
- 如果新程序无法启动，请停止剩下的进程（启动器），重命名 `timevault.exe`，把 `timevault.old-<版本>.exe` 复制为 `timevault.exe`，然后启动。
- 新版本已经应用的迁移会保留在数据库中。请阅读 [changelog.md](./changelog.md) 中该版本的升级说明（0.1.7 说明不能让 0.1.6 对它的数据库启动），在说明要求时恢复升级之前的数据库。

**限制与信任。**

- **发布文件夹决定了服务器上运行什么。** 没有签名：大小和 sha256 只能防止文件只复制了一半或损坏，防不住能同时写入这两个文件的人。请只让管理员能写入该文件夹（及其共享），Web 地址请使用 `https`。见 [security.md](./security.md)。
- 更新会无限期地等待正在运行的任务结束。如果重启不能等，请手工停止服务。
- 版本只比较是否相等，不比较先后。
- `check-config` 检查的是设置，而不是外部环境（它不连接任何东西）。通过它的程序之后仍可能失败，例如在迁移中：这时启动器以程序的退出码结束，原因在日志中；旧程序仍然保留着。
- 程序所在文件夹中留下的文件：`timevault.new.exe` 和 `timevault.new.exe.part`（正在进行的下载；下次启动时删除）、`timevault.old-<版本>.exe`（最新的三个；仍在使用的那个会在之后删除）、`timevault.exe.update.json`（给新程序的说明文件；它启动时删除）。

## 账号头像

页眉、审计日志、任务页和任务列表、文件夹访问权限的摘要，以及权限编辑器中的人员列表，都会在账号名称旁边显示该账号的头像。头像从 `TIMEVAULT_AVATAR_URL` 读取，默认是 `/api/static/hr/{id}.webp`（公司其他应用保存的员工照片；`{id}` 是工号）。

- 该地址是站点所在主机上的一个路径，不会放到 `TIMEVAULT_BASE_PATH` 之下：在公司的 nginx 之后，`/api/` 和 `/tv/` 在同一个主机名上，所以浏览器可以在 `/tv/` 旁边找到 `/api/static/hr/E1001.webp`。`off` 表示关闭头像。
- 浏览器自己去请求每张头像，页面存活期间每张只请求一次，并且只请求即将被看到的头像（权限编辑器的人员列表很长）。没有头像的人（该应用对此回答 `202` 并附带一张占位图，而不是 `404`）、失败的请求和不是图片的应答，都会保留默认头像；缺失的头像在浏览器会话期间会被记住。
- 本地管理员、服务自己使用的名称（`scheduler`、`updater`、`system`）以及不可能是工号的名称，都不会去请求头像。TimeVault 不存储任何东西，内容安全策略也没有改变（头像是用请求返回的数据显示的）。
- 直接通过服务自己的端口打开时，没有 `/api/static/hr/`，所以每个人显示的都是默认头像。

## 运维

- **谁可以做什么。** 任何人无需登录就可以浏览允许访客浏览的文件夹（文件浏览、时间机器、文件版本、备份页和备份任务的状态）；不允许的文件夹对访问者来说根本不存在。其他一切都需要已启用的账号，而账号能对某个文件夹做什么是按文件夹分别决定的：**浏览**（也包括恢复，以及该文件夹的任务和审计事件）、**备份**（立即备份、完整重读、校验、停止任务）和**完全控制**（修改文件夹的设置以及谁能对它做什么）；见[文件夹权限](#文件夹权限)。审计日志（账号可以浏览的文件夹的事件，以及登录等不属于任何文件夹的事件）、状态页和设置页对每个已登录的账号开放。只有 `admin` 账号（Redis 中 `Type` 为 `admin`，或本地管理员）才能添加监控文件夹，并对所有文件夹拥有完全控制权，也只有管理员能看到服务器磁盘上的文件夹；其他人看到的是显示文件夹。见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)、[ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md)、[ADR 0013](./adr/0013-per-folder-access-levels.md) 和 [api.md](./api.md#谁可以调用什么)。
- **浏览很长的文件夹。** 文件浏览和时间机器把文件夹分页列出（默认每页 100 项；可选 50、100、200 或 500，列表上方和下方有数量和页码链接）。**类型**列以大写显示扩展名（文件夹显示"文件夹"，没有扩展名的文件显示"文件"）；列表上方的**类型**下拉框只保留一种（所有文件夹、所有文件、没有扩展名的文件，或某一种扩展名，每项都带数量）。点击**名称**、**类型**、**大小**或**修改时间**会对整个文件夹排序，再点一次反向；文件夹始终排在最前面。你的选择写在地址里，所以链接会保留它。时间机器中的**全选**选中当前页的项目，一次恢复最多可带 500 个路径。在较长的页面上，右下角的两个圆形按钮可以跳到顶部和底部。包含 10,000 个文件的文件夹约 0.1 秒打开，包含 50,000 个文件的约 0.5 秒（在开发机器上测得，所有文件都在同一个文件夹里）；单个文件夹中有几十万个条目时需要几秒钟，因为每一页都会读取整个文件夹。
- **健康检查。** `GET /healthz` 无需登录即返回 `ok`，只能证明进程在运行。**状态**页（仅限账号）检查数据库、仓库（写入测试和剩余空间；剩余不足 5% 时为"降级"）、搜索引擎、Redis 用户目录（没有应答时为"降级"：浏览、已有会话和本地管理员仍可使用）、恢复目录和任务队列，并把 VSS 和 USN 列为"已禁用"。没有会话时，只有 `/healthz`、登录页、语言切换、静态资源和浏览页面可以访问。
- **监控文件夹。** 管理员在**设置**页添加（本地文件夹；不能与仓库、恢复目录或其他监控文件夹重叠；同时填上它的显示文件夹，见[设置放在哪里](#设置放在哪里)；并说明谁可以看到和使用它，见[文件夹权限](#文件夹权限)）。管理员，或对该文件夹拥有完全控制权的人，可以随时编辑名称、显示文件夹、排除规则、启停开关、计划、保留的版本数和权限。服务器上的文件夹本身，管理员在第一个恢复点出现（任何状态，包括失败的）之前可以更正；之后请停用旧的并添加新的。停用的文件夹保留历史，仍可浏览，但不能被备份。
- **备份。** 对某个文件夹拥有"备份"（或"完全控制"）权限的已登录账号，可以在**备份**页点击**立即备份**。普通备份依据大小和修改时间跳过未变化的文件；**完整重读**会重新读取并哈希每个文件。每个备份源同一时间只运行一个备份。无法读取的文件（被锁定、读取时发生变化、链接、特殊文件）会被记录为"跳过"并写明原因，保留之前的良好版本，备份本身仍以警告完成。
- **自动备份。** 为文件夹勾选"自动备份，间隔 N 分钟"（1 到 43200）。服务每 15 秒查看一次；文件夹从未运行过，或上次运行结束已满 N 分钟（无论结果如何）时到期，而没有发现变化的运行不记录任何内容：没有恢复点、没有任务、没有审计事件。设置计划或重新启用文件夹时，如果上次运行早于间隔，会立即启动备份。到期的文件夹会一次一个、错开时间启动（见[调度器设置](#调度器设置)），所以文件夹可能比它到期的时刻晚一些启动。没有每个文件的版本数上限（下一项）时，对一直在变化的文件夹使用很短的间隔，会让仓库无限增长。见 [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md) 和 [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md)。
- **每个文件保留的版本数。** 每个监控文件夹有一个上限（设置页的"每个文件保留的版本数"，1 到 10000；0 或留空表示全部保留）。每次备份之后，会检查它改动过的文件：当一个文件的版本多于上限时，它较旧的版本会被永久删除，并释放没有版本再需要的仓库内容（只在没有备份运行时进行，所以空闲空间的变化可能要过一阵子才看得到）。失去版本的恢复点会在备份页和时间机器页上被标记为**不完整**：它不再显示其版本已被删除的文件，所以从它恢复可能不完整，校验它时会检查对象但不检查清单。对已有历史的文件夹设置上限或调低上限，会为整个文件夹排入一个**版本清理**任务；调低时会先要求勾选（"我知道这些版本会被永久删除"），旁边会给出将被删除的版本数和文件数。当备份之后立即进行的删除无法运行时（因为该文件夹的恢复或校验排队或正在运行，或者它失败了），也会排入一个清理任务：否则它会被遗忘，因为下一次备份只查看它自己改动过的文件。排队的清理不占用 worker；它在该文件夹没有备份、恢复或校验运行时开始，运行期间该文件夹的备份在队列中等待。清理运行期间，该文件夹的恢复和校验会被拒绝，更改它的上限也会被拒绝；在此之前已排队的恢复或校验会等到它结束。审计日志记录**已删除旧版本**。见 [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md)。
- **校验。** 对恢复点运行校验任务，会对照清单摘要检查目录库，并检查每个被引用的对象都存在且大小正确；"深度"校验还会重新哈希内容。复制或恢复仓库之后请运行一次。
- **恢复。**
  1. 登录后打开**时间机器**，选择备份源和恢复点，然后勾选文件或文件夹并选择**恢复所选项…**，或选择**恢复此文件夹…** / **恢复全部内容…**。单个文件的版本页面有**恢复…**。未登录的访问者看到的是一个**登录后可恢复文件**的链接。能浏览某个文件夹的人，就可以从它恢复。
  2. 查看预览：数量、大小、冲突、受阻项目（内容缺失、不安全的名称、从未备份成功）以及计划的变更。
  3. 选择目标文件夹名称（`TIMEVAULT_RESTORE_ROOT` 之下的文件夹，不存在时会创建）、冲突策略（跳过、改名、覆盖），以及在接受只恢复所选内容的一部分时勾选的部分恢复选项。覆盖需要额外确认。
  4. 在预览后 15 分钟内点击**开始恢复**（令牌会过期，并与你的用户和精确的计划绑定；任何更改都需要重新预览）。在任务页面查看进度。在它结束之前，不要向同一个文件夹再启动另一个恢复。请自行把结果从目标位置复制到最终位置：预览和任务页面用显示文件夹（`<TIMEVAULT_RESTORE_DISPLAY_ROOT>\<destination>`）来称呼它，你可以通过网络在那里找到它；管理员还会看到 `<TIMEVAULT_RESTORE_ROOT>\<destination>`。恢复绝不会写入备份源。
- **审计。** **审计**页（仅限账号）列出登录和登录失败（账号名按输入的内容记录，截断到 64 个字符；单点登录会显示登录方式和 Windows 用户）、被拒绝的请求、监控文件夹的变更（每个发生变化的字段的旧值和新值，包括权限列表）、备份、校验、恢复预览与结果、旧版本的删除、取消操作，以及程序更新的各个步骤（**程序更新**，操作者为 `updater`；见[发布与自动更新](#发布与自动更新)）。管理员能看到全部；其他人看到的是他们可以浏览的文件夹的事件，以及不属于任何文件夹的事件（登录等）。**耗时**列显示备份、校验、恢复或清理运行了多久，对备份还显示它所复制数据的速度：大文件夹的第一次备份能说明磁盘和仓库有多快（之后的备份复制得很少，所以它的速度说明不了什么）。应用程序没有修改或删除这些记录的途径，并且数据库触发器会拒绝对它们的 UPDATE 和 DELETE 语句。没有发现变化的自动运行不留下记录。
- **备份 TimeVault 自身。** 需要同时备份 PostgreSQL 数据库（例如用 `pg_dump`）和整个仓库文件夹。仓库只会增长，所以较旧的数据库副本配上较新的仓库是一致的，而较新的数据库配上较旧的仓库会报告对象缺失（校验会列出，恢复会被阻止）。无论恢复了哪一个，之后都请运行一次深度校验。

## 故障排查

| 现象 | 原因与处理 |
|---|---|
| 服务以"is not a loopback address"退出 | 在反向代理之后使用 `127.0.0.1`，或在明确知晓风险的情况下设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`。 |
| 服务以数据库结构相关的提示退出 | 运行 `timevault-server migrate`，或设置 `TIMEVAULT_AUTO_MIGRATE=true`。 |
| 服务以"no way to sign in"退出 | 设置 `TIMEVAULT_REDIS_URL`（公司账号）和/或 `TIMEVAULT_ADMIN_PASSWORD_HASH`（本地管理员；用 `timevault-server hash-password` 生成该值）。 |
| 服务以与 `.env` 有关的错误退出 | 提示会指出文件和行号，绝不会显示该行内容。`TIMEVAULT_ENV_FILE` 必须指向一个存在的文件。 |
| 服务以与 `TIMEVAULT_SERVER_HOST` 或 `TIMEVAULT_RESTORE_DISPLAY_ROOT` 有关的错误退出 | 提示会指出设置名和问题所在，绝不会显示它的值。主机名只能包含字母、数字、`-`、`_` 和 `.`；显示根路径必须形如 `\\server\share\folder`，共享名不能以 `$` 结尾，并且必须位于 `TIMEVAULT_SERVER_HOST` 上。 |
| 添加或编辑监控文件夹被拒绝，提示"请填写显示文件夹"或"其他服务器上的文件夹不能在这里监控" | 设置了 `TIMEVAULT_SERVER_HOST`，所以显示文件夹是必填的，并且它的主机必须是那台服务器（提示里会点出两个主机名）。在该设置出现之前添加的文件夹没有显示文件夹；请在设置页给每个文件夹填上一个。 |
| 服务以"another TimeVault service is already running against this database"退出 | 另一个 `timevault-server` 正在使用同一个数据库（schema）。请先停止那一个；它的锁会在其进程结束时释放。 |
| 服务以"not a TimeVault repository"退出 | `TIMEVAULT_REPOSITORY_PATH` 指向的非空文件夹中没有 `timevault-repository.json`。请使用空文件夹。 |
| 脚本找不到 `go` | 把 Go 加入 `PATH`，或把 `TIMEVAULT_GO_ROOT=<包含 bin\go.exe 的文件夹>` 这一行写入 `.env`。只在某一个终端里设置该变量，仅对那个终端有效。 |
| 重新构建之后，页脚、设置页或 `timevault-server version` 显示的仍是旧版本号 | 要么构建命令里还带着 `-X main.version=<旧版本号>`（旧命令的副本），于是程序是新代码、版本号却是旧的：请用[构建可执行文件](#构建可执行文件)中不带 `-X` 的命令构建。要么服务仍在运行旧文件：Windows 不允许替换正在运行的程序文件，所以请先停止服务，复制新文件，再启动它，并对真正启动的那个文件运行 `version`。用 `-trimpath` 构建时不会记录 `-ldflags`，所以 `go version -m` 看不到这个参数；请以 `version` 为准。 |
| 服务监听的端口（或其他设置）与 `.env` 中写的不一致 | 进程环境变量中的 `TIMEVAULT_*` 变量优先于 `.env`。启动日志中有一条警告，会点名它覆盖的每一项设置（端口对应 `TIMEVAULT_LISTEN_ADDR`）。常见原因是旧版 `dev-run.ps1` 在终端里留下了变量：请用 `scripts\dev-run.ps1` 启动服务，或用 `. .\scripts\dev-env.ps1` 加载会话，它们都会移除这类变量。如果它又出现了，说明它设在你的 Windows 用户或系统环境变量里。 |
| `dev-db.ps1` 找不到 PostgreSQL | 设置 `TIMEVAULT_PG_BIN` 或传入 `-PgBin`。 |
| 登录返回 429 | 尝试次数过多：10 分钟内每个地址 5 次，或每个账号名 10 次失败（`Retry-After` 说明要等多久）。放在反向代理之后时，所有访问者共用代理的地址，除非 `TIMEVAULT_TRUSTED_PROXIES` 列出了该代理。请等待，或重启服务（计数保存在内存中）。 |
| 登录提示无法连接用户账号服务（503） | Redis 没有应答。浏览、已有的会话和本地管理员仍可使用。请检查 `TIMEVAULT_REDIS_URL` 并运行 `timevault-server check-redis-users`；**状态**页会把该目录显示为"降级"。这期间的尝试计入地址，不会算到账号头上。 |
| 公司账号无法登录 | 密码错误、账号未启用、没有保存密码，或登录名匹配到多个账号（工号、姓名或邮箱；服务日志会说明这一点，但不会点名任何人）。请运行 `check-redis-users`：不可读的记录、没有密码的记录和出现在多个键上的工号都会在那里计数。可以试试工号，它是歧义最小的。 |
| `check-redis-users` 报告有不可读的记录 | 有解码器无法读取的哈希（缺少字段或字段类型出乎意料）。报告会给出该键在所有按名称排序的键中的位置，可据此用 `redis-cli` 找到它。这样的记录无法登录。 |
| 没有"使用 Windows 账号登录（SSO）"按钮 | `TIMEVAULT_SSO_HOST` 为空，或者没有 Redis 用户目录（`TIMEVAULT_REDIS_URL`）。启动日志和设置页会显示 `sso_host`。 |
| 单点登录提示"无法把这次登录与当前浏览器对应起来" | 单点登录服务记录的地址不是 TimeVault 看到的该浏览器的地址。在反向代理之后，请把 `TIMEVAULT_TRUSTED_PROXIES` 设为该代理（代理必须发送 `X-Forwarded-For`），或设置 `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`。审计页会以原因 `address_mismatch` 显示这次失败（登录过程中浏览器的地址变了则为 `browser_address_changed`），并附上 TimeVault 看到的内容：`started_from`（它当作浏览器的地址）、`sso_address`、`connection`、`trusted_proxy` 和 `forwarded`。如果 `started_from` 是某台服务器的地址，说明紧挨着 TimeVault 的代理前面还有另一层代理，把浏览器隐藏了：见[两层代理](#两层代理)。如果 `trusted_proxy` 是 `false` 且设置了 `forwarded`，说明连接过来的代理不在 `TIMEVAULT_TRUSTED_PROXIES` 中。 |
| 打开 `/tv/` 后落到了另一个应用（例如门户页），或者页面没有样式、链接都离开了 `/tv/` | TimeVault 不知道自己的前缀。请使用 0.1.5 或更高版本，在 `.env` 中设置 `TIMEVAULT_BASE_PATH=/tv` 并重启；启动日志必须写出"serving under a path prefix"。没有它时，每个重定向和链接都从主机根路径开始，在 `/tv/` 之外。 |
| 在 `/tv/` 之后，审计日志里所有人都是 `127.0.0.1`，或者单点登录提示无法与当前浏览器对应 | 代理没有传递浏览器的地址，或者 TimeVault 不信任该代理。请在代理配置块中设置 `proxy_set_header X-Forwarded-For $remote_addr;`，并在 `.env` 中设置 `TIMEVAULT_TRUSTED_PROXIES=127.0.0.1`（代理连接过来所用的地址）。 |
| 登录之后，某个地址里没有 `/tv` 的页面把你显示为未登录 | 设置了前缀之后，Cookie 被限制在该前缀之内。请使用 `/tv/` 之下的地址；页面上的每个链接和重定向本来就都指向那里。 |
| 单点登录提示浏览器拦截了窗口 | 请允许 TimeVault 站点的弹出窗口。窗口只能由点击本身打开。 |
| 单点登录一直等待，然后提示未能在规定时间内完成 | 单点登录服务没有记录结果。请检查 `TIMEVAULT_SSO_HOST` 是否是人们实际访问的服务，以及它是否把 `sso_login_<token>` 写入 `TIMEVAULT_REDIS_URL` 所指的 Redis（相同的数据库编号）。 |
| 单点登录提示"你的 Windows 账号在这里没有可用的账号" | Windows 给出的工号在 Redis 中没有可用的账号（审计页会显示 `no_active_account` 和该工号）。请用 `check-redis-users` 检查该账号。 |
| 设置页提示"只有管理员可以添加监控文件夹" | 就是这样：添加文件夹只有管理员能做（该账号在 Redis 中的 `Type` 为 `admin`；角色变化在一分钟内生效）。修改某个文件夹的设置需要对它拥有**完全控制**权限，管理员可以在设置页授予。 |
| 某人看不到本该看到的监控文件夹，或者看到的是"未找到" | 没有被授权给任何人的文件夹，谁也看不到。打开该文件夹在设置页的**编辑**表单（以管理员身份，或对它拥有完全控制权），检查这个人的工号是否在某个列表中，或者是否勾选了**所有已登录的人**（对未登录的访问者则是**访客**）。未登录的访问者只有**访客**所给的权限。见[文件夹权限](#文件夹权限)。 |
| 备份（或保存文件夹的设置）时提示"你没有权限执行此操作" | 该账号能看到这个文件夹，但级别太低：备份、校验或停止任务需要**备份**，修改设置需要**完全控制**。审计页会把这次尝试显示为**访问被拒绝**，并带上文件夹和 `needs`。服务器上的文件夹只有管理员能修改。 |
| 文件夹表单的权限部分提示用户目录目前无法列出账号，或者拒绝添加某个人 | Redis 没有应答（或没有设置 `TIMEVAULT_REDIS_URL`）。已经有权限的人会显示出来并可以保留；在目录恢复应答之前，不能添加其他人。请查看**状态**页。 |
| 点击立即备份提示监控文件夹已停用（409） | 需要管理员在设置页重新启用它。 |
| 迁移文件夹时返回 409，或提示它已有备份 | 该文件夹的备份正在运行（请在它结束后再试），或者该文件夹已有某种状态的恢复点，不能再更改：请停用它并添加新的文件夹。 |
| 自动备份没有运行 | 文件夹已停用或没有设置计划；或者它上次运行结束距今不到一个间隔（备份页和设置页会显示下次时间）；或者间隔状态暂不允许（自动备份一次运行一个，带有间隔和休息；见[调度器设置](#调度器设置)）；或者开启了 Everything 探测且 Everything 说没有变化；或者服务没有运行。调度器每 15 秒查看一次。 |
| 我正在查看的任务显示"未找到" | 没有发现变化的自动备份不会留下任务。 |
| 文件浏览中文件夹的大小显示"-" | Everything 没有配置或没有响应、没有索引文件夹大小（请在 Everything 中开启"索引文件夹大小"），或不认识该文件夹。文件不受影响。时间机器和版本页面从不显示文件夹大小。 |
| 更改版本数上限被拒绝，提示"这个上限会永久删除 … 个较旧版本"（409 `prune_not_confirmed`） | 调低上限会删除版本。请勾选确认框后再次保存（API：`confirm_prune: true`）。 |
| 恢复或校验被拒绝，提示该文件夹的旧版本正在被删除（409 `prune_running`） | 该文件夹的版本清理正在运行；更改它的版本数上限也会因此被拒绝。请一分钟后再试；备份页会列出该任务。只是排在该文件夹的备份或校验后面的清理，不会拒绝任何操作。 |
| 删除版本之后仓库没有变小 | 只有在没有备份运行时才会释放内容，而且每次清扫的时间有限；调度器会重复进行。总有备份在运行的服务器会推迟它。 |
| "Restore is not enabled" | `TIMEVAULT_RESTORE_ROOT` 为空。 |
| 恢复预览被判定为过期或失效 | 重新预览；令牌有效期 15 分钟，并与精确的计划绑定。在没有设置 `TIMEVAULT_SESSION_KEY` 的情况下重启，密钥也会变化。 |
| 文件被列为跳过（被锁定） | 文件被其他程序打开，或在读取时发生变化。请稍后再次备份；MVP 没有 VSS。 |
| 恢复报告对象缺失或损坏 | 仓库丢失或损坏了文件。请运行深度校验，从备份恢复仓库，或选择其他恢复点。不会从损坏的对象恢复任何内容。 |
| 中断的恢复留下了 `.tvpart-*` 文件 | 这是崩溃的恢复在目标文件夹中留下的临时文件，可以删除。 |
| 搜索显示"未配置"或"不可用" | 见 [Everything 搜索](#everything-搜索)。历史和恢复仍可使用。"未配置"表示 `TIMEVAULT_EVERYTHING_URL` 为空；"不可用"表示它所指的 Everything 没有应答（它的 HTTP 服务被关闭，或端口、用户名、密码不一致）。 |
| 任务一直显示"failed: interrupted by a service restart" | 任务运行时服务被停止。请重新启动该任务；未完成的恢复点已经被清理。更新从不会这样做：它会等正在运行的任务结束。 |
| 设置页显示**等待正在运行的任务**，页眉下面的提示一直在 | 有任务正在运行（见备份页），更新在无限期地等它。新任务在队列中等待，直到服务重启。如果重启不能等，请手工停止服务；服务再次启动时，该任务会被恢复为失败，更新则在那次启动之后的第一次查看（10 秒后）重新开始。 |
| **程序更新**显示**出现问题**，管理员看到"read the manifest: …" | 服务读不到 `TIMEVAULT_UPDATE_URL` 处的清单：文件还不在那里、共享或 Web 地址有误或服务器访问不到、运行服务的账号不能读它，或者 `http(s)` 地址返回了 `200` 以外的应答（`404`、跳转到登录页、`202`）。请在服务器上打开这个地址试试。 |
| **出现问题**："the program is not the one the manifest describes" | `timevault.exe` 的大小或 sha256 与 `timevault.json` 中的不一致：发布还在复制中（每次查看都会重试，会自行恢复），或者这两个文件来自不同的构建，或者 `package_url` 指向了另一个文件。请重新复制这两个文件（清单最后复制），或再运行一次 `scripts\release.ps1`。 |
| **出现问题**："the program of the release says it is version X, but the manifest says Y" | 清单中的 `latest_version` 与程序里的版本不一致（手工改过清单，或者构建所用源码中的 `var version` 没有提高）。在清单改变之前不会再试。请运行 `scripts\release.ps1`，它会从程序中取版本号。 |
| **出现问题**："the new program does not accept the settings of this service: …" | 新版本的 `timevault.exe check-config` 拒绝了 `.env`（新版本对某个设置的检查更严格，或要求了某个设置）。请阅读原因并修改 `.env`；下次查看时会再试。这期间服务继续使用旧程序运行。 |
| **出现问题**："the new program could not be put in place: … Access is denied" | 运行服务的账号不能在程序所在的文件夹中重命名或创建文件，或者杀毒软件占用了该文件。请给该账号对该文件夹的**修改**权限（启动日志会对此发出警告）。服务继续使用旧程序运行，在清单改变或服务重启之前不会再尝试这个发布。 |
| 更新之后，控制台窗口或计划任务显示程序已结束，退出码为 75 | `TIMEVAULT_UPDATE_RESTART=exit`，而没有任何东西再次启动程序。请使用 `supervise`（默认值），或者让服务管理器在程序结束时重新启动它（NSSM：`AppExit 75 Restart`）。 |
| 更新之后，旧程序文件 `timevault.old-<版本>.exe` 无法删除 | 使用 `supervise` 时，启动器仍在从它运行（见[发布与自动更新](#发布与自动更新)）。启动器结束之后，它会在以后的某次更新中被删除；在服务停止并重新启动之后，也可以手工删除。 |
| 手工回退之后，服务又自己更新回了较新的版本 | 发布文件夹里仍然是较新的清单。回退之前请先把较旧的发布放到那里（或者清空 `TIMEVAULT_UPDATE_URL`）。 |
| 账号头像没有显示 | 检查 `TIMEVAULT_AVATAR_URL`（启动日志里有 `avatar_url`；`off` 表示没有头像），并在浏览器中打开同一主机名下的 `/api/static/hr/<工号>.webp`：它必须返回 `200` 和一张图片。`202`（没有头像的人的占位图）和 `404` 会有意保留默认头像。直接通过服务自己的端口（`http://host:8660/`）访问时没有这样的地址。 |
