# English

## Overview

How to set up, configure, run, operate and troubleshoot TimeVault (version 1.0.1). It describes what exists today; the service is a single Go program, `timevault-server`, that needs PostgreSQL and a repository folder, and, for company sign-in, the Redis user directory of another application (the same Redis takes the service's web log, see [Web log](#web-log)). For search, folder sizes and skipping unchanged folders the service reads the NTFS volumes of the monitored folders itself, which needs administrator rights (see [Search index](#search-index)); there is nothing else to install. The `.env` file holds only what the service needs before it can reach its database, and its secrets; every other setting is a **Site setting**, kept in the database and edited by the local administrator on the **Site settings** page (see [Where settings live](#where-settings-live)). There is no installer and no Windows service wrapper yet.

## Prerequisites

| Item | Notes |
|---|---|
| Windows with NTFS | Developed and tested on Windows 11 Pro. The server also compiles on other systems (some path rules and tests are Windows specific), but only Windows was exercised. |
| Go 1.25 or newer | Needed to build (`go.mod` declares `go 1.25.0`); the restore code uses `os.Root`. A portable Go works: put the line `TIMEVAULT_GO_ROOT=<Go folder>` (the folder that contains `bin\go.exe`) into `.env` and the dev scripts add it to `PATH` in every terminal, without touching the system `PATH`. |
| PostgreSQL | Tested with 16.4 only. The SQL needs at least PostgreSQL 11 (identity columns, `hashtextextended`), but older versions than 16 were not tried. |
| Redis (optional) | The user directory of another (dash) application: hashes named `app_user_<ee_code>_<name>_<email>`. Without it only the local administrator can sign in. Tested against an in-process fake and pickle data written by Python, not against the company's own Redis: run `timevault-server check-redis-users` on it once. The service also appends its web log to a list in that Redis (on by default when `TIMEVAULT_REDIS_URL` is set; see [Web log](#web-log)): the account of `TIMEVAULT_REDIS_URL` must be allowed to `RPUSH` to it, which is the only write, and that was tested with an in-process fake too. |
| Administrator rights (for the search index) | The service reads the NTFS volumes of the monitored folders directly (see [Search index](#search-index)), and Windows allows that only to a process that runs as an administrator or as the SYSTEM account: start the service from an elevated shell, or as a scheduled task that runs with the highest privileges. Without it search shows "unavailable", folder sizes show "-", the scheduler scans as it always did and the Status page says why; everything else works. Only monitored folders on a local NTFS drive letter (`D:\...`) are indexed. |

## Quick start for development

The scripts keep their data under the git-ignored `data\` folder and **all settings in one git-ignored file, `.env` in the repository folder** (see [Where settings live](#where-settings-live)). They never touch an existing PostgreSQL service and print no secrets. If PowerShell blocks the scripts, run them as `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1`. If Go is not on your `PATH`, first put the line `TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>` into `.env`; without it `dev-run.ps1` stops at once with "The go command was not found". Earlier versions kept settings in three more files (`data\local.env`, `data\pgdev\db.env`, `data\dev\server.env`); the first time a script runs it moves their settings into `.env` and deletes them.

1. `scripts\dev-db.ps1 start` creates (first time) and starts a private PostgreSQL cluster in `data\pgdev` on `127.0.0.1:55432` with the databases `timevault` and `timevault_test`. The random password is written into `.env` (`TIMEVAULT_DATABASE_URL` and `TIMEVAULT_TEST_DATABASE_URL`). It needs the installed PostgreSQL binaries (`TIMEVAULT_PG_BIN` in `.env` or `-PgBin` to point at them). Other actions: `stop`, `status`, `init`, `reset` (deletes the cluster). **You can skip this step and use a PostgreSQL server you already have**: create the database and the role yourself, then put `TIMEVAULT_DATABASE_URL` (and `TIMEVAULT_TEST_DATABASE_URL`, for the tests) into `.env`. The scripts then start no private cluster, and `dev-db.ps1` refuses to create or reset one while `.env` points at another server.
2. `scripts\dev-run.ps1` creates `.env` (commented, git-ignored) when it is missing; starts the private database when `TIMEVAULT_DATABASE_URL` points at it (port 55432) and otherwise expects the server you configured to be running; builds `data\dev\bin\timevault-server.exe`; and adds to `.env` what the service needs and `.env` does not have yet: a repository and a restore folder under `data\dev`, `TIMEVAULT_AUTO_MIGRATE=true`, a session key and, on the very first run, a random password for the local administrator `admin` (the password goes to `data\dev\admin-password.txt`, only its hash to `.env`). **It removes `TIMEVAULT_*` variables that are left in your terminal** before it starts the server, because they would override `.env`, and it names them. It then runs the server in the foreground on `http://127.0.0.1:8660/` (`-Port` changes the port for that run; without it `TIMEVAULT_LISTEN_ADDR` from `.env` applies) and prints whether the Redis user directory is configured. Start the script from an **elevated** PowerShell if you want search and folder sizes: the NTFS index reads the volumes, which needs administrator rights (see [Search index](#search-index)); from a normal shell everything else works and the Status page says that the index needs administrator rights. Stop it with Ctrl+C.
3. `scripts\dev-demo.ps1` (with the server running) registers `data\dev\source` as the monitored folder "Demo files" and creates three recovery points through the API, leaving some uncommitted changes so the Explorer shows changed, new and deleted items. It takes the address from `TIMEVAULT_LISTEN_ADDR` in `.env`; after `dev-run.ps1 -Port` pass `-BaseUrl http://127.0.0.1:<port>`.
4. Open the address. Anyone can browse. To back up, restore or change settings, sign in as `admin` with the password from `data\dev\admin-password.txt`, or with a company account after you put `TIMEVAULT_REDIS_URL` into `.env` (check it with `data\dev\bin\timevault-server.exe check-redis-users`, run from the repository folder). That also turns the web log on (see [Web log](#web-log)): on a development computer that uses the company's Redis, set the Site setting `web_log` to `off` too (**Settings → Site settings**). The header has the language switch (English / 中文) and a light/dark theme toggle (the page follows the system setting until you use the toggle; the browser then remembers your choice).

5. Optional, for the search box, the folder sizes and the scheduler's change probe: run `scripts\dev-run.ps1` from an elevated PowerShell. A monitored folder is indexed shortly after it is added (see [Search index](#search-index)); there is nothing else to install or switch on.
6. Optional, for the other settings (the address people use, sign-in options, workers, the scheduler, updates, logging): sign in as the local administrator `admin` and open **Settings → Site settings** (see [Where settings live](#where-settings-live)).

`. .\scripts\dev-env.ps1` prepares your own PowerShell session for `go test` and for running `timevault-server` by hand. It removes `TIMEVAULT_*` settings that are left in the session (they would override `.env`), sets `TIMEVAULT_ENV_FILE` to the `.env` of the repository, loads what only tests and tools read (`TIMEVAULT_TEST_*`, `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN`) and puts Go on the `PATH` of that session. The settings of the service are not copied into the environment: the service reads `.env` itself.

## Commands

| Command | Purpose |
|---|---|
| `timevault-server serve` | Run the service (the default when no command is given). |
| `timevault-server migrate` | Apply the database migrations and exit. |
| `timevault-server hash-password` | Read a password (hidden when typed in a terminal, or one line from standard input) and print the value for `TIMEVAULT_ADMIN_PASSWORD_HASH` (the optional local administrator). |
| `timevault-server check-redis-users` | Connect to the Redis user directory (`TIMEVAULT_REDIS_URL`) and print how much of it can be used: the number of keys, readable and unreadable records, activated accounts, types `admin` and `user`, accounts without a password, employee IDs that appear on more than one key, and per field how many values could be decoded. It prints no name, e-mail address, key or password. Exit code 1 when Redis does not answer or no record is readable. Run it from the folder that holds `.env`, or set `TIMEVAULT_ENV_FILE`. |
| `timevault-server version` | Print the version. |
| `timevault-server check-config` | Check that the settings of the environment and the `.env` file are valid for the service, print `the settings are valid` or the reasons they are not (exit code 1). It connects to nothing, so it does not look at the Site settings: those are in the database and are checked when they are saved. A service that updates itself runs it with the new program before it replaces itself (see [Releasing and automatic updates](#releasing-and-automatic-updates)). |
| `timevault-server settings list`, `get <key>`, `set <key> <value>`, `reset <key>` | The Site settings from the command line, on the database that `TIMEVAULT_DATABASE_URL` names (run it from the folder that holds `.env`, or set `TIMEVAULT_ENV_FILE`). `list` shows every Site setting with the value in effect, a `*` marking a stored value; `set` stores a value after checking it like the page does (a value that equals the default is not stored); `reset` forgets the stored value so the default applies. It is the way back when the Site settings page cannot be reached, for example because a setting locked everybody out. The service has to be restarted to use what was changed. See [Where settings live](#where-settings-live). |

## Building the executable

`timevault-server.exe` is the only file the service needs besides its `.env`: the web templates, the assets and the database migrations are embedded in it. The program is the package `./cmd/timevault-server`; the repository root is not a program (`go build .` fails with "no Go files").

```powershell
cd <repository folder>
go build -trimpath -ldflags "-s -w" -o timevault-server.exe ./cmd/timevault-server
.\timevault-server.exe version
```

`go build -o timevault-server.exe ./cmd/timevault-server` is enough. `-trimpath -ldflags "-s -w"` only makes the file smaller (about 23 MB instead of 32 MB). Go 1.25 or newer is needed (see `go.mod`).

**The version number** is written in one place, `var version` in `cmd/timevault-server/main.go`, and the build takes it from there, so the command above always gives the version of the sources it is run on. The `version` command, the start-up log, the footer of every page, the Settings page and `GET /api/v1/settings` show it. Run `.\timevault-server.exe version` after every build. Do not add `-X main.version=<number>` to the command: it replaces the number in `main.go`, so a copy of the command that was written for an older version keeps stamping the old number. A build of the 0.1.5 sources with `-X main.version=0.1.4` has all the features of 0.1.5 but calls itself 0.1.4.

**Dependencies and the `vendor\` folder.** The build needs the Go modules listed in `go.mod`, either from the module cache (`go env GOMODCACHE`, which Go fills from the module proxy) or from a `vendor\` folder next to `go.mod`. While a `vendor\` folder exists, Go uses it automatically (the same as `-mod=vendor`; `go build`, `go vet` and `go test` all do). That suits a server without internet access: run `go mod vendor` once on a computer that has the modules (it writes about 600 files, 7 MB), and copy the `vendor\` folder together with the sources.

- `vendor\` is not part of the repository, so a clone of the repository does not have it. Copy it with the sources, or build on the computer that has it.
- `-mod=vendor` without a `vendor\` folder fails with `go: inconsistent vendoring ... is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt`. The same message appears when `vendor\` is older than `go.mod` or `go.sum`. Run `go mod vendor` again (do it after every change of the dependencies), or build from the module cache with `-mod=readonly`, which ignores `vendor\`.
- The simplest way to deploy to a server without internet access is to build `timevault-server.exe` on the development computer and copy that file, with the `.env`, to the server.
- A server that updates itself does not need that copy more than once: `scripts\release.ps1` builds the release folder that such servers read (see [Releasing and automatic updates](#releasing-and-automatic-updates)).

## Where settings live

| What | Where | Changed by | Takes effect |
|---|---|---|---|
| Bootstrap settings and secrets: the database URL, the listen address, the backup folder (`TIMEVAULT_REPOSITORY_PATH`), the restore folder (`TIMEVAULT_RESTORE_ROOT`), the Redis URL of the user directory, the local administrator, the session key (the list is in [Configuration reference](#configuration-reference)) | The `.env` file (a process environment variable overrides it) | Whoever installs the service, by editing the file | After a restart |
| **Site settings**: this server's host name and the display path of the restore folder, pictures of the accounts, the path behind a reverse proxy, trusted proxies, sign-in options, workers and process priority, the scheduler, the search index, automatic updates, logging and the web log (26 settings in 7 groups, listed in [Site settings](#site-settings)) | The database (table `site_settings`) | **Only the local administrator**, on the **Site settings** page, with `PATCH /api/v1/site-settings`, or with `timevault-server settings` | After a restart (the page has a **Restart now** button) |
| Monitored folders: name, display folder, folder on the server, exclude patterns, on or off, automatic schedule, versions kept per file | The database (table `sources`) | Administrators add folders and can change everything about them; whoever has Full on a folder changes its settings, but not the folder on the server, on the **Settings** page or with `PATCH /api/v1/sources/{id}` | At once |
| Who may do what with each folder (Browse, Back up, Full; granted to guests, to everybody who is signed in or to single employee IDs) | The database (table `source_access`) | Administrators, and whoever has Full on the folder, in the permissions part of the folder's form on the **Settings** page or with `access` in the API (see [Folder permissions](#folder-permissions)) | At once, on the next request of every visitor |
| Accounts and roles | Redis (`app_user_*`), written by another application | That application | Within a minute for a session that is already signed in |

- The `.env` file is the one named by `TIMEVAULT_ENV_FILE` (it must exist), otherwise `.env` in the working directory, otherwise `.env` next to `timevault-server.exe`. The first one found is used; files are not merged. In development it is the `.env` in the repository folder, and it is the only settings file: the dev scripts read and write it, and `dev-run.ps1` points the service at it.
- **A `TIMEVAULT_*` variable in the process environment wins over the same key in the file**, and nothing tells you unless you look: a variable left in a terminal makes the service ignore what you wrote in `.env` (typically a port that does not change). The service therefore names every setting that the environment overrides in a warning when it starts (`check-redis-users` prints them too), and the dev scripts remove such variables from the terminal. If the warning keeps coming back, the variable is set in your Windows user or system environment. This is about the bootstrap settings only: a Site setting that is still written in the environment or in `.env` (such as `TIMEVAULT_JOB_WORKERS`, or one of the removed `TIMEVAULT_EVERYTHING_*` settings) is **ignored**, whatever its value, and the start-up log names it in a warning, so that an old line can never be the reason a value saved on the Site settings page does not take effect.
- Syntax: one `KEY=VALUE` per line. Blank lines and lines that start with `#` are ignored, a leading `export ` is allowed, and a UTF-8 byte order mark and Windows line ends are accepted. The value is taken literally up to the end of the line, so a path with `\` or a password with `#` needs no escaping (and a trailing ` # comment` would become part of the value); one pair of matching single or double quotes around the value is removed; the last assignment of a key wins. An error names the line number and never shows the line.
- The file holds secrets (the database and Redis passwords, the session key). `/.env` is in `.gitignore`; keep the file readable only by the account that runs the service, and never commit a filled-in copy. [configs/timevault.example.env](../configs/timevault.example.env) is the template.
- The **Settings** page (any signed-in account may open it) shows the monitored folders the visitor may browse, each with what the visitor may do with it, the backup storage and the restore folder with the free space of the repository, a **Service** card with the version (the local administrator also finds the **Site settings** button there), and the state of the automatic update (see [Releasing and automatic updates](#releasing-and-automatic-updates): the address of the release and the last problem are only for administrators, who can also press **Check now**). Secrets are never shown. The running configuration that this page used to show (this server's host name, the listen address, the database and Redis as `host:port/name`, the local administrator's name, the settings file and the state of the search index) is on the **Site settings** page, which only the local administrator sees. The form that adds a monitored folder is for administrators; the form that changes one is for administrators and for whoever has Full on that folder (without the folder on the server's disk, which only administrators see and change). Only administrators see the folders on the server's disk (the real folder of each monitored folder, the backup folder and the real restore folder); everybody else sees the display folders.
- **Backup folder and restore folder.** They are two different places. The backup folder (`TIMEVAULT_REPOSITORY_PATH`, the repository) is where TimeVault keeps the contents of everything it backed up. The restore folder (`TIMEVAULT_RESTORE_ROOT`) is where restored files are put: a restore never overwrites the original files and never writes into a monitored folder, so it writes the restored copies into a new folder below the restore folder (named on the restore form, by default `restore-<date>-<time>`), and you copy back what you need from there. Neither may lie inside a monitored folder.
- **Display folders.** People reach the monitored folders and the restore folder over the network, not as `D:\TimeVault\monitor`. Every monitored folder therefore has a **display folder**, the network path people open it with (for `D:\TimeVault\monitor` shared as `TimeVault`: `\\sgmeslt595.co.murata.local\TimeVault\monitor`), and the restore folder has the Site setting `restore_display_root` (`\\sgmeslt595.co.murata.local\TimeVault\restore`). Set the Site setting `server_host` to the name of this server (`sgmeslt595.co.murata.local`): then the Settings page and the API refuse a monitored folder without a display folder, and one whose display folder is on another server (its search, backup and restore only work with this server's own folders). Without `server_host` a display folder is optional and is not compared with a host. Administrators fill the field on the Settings page, which also marks a monitored folder that has none ("No display folder yet"; after setting the host name every folder that was added before needs one before it can be edited again). The service checks the host, not that the share really leads to the monitored folder: share the folder first, then write the path you open it with. Where a display folder is shown, the Explorer, the Time Machine and the page of a file's versions have a **Location** line with a **Copy** button.
- **Who sees which folder.** Administrators see both the display folder and the real folder. Everybody else, signed in or not, sees only the display folder, and texts that quote a real folder (job errors and logs, restore messages, audit details, status details, the reasons a file was not captured) show the display folder, `[<name of the monitored folder>]`, `[backup folder]` or `[restore folder]` instead. See [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md).

## Site settings

Everything that the service does not need before it can reach its database is a **Site setting**: the address and display options, sign-in options, workers and priority, the scheduler, the search index, automatic updates and logging. Site settings are stored in the database (table `site_settings`, one row for each setting that has a value) and only the **local administrator** can read and change them: the account named by `TIMEVAULT_ADMIN_USER` that signs in with `TIMEVAULT_ADMIN_PASSWORD_HASH` (with sign-in switched off for development, everybody is the local administrator). Company accounts cannot see or change them, not even the accounts of type `admin`. The reasons are in [ADR 0017](./adr/0017-site-settings-in-the-database.md).

- **The page.** **Settings → Site settings** (`/settings/site`) shows the 26 settings in seven groups, each with the value in effect, the default and a short explanation, and above them what the environment and `.env` hold (version, listen address, database and Redis as `host:port/name`, backup and restore folder, the local administrator, the settings file and the state of the search index; never a secret). Anyone else who is signed in is told that there is no such page (`404`, `not_found` in the API) and the attempt is written to the audit log as an access denied with `reason: site`; the page is not offered in the menus. A visitor who is not signed in is asked to sign in, as for every page that needs an account.
- **A value is stored only when it differs from the default.** A setting that equals its default is not stored, so a default that changes in a later version reaches everybody who never chose a value. An empty field means "use the default".
- **Checked before it is saved.** The values are checked together with the bootstrap settings that they depend on (the web log needs the Redis of `TIMEVAULT_REDIS_URL`, an update address needs `TIMEVAULT_AUTO_MIGRATE=true`, and so on). A form with a problem is shown again as it was typed, with what is wrong, and nothing is saved. The messages never repeat a value.
- **They take effect after a restart.** Saving changes nothing in the running service. While what is saved differs from what the service started with, the page says "Some saved settings are not in use yet" and offers **Restart now**. The service then stops cleanly and starts again the way it does after an automatic update (`update_restart`: `supervise` keeps the process as a small launcher that starts it again, `exit` ends it with exit code 75 for a service manager to start). It refuses while a job runs ("Jobs are running. Restart when they have finished."; `409 jobs_running` in the API): for a moment it stops taking new jobs, and takes them again when it does not restart. Restarting by hand has the same effect.
- **Audit.** A change is written to the audit log as **Site settings changed** (`site.update`) and a restart as **Restart for the Site settings** (`site.restart`), with the actor and the address. The detail names the settings that changed (`changed`) or went back to the default (`reset`) and **never their values**, which can be addresses or paths. Only the local administrator sees these events, on the **Audit** page and in `GET /api/v1/audit`; everybody else's list leaves them out and does not offer them as a filter.
- **API and command line.** `GET /api/v1/site-settings`, `PATCH /api/v1/site-settings` and `POST /api/v1/site-settings/restart` do the same for scripts (see [api.md](./api.md)). `timevault-server settings list|get|set|reset` (see [Commands](#commands)) works on the database directly. It is the way back when the page cannot be reached, for example because a setting locked everybody out: run `timevault-server settings reset <key>` and restart the service. Without a local administrator in `.env` it is the only way to change a Site setting, and the service says so when it starts.
- **Starting.** The service reads `.env`, connects to the database with a small pool, applies the migrations (`TIMEVAULT_AUTO_MIGRATE=true`) or checks that they were applied, reads the Site settings, validates everything together and only then opens the pool that `job_workers` asks for. If the stored settings are not valid together, the service does not start and says so (`the Site settings in the database are not valid; correct them with "timevault-server settings"`), naming the problems.
- **After an upgrade from 1.0.0** the table is empty and every setting has its default, whatever `.env` said: enter the values again on the page (or with `settings set`) and restart. Write the values down before you upgrade, because the new program ignores them as soon as it starts. Two of them matter at once: a server that updates itself stops doing so until `update_url` is entered again (the update that brings 1.0.1 is the last one it installs by itself until then), and a site behind a proxy under a path has broken links until `base_path` is entered again; open the Site settings page on the server's own address (`http://127.0.0.1:8660/settings/site`, which needs no prefix) or run `timevault-server settings set base_path /tv`. A Site setting that is still in `.env` is ignored and named in a warning when the service starts; delete those lines. The removed `TIMEVAULT_EVERYTHING_*` settings and `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` are named there too. The database URL, the folders, Redis, the local administrator and the session key stay in `.env`.

| Group | Key | Default | Meaning |
|---|---|---|---|
| Address and display | `server_host` | empty | The name of this server as people type it in network paths, for example `sgmeslt595.co.murata.local`. Letters, digits, `-`, `_` and `.` only. When set, every monitored folder needs a display folder on this host (compared without regard to case), and `restore_display_root` must be on it. When empty, display folders are optional and not compared with a host. |
| Address and display | `restore_display_root` | empty | The restore folder as people reach it over the network, `\\server\share\folder` (either slash). It must be a network path, on `server_host` when that is set; ignored when there is no restore folder. Without it, ordinary accounts are told only that the restore folder is on the server. |
| Address and display | `avatar_url` | `http://<server_host>/api/static/hr/{id}.webp` | Where the browser reads the picture of an account from, with `{id}` once where the employee ID goes: an `http(s)` address (no user name or password), or a path starting with `/` on the host the site is opened from. Not set: the pictures of the other company applications on the server named in `server_host`, and no pictures while that is not set. `off` or `none` turns the pictures off. See [Pictures of the accounts](#pictures-of-the-accounts). |
| Address and display | `base_path` | empty | The path prefix the site is served under behind a reverse proxy, such as `/tv` for `http://host/tv/`. Empty means the site is at the root of its address. `tv`, `/tv` and `/tv/` all mean `/tv`; letters, digits and `- _ . ~` only, and it must not start with a path of the application itself (`api`, `static`, `login`, `explorer`, `history`, `versions`, `backups`, `jobs`, `restore`, `audit`, `status`, `settings`, `lang`, `logout`, `auth`, `healthz`, `help`). Every address the pages, the redirects and the cookies give out carries it; a request with or without it is accepted. See [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path). |
| Address and display | `trusted_proxies` | empty | Reverse proxies, as addresses or CIDR ranges separated by commas (`127.0.0.1,::1,10.0.0.0/24`), whose `X-Forwarded-For` header is believed. The client address then comes from that header; it is used by the single sign-on check, the **From** column of the audit log, the sign-in throttle, the `ip=` of the console log and the address in the web log. The proxy must set that header itself; see [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path), and [Two proxies in a row](#two-proxies-in-a-row) when another proxy sits in front of it. |
| Sign-in | `session_ttl` | `8h` | Session lifetime, from `5m` to `720h`. |
| Sign-in | `cookie_secure` | `false` | Adds `Secure` to cookies. Set it to `true` behind HTTPS. |
| Sign-in | `sso_host` | empty | The company sign-on service, as `host`, `host:port` or `http(s)://host[:port]` (the scheme defaults to `http`). When set, the login page offers "Sign in with Windows (SSO)"; see [Single sign-on](#single-sign-on). Needs `TIMEVAULT_REDIS_URL`. |
| Sign-in | `sso_require_same_ip` | `true` | The address that the sign-on service saw must be the address of the browser that asked for the sign-in. Turn it off only when the address of the browser cannot be known (`trusted_proxies` is the better way). |
| Performance | `job_workers` | `2` | Jobs that run at the same time (1 to 16). The database connection pool is sized to match (2 × workers + 8, at least 16 connections); PostgreSQL's `max_connections` must allow that. |
| Performance | `hash_workers` | smaller of 4 and the CPU count | Files hashed and copied in parallel inside one backup (1 to 32). |
| Performance | `process_priority` | `below_normal` | Priority of the service among the programs of this computer: `normal`, `below_normal` or `background` (which also lowers the disk priority). Windows only. |
| Scheduler | `scheduler_parallel` | `1` | How many automatic backups may be queued or running at the same time (1 to 4). Backups started by hand are not limited. See [Scheduler settings](#scheduler-settings). |
| Scheduler | `scheduler_gap` | `auto` | The least time between the starts of two automatic backups: `auto`, or a duration from `0s` to `6h` (`0s` turns the spacing off). |
| Scheduler | `scheduler_rest` | `auto` | The pause after an automatic backup before the next one starts, only while `scheduler_parallel` is 1: `auto`, or a duration from `0s` to `6h` (`0s` turns it off). |
| Scheduler | `scheduler_probe` | `false` | Ask the search index whether a folder changed before scanning it, and skip the scan when it did not. It works only while the NTFS index runs (see [Search index](#search-index)); without it every due folder is scanned as before. |
| Scheduler | `scheduler_full_scan_every` | `24h` | With the probe on, the longest a folder goes without a complete scan, from `1h` to `720h`. |
| Search index | `search_index` | `auto` | `auto`: the NTFS index of the monitored folders runs whenever the service is allowed to read the volumes (see [Search index](#search-index)). `off`: it never runs, so search shows "unavailable", folder sizes show "-" and the scheduler probe has nothing to ask. |
| Automatic updates | `update_url` | empty | Where the release manifest `timevault.json` is: an `http(s)` address, or the absolute path of the file, of its folder or of a share (`D:\TimeVault\release`, `\\server\share\tv-release`); an address that ends in a slash, and a path of a folder, name the folder. No user name, password, query or fragment. Empty means the service never updates itself. Needs `TIMEVAULT_AUTO_MIGRATE=true`. See [Releasing and automatic updates](#releasing-and-automatic-updates). |
| Automatic updates | `update_interval` | `10m` | How often the manifest is read, `1m` to `24h`. The first look is about 10 seconds after the start; an administrator can ask for one at once (**Check now**). |
| Automatic updates | `update_restart` | `supervise` | How the new program is started after an update, and after a restart for the Site settings: `supervise` (the process that was started stays as a small launcher and starts it) or `exit` (the process ends with the exit code 75 and a service manager starts it). |
| Logging | `log_level` | `info` | `debug`, `info`, `warn` or `error`. `debug` also writes the debug lines of the web log; the other levels change nothing there (see [Web log](#web-log)). |
| Logging | `log_format` | `text` | `text` or `json`. |
| Logging | `web_log` | `auto` | Whether the service appends lines to the web log that the company applications share, a list in the Redis of `TIMEVAULT_REDIS_URL`. `auto`: on exactly when `TIMEVAULT_REDIS_URL` is set. `on` (also `true`, `1`, `yes`): needs `TIMEVAULT_REDIS_URL`, or the setting is refused. `off` (also `false`, `0`, `no`): never written. See [Web log](#web-log). |
| Logging | `web_log_key` | `web_log` | The Redis list of the web log. ASCII letters, digits and `_ . : -` only, at most 128 characters; it must not start, in any case, like `TIMEVAULT_REDIS_KEY_PREFIX` or `sso_login_` (the keys of the accounts and of the single sign-on results), or the setting is refused. |
| Logging | `web_log_host` | the computer name in lower case | The last field of every line of the web log. ASCII letters, digits and `. - _` only, at most 63 characters; a value is used as written. |

## Configuration reference

The `.env` file holds only the settings below; each is an environment variable or a line of the `.env` file (see [Where settings live](#where-settings-live); the environment wins). Invalid values stop the program with a message that names the variable. Secrets are never logged. Everything else is a [Site setting](#site-settings).

| Variable | Default | Meaning |
|---|---|---|
| `TIMEVAULT_ENV_FILE` | none | Path of the settings file to read instead of looking for `.env`. It must exist. Only the process environment can set it. |
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8660` | Address to listen on. A non-loopback address is refused unless `TIMEVAULT_ALLOW_NON_LOOPBACK=true`. |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | Confirms that a TLS reverse proxy protects a non-loopback listener. The service logs a warning at start when it listens beyond this computer. |
| `TIMEVAULT_DATABASE_URL` | none (required) | PostgreSQL connection URL. Secret. |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | Apply migrations at start. When `false` the service refuses to start if the schema is not current; run `migrate` first. |
| `TIMEVAULT_REPOSITORY_PATH` | none (required) | Backup folder (the repository): where the contents of the backed-up files are stored. A new or empty folder is initialised; a non-empty folder that is not a TimeVault repository is refused. Needs a restart to change. |
| `TIMEVAULT_RESTORE_ROOT` | empty | Restore folder. A restore never overwrites the original files and never writes into a monitored folder: it creates a new folder below this one for each restore (named on the restore form) and writes the restored copies there. Empty disables restore. It must not be inside or contain the repository or a monitored folder. |
| `TIMEVAULT_AUTH_DISABLED` | `false` | Turns sign-in off: everyone is the local administrator. Only accepted on a loopback listener. For development only. |
| `TIMEVAULT_REDIS_URL` | empty | The Redis user directory, `redis://[:password@]host:port/db` (`rediss://` for TLS). Empty means no company accounts. The web log is written to the same Redis (see [Web log](#web-log)). Secret. |
| `TIMEVAULT_REDIS_KEY_PREFIX` | `app_user_` | Prefix of the keys of the user hashes. |
| `TIMEVAULT_REDIS_TIMEOUT` | `3s` | Connect, read and write timeout for Redis, `100ms` to `30s`. The web log's writes use it too. |
| `TIMEVAULT_ADMIN_USER` | `admin` | Name of the optional local administrator, the only account that may open the Site settings page. |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | none (optional) | Output of `timevault-server hash-password`. Creates a local administrator who can sign in even when Redis is down; the name is never looked up in Redis. Without it the service needs `TIMEVAULT_REDIS_URL` (unless sign-in is disabled), and the Site settings can only be changed with `timevault-server settings`. Secret. |
| `TIMEVAULT_SESSION_KEY` | random per start | Base64 text that decodes to at least 32 bytes. Set it so sessions and restore confirmations survive a restart. Secret. |

Variables used only by the helper scripts and tests, kept in the same `.env` (the service ignores them): `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN` and `TIMEVAULT_TEST_DATABASE_URL` (see [Tests](#tests)). [configs/timevault.example.env](../configs/timevault.example.env) is a template with placeholders.

## Running outside the dev scripts

Build `timevault-server.exe` first (see [Building the executable](#building-the-executable)).

1. Create a PostgreSQL role and database for TimeVault (use your own password):

   ```sql
   CREATE ROLE timevault LOGIN PASSWORD '<choose-a-password>';
   CREATE DATABASE timevault OWNER timevault;
   ```

2. Copy [configs/timevault.example.env](../configs/timevault.example.env) to `.env` next to `timevault-server.exe`, replace every `<placeholder>` and delete the lines you do not need. Signing in needs `TIMEVAULT_REDIS_URL` (company accounts; check it with `timevault-server check-redis-users`) and/or a local administrator: `timevault-server hash-password` creates the value for `TIMEVAULT_ADMIN_PASSWORD_HASH`.
3. Run `timevault-server migrate` (or set `TIMEVAULT_AUTO_MIGRATE=true`) and `timevault-server serve`; start it from an elevated shell if you want search (see [Search index](#search-index)). Open `http://127.0.0.1:8660/`; an administrator adds the monitored folders on the **Settings** page, and the local administrator sets everything else on **Settings → Site settings** (see [Site settings](#site-settings); a saved setting is used after a restart, and the page has a **Restart now** button).
4. To let other computers use it, put an HTTPS reverse proxy in front, listen on loopback, and set the Site setting `cookie_secure` to `true`. Listening on a non-loopback address directly needs `TIMEVAULT_ALLOW_NON_LOOPBACK=true` and still has no TLS. Anyone who can reach the address can browse the file names, sizes and history of every monitored folder without an account, and company passwords are typed into the site, so HTTPS is not optional. The sign-in limits count the address that connects, so behind a reverse proxy they count the proxy's address unless the Site setting `trusted_proxies` lists the proxy (see [Single sign-on](#single-sign-on) and [security.md](./security.md)). When the proxy serves TimeVault under a path of a host name that other applications share, such as `/tv/`, see [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path).
5. The service has no Windows service wrapper. Start it with a method you trust (for example a scheduled task or a service manager). A scheduled task of a user account (no administrator rights) was tried with 0.1.8: it starts the service, which keeps running, also through an automatic update (see [Releasing and automatic updates](#releasing-and-automatic-updates)); since 1.0.1 that account cannot read the volumes, so the search index reports that it needs administrator rights and everything else works. For search, the service has to run with administrator rights (see [Search index](#search-index)): a scheduled task with **Run with highest privileges** or the account SYSTEM should do it, but only an elevated shell was tried, and a Windows service wrapper such as NSSM was not tried. Switch off the task setting **Stop the task if it runs longer than** (the Task Scheduler sets it to 3 days for a new task), or the service is stopped when the time is up. It stops cleanly on Ctrl+C or SIGTERM, waiting up to 30 seconds for the HTTP server and running jobs.

At start the service marks jobs that were running when the previous process ended as failed ("interrupted by a service restart"), removes the partial history of unfinished recovery points, and then resumes queued jobs.

## Behind a reverse proxy under a path

Use this when one host name serves several applications and TimeVault is one of them: for example `http://prod_eng.co.murata.local/tv/` next to `/qdb/`, `/mer/` and `/gitea/`. Up to 0.1.4 TimeVault wrote every address with a leading slash (`Location: /explorer`, `/static/...`, the forms, the requests of its script). That is right at the root of a host and wrong under `/tv/`: the first redirect leaves the prefix and ends on whatever the proxy serves at the root of the host (in the first deployment, the portal), and the pages lose their style. The TimeVault log then shows one request, and the browser is on another application. Since 0.1.5 the service knows its prefix; the design is in [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md).

**Turn it on**

1. Set the Site setting `base_path` to `/tv` (**Settings → Site settings**, group **Address and display**) and restart the service (**Restart now**). The start-up log says `serving under a path prefix` and the Site settings page shows the **Address prefix**. Without the setting nothing changes.
2. In the proxy, send `/tv/` to the service. The service accepts a request with the prefix and without it, so the proxy may pass the address on as it is (recommended, the simplest) or take the prefix off.
3. The proxy next to TimeVault must tell it the address of the browser, and it has to be listed in the Site setting `trusted_proxies` (`127.0.0.1` when the proxy runs on the same computer). The block below is for a proxy that the browser reaches itself; when another proxy sits in front of it, add what [Two proxies in a row](#two-proxies-in-a-row) says. An nginx block that was run against this version:

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

For a request that comes from the front nginx, nginx now makes `$remote_addr` the address of the browser; a request from anybody else keeps the address it connects from, and the header that it sends is ignored. `trusted_proxies` stays as it is (`127.0.0.1`, the nginx next to TimeVault). Reload nginx; TimeVault does not need a restart. The address after `set_real_ip_from` is the one the front nginx connects from, as the nginx next to TimeVault sees it (its access log shows it, and so does `started_from` of a refused single sign-on); it is not necessarily the address people type.

**Another way.** In the nginx next to TimeVault append instead of replace (`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`), put both proxies into the Site setting `trusted_proxies` (`127.0.0.1,10.135.36.81`) and restart TimeVault. TimeVault takes the right-most address of the header that is not a listed proxy, so what a browser writes on the left is not believed. The `real_ip` way keeps `trusted_proxies` as it is and makes the access log of that nginx show the browser.

Both ways were run against this version, through the front nginx and directly, with and without a forged `X-Forwarded-For`: TimeVault was told the address of the browser every time.

**Check it.** Open TimeVault through the front server and sign in: the **From** column of the Audit page shows the address of the browser, not that of a server. When a single sign-on is refused because of the addresses, the **Sign-in failed** entry and a warning in the service log say what TimeVault saw: `started_from` (the address it took for the browser), `sso_address` (the one the sign-on service recorded), `connection` (the address that connected: the nearest proxy), `trusted_proxy` (whether that address is listed in `trusted_proxies`) and `forwarded` (the `X-Forwarded-For` header, cut to 256 characters). If `started_from` is the address of a server, a proxy in front hides the browser: set it up as above. If `trusted_proxy` is `false` and `forwarded` is set, the proxy that connects is not in `trusted_proxies`. A browser that reaches the front server over IPv4 and the sign-on service over IPv6 (or the reverse) is also seen with two addresses; the two values show it.

**Not verified.** The company's own servers: the steps were run on one computer with a copy of the two `/tv/` blocks and loopback addresses for the servers.

## Tests

`go test ./...` runs everything. Tests that need PostgreSQL are skipped unless `TIMEVAULT_TEST_DATABASE_URL` points at a database; each test creates a private schema named `tvtest_<random>` and drops it, so use a separate test database such as `timevault_test`, never the working database. `dev-db.ps1` writes the URL for the private cluster into `.env`; if `.env` points at your own PostgreSQL server instead, create the test database there first (`CREATE DATABASE timevault_test OWNER timevault;`) and set `TIMEVAULT_TEST_DATABASE_URL` in `.env`; the role needs no other rights. Tests that exercise junctions and Windows file locking run only on Windows. The NTFS index is tested with a model of a volume everywhere, and by a live test that reads a real volume (`TestWindowsVolumeMatchesAWalkAndFollowsTheJournal` in `internal/ntfsindex`, Windows only): it needs administrator rights, so it runs from an elevated shell and is skipped otherwise; it only reads the volume and writes into a temporary folder that it removes (see [Search index](#search-index)). The Redis tests need no Redis: they use an in-process fake with pickle data written by Python.

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

1. Set the Site setting `sso_host` to the address of the sign-on service, `<sso.example.local>` (`host`, `host:port` or `http(s)://host[:port]`), on **Settings → Site settings**, group **Sign-in**. It is a setting of its own because the address may change. Restart the service (**Restart now**). The login page now has the button **Sign in with Windows (SSO)**, and the start-up log and the Site settings page show the sign-on service (`sso_host`). Without the setting the login page is as before.
2. The sign-on service has to write its results (`sso_login_<token>`) to the Redis that `TIMEVAULT_REDIS_URL` points at (the same database number), and the Redis account of TimeVault has to be allowed to read them. TimeVault only reads them; it never deletes the key, and the sign-on service lets it expire after 5 minutes.
3. Behind a reverse proxy (the usual case) choose one of two ways:
   - **Recommended.** List the proxies in the Site setting `trusted_proxies` (addresses or CIDR ranges separated by commas). The proxy has to put the address of the browser into the `X-Forwarded-For` header, and to pass the `Host` header unchanged, because the check that a request comes from the site itself compares `Origin` with `Host`. TimeVault then also writes the real address into the **From** column of the audit log and counts sign-in attempts per browser instead of per proxy. A tested nginx block is in [Behind a reverse proxy under a path](#behind-a-reverse-proxy-under-a-path).
   - Set the Site setting `sso_require_same_ip` to `false`. The address of the browser is then not compared with the one that the sign-on service saw. This is weaker; see ADR 0009.

   With neither, every single sign-in ends with "The sign-in could not be matched to this browser", because TimeVault compares the address of the proxy with the address of the browser.

   The same happens on a development PC that opens TimeVault directly (for example `http://localhost:8660`): TimeVault sees a loopback address (`127.0.0.1` or `::1`), while the sign-on service sees the network address of the PC. Open the site through the proxy address instead, or set `sso_require_same_ip` to `false` on that PC only.

**What a person sees.** The button opens a small window with the sign-on page, and the login page says "Waiting for the Windows sign-in". After a few seconds the window closes itself and the person is signed in, with the role of the account in Redis (`admin` or not), and lands on the page they came from. When something goes wrong the page says what: the browser blocked the window (allow pop-ups for the site), Windows could not tell who you are, the Windows account has no active account here, the sign-in could not be matched to this browser, the sign-in took too long or was already used, the window was closed, or the user accounts cannot be reached. The password form keeps working.

**Check it.** Open the login page and press the button. The **Audit** page then shows **Signed in** with `method: sso` and the Windows user; a refused attempt shows **Sign-in failed** with a `reason` (`no_active_account`, `no_windows_user`, `address_mismatch`, `browser_address_changed` or `result_unreadable`). For the two reasons about the addresses it also shows the addresses that were compared and how the request reached the server (`started_from`, `sso_address`, `connection`, `trusted_proxy`, `forwarded`; see [Two proxies in a row](#two-proxies-in-a-row)), and the service log has a warning with the same.

## Search index

Search, the sizes of folders in the Explorer and the scheduler's change probe all come from one place: an index that the service keeps in its own memory, of the files and folders below the monitored folders. It is built from the file records of the NTFS volume (its master file table) and kept up to date from the change journal (the USN journal) of the volume. It needs no other program, no HTTP server and no setting beyond the Site setting `search_index` (`auto` by default; `off` switches it off). It answers what exists *right now*; it is not a history search: browsing the past uses the catalog. The reasons are in [ADR 0018](./adr/0018-native-ntfs-index-replaces-everything.md).

**Requirements**

- **Administrator rights.** Windows lets only a process that runs as an administrator (or as the account SYSTEM) open a volume for reading, so the service has to run that way, for example from an elevated PowerShell. The service only reads the volume; it never writes to it. Without the rights the Status page shows the search as `degraded` ("The service has no administrator rights, so it cannot read the volumes to index them."), search answers "unavailable", the folder sizes show "-" and the scheduler scans as it always did; nothing else is affected.
- **A local NTFS volume with a drive letter.** A monitored folder on a network share (`\\server\share\...`) is not indexed, and neither is one on a volume that is not NTFS (the Status page says which volume cannot be indexed and why): its search answers "unavailable", its folder sizes show "-" and its scans are never skipped. Only NTFS was tried; ReFS, FAT and exFAT volumes were not.
- **An active change journal** on the volume. If it has none, the Status page says so and gives the command that creates it (`fsutil usn createjournal m=33554432 a=8388608 <drive>:`). This case was only tested with a model of a volume, not on a real volume without a journal.

**What is held.** Only the monitored folders: the file records of the volume are read, and everything outside the monitored folders is dropped when the build is done. For every file and folder the index holds its name, its size (the length of the unnamed data stream, which is what a backup reads), the time of its last write and the folder it is in. A junction or symbolic link (reparse point) is not entered, a file with several hard links is held under its first name only, and short (8.3) names are skipped. It takes about 230 bytes of memory for each held entry: one million files are about 230 MB.

**Building.** The index of a volume is built in the background when the service starts and when the monitored folders of that volume change (one is added, moved or removed): the file records are read twice, folders first and then what is inside the monitored folders. That takes seconds, not minutes, and depends on the volume rather than on how much is held (13 seconds on a volume with 2 million entries). While a volume is being built, search answers "unavailable" for its folders (the Status page says "The index of C: is being built."); an index that is being replaced keeps answering until the new one is ready. It is built again when the change journal was replaced or overwritten before the index had read it (the journal wrapped around), and, at most once a minute, when a folder was moved into a monitored folder from outside (the index cannot know what is inside it).

**Staying current.** The service reads the change journal every 10 seconds, and before it answers a question when it did not do so in the last 2 seconds (the scheduler's probe always does), so a file that was created, written, renamed, moved or deleted a moment ago is found: in a test on the real service they showed in a search within 2 seconds. The size and time of a file that changed are read from the disk shortly after.

**What it answers**

- **Search** (the box in the **Explorer**). The typed text is split into terms (whitespace separates them, double quotes make a phrase; at most 256 bytes and 16 terms). `*` and `?` act as separators, because they cannot be part of a Windows file name. Terms are plain text: `|`, `!`, `<>`, `regex:`, `size:` or `content:` are searched for as they are typed. A name matches when it contains every term, ignoring case. The search stays inside the monitored folder, and every result is checked against its root again; anything outside is dropped and counted, never shown. Results come folders first, then by name, in the same order on every page; pages hold at most 200 results (50, 100 or 200 can be chosen) with page links.
- **Folder sizes in the Explorer.** The Explorer asks the index for the folders inside the folder on screen and shows their size (the sizes of all files below, recursively), so a folder row has a size and folders can be sorted by size. The modified time of a folder is its own, read from the disk. The answer comes from the index, so it can lag a little behind the disk. TimeVault keeps an answer for 10 seconds, waits at most 2 seconds for it and, when the index does not answer, shows "-" and does not ask again for 15 seconds. The Time Machine and the versions page read the catalog and show no folder sizes.
- **Change probe for the scheduler.** Whether anything below a folder changed since a time, and how many entries are below it; see [Scheduler settings](#scheduler-settings).

**Status.** The **Status** page shows the search as `ok` ("N entries held on C:.", or "There are no monitored folders to index." while nothing is monitored), `degraded` (a volume is being built, or cannot be indexed: no administrator rights, no change journal, not NTFS, or another failure that the log of the service names) or `disabled` (the Site setting `search_index` is `off`). `GET /api/v1/health` has the same in its `search` component. The log has one line when a volume has been indexed (`ntfs index: the volume is indexed`, with the volume, the number of entries and the seconds it took) and one warning when it could not be.

**Verified and not verified.** Verified on Windows 11 with the NTFS volumes of the development computer, from an elevated shell: an index of real folders equals a walk of the same folders in names, sizes and times, and follows a created, written, renamed, moved and deleted file or folder (the live test `TestWindowsVolumeMatchesAWalkAndFollowsTheJournal`); an index of 2.0 million entries (`C:\Windows`, `C:\Program Files`, `C:\Program Files (x86)` and `C:\Users`) was built in 13 seconds, held 453 MB, and a search below the biggest of the folders (1.75 million entries) took about 0.13 seconds; and the real service, started on a new database, did this from one end to the other: it built the index of a monitored folder in 14 seconds, found files with several terms and in other letter case, gave the right folder sizes, showed a file that was created, deleted or renamed within 2 seconds, skipped the scan of an unchanged folder once the index had watched for about 7½ minutes (the 5 minutes the probe looks back, the 1 minute interval and the gap), and scanned again and made a recovery point when a file had changed after that. Not verified: ReFS, FAT or exFAT volumes, a network share, a volume without a change journal and a journal that wraps around while the service runs (a model of a volume only), a service started as a scheduled task or as the account SYSTEM (only an elevated shell was used), and a monitored folder of 500 GB or one million files on a real volume (the figures above come from the folders listed and the arithmetic of the per-entry cost).

## Scheduler settings

Automatic backups are set per folder on the **Settings** page ("Back up automatically every N minutes"). The settings below, Site settings of the group **Scheduler** (and `process_priority`), only decide how the backups that are due are spread out, so that a server with, say, 10 monitored folders does not scan them all at once. The reasons are in [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md); the change probe now asks the NTFS index ([ADR 0018](./adr/0018-native-ntfs-index-replaces-everything.md)).

- **One at a time.** `scheduler_parallel` = `1` (1 to 4). A backup started with **Back up now** is not limited and does not count.
- **Gap.** `scheduler_gap` = `auto` is the gap that just keeps up with all the schedules, `1 / Σ(1 / interval)`, within 30 seconds and 10 minutes: ten folders with an interval of 60 minutes start one every 6 minutes. A duration such as `5m` fixes it; `0s` turns the spacing off.
- **Rest.** `scheduler_rest` = `auto` waits after a run for as long as it took (30 seconds to 10 minutes), so the long scan of a big folder is followed by a long pause. The next start is at the later of the gap after the previous start and the rest after the previous end. `0s` turns it off. It only applies while `scheduler_parallel` is 1.
- **Order.** Folders without a recovery point first, then the most overdue in proportion to its own interval, then the one whose last run was the quickest, then the lowest id. How long the last run took is shown on the Settings page ("Last run took").
- **Priority.** `process_priority` = `below_normal` is the default, so that backup scans give way to the file shares of the same computer. `background` also lowers the disk priority; `normal` is how it was before 0.1.4.
- **Change probe.** `scheduler_probe` = `true` (off by default) makes the scheduler ask the NTFS index first and skip the scan of a folder that did not change. It needs the index of the folder to be ready (see [Search index](#search-index)). A folder is scanned anyway when the index does not know the folder, is not ready or does not answer within 3 seconds; when the number of entries below the folder is not the number at the last complete scan (this is how a deletion shows); when anything below it was created, modified, renamed, moved, deleted or had its attributes changed since 5 minutes before that scan; when the index has not been watching the volume since 5 minutes before that scan (see below); when the last recovery point has items that were not captured; when the exclude patterns of the folder were changed since that scan; and at least every `scheduler_full_scan_every` (24 hours). The index counts a change anywhere below the monitored folder, so a probe says "changed" more often than strictly needed, never less often. A tick asks the index at most 25 times, and an index that fails to answer is left alone for a minute. With `log_level` `debug` the log says "scheduled scan skipped: the search engine sees no change" for every skip. A skipped folder leaves nothing in the lists, like any run that finds nothing changed, and its interval starts again.
- **After a start or a rebuild of the index.** The index knows nothing about what happened before it began to watch the volume, so a folder is not skipped until its last complete scan was made at least 5 minutes after that moment. After a restart of the service, or after the index of a volume was built again (a monitored folder was added, for instance), every folder is therefore scanned once more as usual (for an interval shorter than 5 minutes, until 5 minutes have passed) before skipping resumes. A restart for the Site settings costs the same. The entries below a folder are counted right before every scan, and a later probe compares with that count; a scan that ran while the index was not ready (the first one after a start, for example) has no count, so the next probe cannot skip.
- The pace is kept in memory: after a restart the first automatic backup may start at once.

## Releasing and automatic updates

A server whose Site setting `update_url` is set replaces its own program when a newer release is published: it waits for the jobs that run, swaps the file and restarts, without anybody at the server. The decisions and the alternatives are in [ADR 0014](./adr/0014-self-update-from-a-release-manifest.md).

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
      "latest_version":  "1.0.1",
      "sha256":  "<64 hexadecimal characters>",
      "package_url":  "timevault.exe",
      "size":  23352320
  }
  ```

  `package_url` is the file name, which a service looks up next to the manifest it read (right for a folder, a share, or a web folder that holds both files), or, with `-BaseUrl`, the full address of the program. A hand-written manifest works too: a leading `v` in the version and unknown fields are accepted.
- Copy `timevault.exe`, then `timevault.json`, into the **release folder** that the servers read. A service that reads the folder while it is being copied sees a size or a hash that does not match and tries again at the next look; it never installs a half-copied file.

**Where the servers read it.** `update_url` is one of:

- a folder or a share, `D:\TimeVault\release` or `\\server\share\tv-release` (the account that runs the service needs read access to it; a share that cannot be reached when the service starts is found as soon as it can be); or
- an `http(s)` address of the manifest or of its folder, served by any web server that serves the two files as they are. **The static files of the other company applications are the place for it:** the manifest is `http://<server.example.local>/api/static/cd/timevault.json`, and its `package_url` of `timevault.exe` (a file name, no address) leads to `http://<server.example.local>/api/static/cd/timevault.exe`. Copy `timevault.exe`, then `timevault.json`, into the `static\cd` folder of that application (make the folder once); the address is entered as it is, it is **not** derived from `server_host`. That application answers `202` and a picture for a file that is not there, so until the files are copied the Settings page says **Problem** with `… answered 202 Accepted`. Each look (every 10 minutes by default) is one request to that application, which writes it to its web log. Any other web server works the same, for example nginx: `location /tv-release/ { alias D:/TimeVault/release/; }`. Redirects are followed (at most five). An answer other than `200` (a `202` or a `404`, say) is an error.

**Turn it on at a server (once).**

1. Copy `timevault.exe` of version 0.1.8 or later to the server, next to the `.env`. This is the last manual copy: older versions cannot update themselves.
2. Put `TIMEVAULT_AUTO_MIGRATE=true` into the `.env` (a service with an update address refuses to start without it, because nobody runs `migrate` when the service updates itself). Then, as the local administrator, set the Site setting `update_url` to the address of the manifest, for example `http://<server.example.local>/api/static/cd/timevault.json`, and if you want `update_interval` to `5m` (**Settings → Site settings**, group **Automatic updates**), and restart the service.
3. The account that runs the service needs **Modify** on the folder of `timevault.exe` (it writes `timevault.new.exe`, `timevault.old-<version>.exe` and `timevault.exe.update.json` there) and **Read** on the release folder.
4. Start the service. The start-up log says `automatic updates are on` with the address and the interval, and warns when the folder of the program cannot take a new file. The **Settings** page has a card **Program updates**: the state, the released version, when the manifest was last read, and, for administrators, the address of the release, the last problem and **Check now**.

**What happens when a release is published.**

1. At the next look (within the interval, at once when an administrator presses **Check now** or calls `POST /api/v1/update/check`, and 10 seconds after the start) the service finds a version that is not the running one. It downloads the program to `timevault.new.exe`, checks its size and sha256, and runs it with `version` (it must print the version of the manifest) and `check-config` (it must accept the settings of this service).
2. It stops taking jobs: the Settings page says **Waiting for the running jobs** with their number, and every signed-in account sees a notice under the header (_Version 1.0.1 will be installed as soon as the running jobs have finished. New jobs wait until then._). The jobs that run finish; new ones, also those of the scheduler, wait in the queue. There is no time limit.
3. With no job running, it renames the running `timevault.exe` to `timevault.old-<version>.exe`, puts the new file at the name `timevault.exe`, writes the note, stops the scheduler, the web server and the job runner, and starts the new program. The queued jobs run then.
4. The new program logs `automatic update: this program has taken over` and writes the audit event; the **Audit** page lists the **Program update** events of the actor `updater` (`staged`, `restart`, `done`, and `cancelled` or `failed`), with no path and no error text in the details (the log of the service has them).

If the release goes back to the version that runs, the waiting ends and the jobs are taken again. A problem (a manifest that cannot be read, a program with the wrong size, hash or version, settings that the new program refuses, a swap that fails) is shown on the Settings page (the text only to administrators), logged once, and tried again at the next look; the service goes on with the old program. A wrong size or hash is tried again too, because it is what a release that is still being copied looks like; a program whose version is not the one of the manifest is not tried again until the manifest changes. A download of an `http(s)` address that receives no data for two minutes is given up, and a program that was removed after the download (a virus scanner, say) is downloaded again, both at the next look.

**How the program is started after an update (`update_restart`).**

- `supervise` (default): the process that was started stays alive as a small **launcher** and runs the new program as its child, with the same arguments, environment, folder and console. In a console window Ctrl+C stops both; as a scheduled task the task keeps one running process and needs no "restart on failure". When the launcher ends in any other way, for example when the task is ended (the scheduler kills the process at once), the program notices that the launcher is gone, shuts down in order and frees the port, so ending the task leaves nothing behind. The launcher is the first program that was started and keeps its code: an update replaces the file on disk, not the launcher, so a change in the launcher itself needs a restart of the launcher. While it lives, the file it was started from (`timevault.old-<version>.exe`) stays locked.
- `exit`: the process ends with the exit code **75**, and whatever started it has to start it again. Use it when a service manager runs the program, for example NSSM with `AppExit 75 Restart` (not tried: NSSM was not available for the tests). With the Windows Task Scheduler, a task whose trigger repeats every minute with **Do not start a new instance** (`MultipleInstances IgnoreNew`) starts the new program at the next repetition, and a repetition while the program runs starts nothing: this was tried. The setting **If the task fails, restart every …** was tried too, and it did **not** start the program again after it ended with 75, so do not rely on it. A program that was started by a launcher always exits with 75, whatever the setting says, because the launcher waits for that code.

**Going back.** The previous program is `timevault.old-<version>.exe` (the newest three are kept). **Put the older release in the release folder first, or empty `update_url`:** otherwise the service updates itself to the newer one again at the next look, because the version in the manifest is not the one that runs.

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

The header, the audit log, the job page and the job lists, the summary of who has access to a folder and the people lists of the permission editor show the picture of an account next to its name. The browser reads each picture from the Site setting `avatar_url`. Not set, that is `http://<server_host>/api/static/hr/{id}.webp`: the pictures of the employees that the other company applications keep, on the server named in `server_host` (`{id}` is the employee ID). While `server_host` is not set there are no pictures, unless the address is written down.

- **The setting.** `off` (or `none`) turns the pictures off. A full `http(s)` address reads them from another host. A path such as `/api/static/hr/{id}.webp` reads them from the host the site is opened from (it is not put under `base_path`), which suits a site whose proxy serves that application under every name; opened directly on its own port, the service has no such path and everybody has the default picture. `{id}` must be there once, and the address must not carry a user name or a password.
- **The browser reads them, not the service.** The service never fetches or serves a picture and stores nothing. The site and the pictures may be on different hosts, so the content security policy of every page lists the host of the picture address for images, and for nothing else (`img-src 'self' data: http://<host>`). A host name with an underscore, such as `prod_eng.co.example.local`, cannot be written in a policy (the browser ignores that whole entry), so the domain around it is listed instead: `http://*.co.example.local`. When no domain of two labels is left (`prod_eng`, `prod_eng.local`), the pictures only show when the site is opened by that same address, and the start-up log says "cannot be named in the content security policy". A site served over `https` needs an `https` address: a browser does not show pictures from `http` in an `https` page.
- **A person without a picture.** The other application sends the pictures as `text/plain`, which an image does not mind, and answers `202` with a placeholder picture of 480×320 pixels instead of `404`. A picture of exactly that size, a failed request and a host that does not answer all leave the default picture. This was the reason the pictures did not show in 0.1.8, which only accepted `image/*`.
- **How often.** The browser asks for each picture once for as long as the page lives, and only for the pictures that are about to be seen (the people list of the permission editor is long). A missing picture is remembered for ten minutes of the browser session, so that a list does not ask again and again and a host that was down is tried again soon.
- No picture is asked for the local administrator, for the names that the service uses itself (`scheduler`, `updater`, `system`) or for a name that cannot be an employee ID.

## Web log

Since 1.0.0 the service appends lines to the **web log** that the company's applications share: the Redis list `web_log`, in the Redis of `TIMEVAULT_REDIS_URL` (the same database number). It is on by default when `TIMEVAULT_REDIS_URL` is set. It is the first time TimeVault writes to Redis: the accounts and the single sign-on results are still only read, and `internal/redisuser` still never writes. The decision and the alternatives are in [ADR 0016](./adr/0016-web-log-in-the-shared-redis-list.md).

**Settings** (Site settings, group **Logging**; see [Site settings](#site-settings))

- `web_log`: `auto` (the default) is on exactly when `TIMEVAULT_REDIS_URL` is set; `on` (`true`, `1` and `yes` mean the same) needs `TIMEVAULT_REDIS_URL`, and the Site settings page refuses it without; `off` (`false`, `0`, `no`) never writes. **To switch it off, set `web_log` to `off` on the Site settings page and restart the service.**
- `web_log_key` (default `web_log`): the list. ASCII letters, digits and `_ . : -` only, at most 128 characters. It must not start, in any case, like the keys of the accounts (`TIMEVAULT_REDIS_KEY_PREFIX`, `app_user_` by default) or of the single sign-on results (`sso_login_`), so that this setting can never make the service write into their namespaces; the Site settings page refuses such a key.
- `web_log_host`: the last field of every line. Not set: the name of this computer in lower case. A value is used as written; ASCII letters, digits and `. - _` only, at most 63 characters.
- The Redis account of `TIMEVAULT_REDIS_URL` has to be allowed to `RPUSH` to the list, which is the only thing TimeVault writes.

The start-up log has `web_log`, `web_log_key` and `web_log_host` in the line `starting TimeVault` and, when the web log is on, a second line, `web log is on`, with the list, the host name and the Redis as `host:port/db` (never the password). The three settings are on the **Site settings** page, which checks them when they are saved (the service checks them again when it starts), and `GET /api/v1/settings` has `web_log` for the local administrator.

**A line.** Every element of the list is `zlib.compress(pickle.dumps(line, protocol=4))`, written the way Python writes it, so that what reads the lines of the other applications reads these too. The line is six fields told apart by ` | `, the same as theirs:

```text
ip | YYYY-MM-DD HH:MM:SS.ffffff | message | status | level | host
10.0.0.5 | 2026-11-18 14:05:12.250431 | TimeVault: /explorer user=E123456 0.112(s) | 200 | info | sgmeslt595
```

- `ip` is the address of the browser (the connection, or what a proxy of `trusted_proxies` reports), and `127.0.0.1` for what the service does on its own (the scheduler, the updater, start and stop). The time is the local time of the server. `status` is the HTTP status, or 200, 403 or 500 for what the audit trail and the scheduler report. `level` is `info`, `warning`, `error` or `debug`.
- TimeVault never adds a field, so what splits a line by position keeps working: the user is in the message as `user=<employee ID>` (the local administrator under its name, `guest` for a visitor who is not signed in). Every message starts with `TimeVault: `. A `|` in a message becomes `/`, a control character becomes a space, and a message is cut at 400 characters, so a line always has six fields.

**What is written.**

- **A page that somebody opened,** every `GET` of a page: `TimeVault: <route> user=<employee ID or guest> <seconds>(s)`, for example `TimeVault: /explorer user=E123456 0.112(s)`. The route is the pattern that matched, with the numbers filled in (`/explorer`, `/jobs/12`), never the address as it was typed (no query, no path, no search text); an address that matched nothing is `(not found)`. `info` for a `2xx`, `warning` for a `4xx`. Not written: `/static/…`, `/healthz`, `/api/…`, redirects (`3xx`; the page that they lead to is written when it is opened), and a reload that the page script made by itself, which carries the request header `X-TV-Auto: 1` (the job page reloads itself while its job runs, and the Backups page when a job that it shows has finished).
- **A request that failed on the server,** any `5xx` (also of the API) and a panic: `TimeVault: error <route> user=<employee ID> ref=<reference>`, `error`, with the status that was answered. `ref` is the reference that the error page shows and that the console log holds as `id=` together with the error text (the text itself is never written to Redis); a `5xx` that has no reference of its own, such as the `503` for a folder that cannot be read, has no `ref=`.
- **Every audit event,** after it is stored in the database, so what people do and what the scheduler and the updater do: `TimeVault: <action> [method=… route=…] [stage=… from=… to=…] [source=<folder id>] [<target>=<id>] user=<actor> [<seconds>(s)]`. For example `TimeVault: backup.request source=3 user=E123456`, `TimeVault: backup.finish source=3 point=17 user=scheduler 12.400(s)` and `TimeVault: access.denied method=POST route=/settings/sources user=E1001`. The result decides the status and the level: `ok` is 200 and `info`, `denied` is 403 and `warning`, `failed` is 500 and `error`. The address is that of the request (`127.0.0.1` for the scheduler and the updater). Of the detail of an event only the method and the route of a refusal and the stage, `from` and `to` of a program update are used; the name that was typed at a failed sign-in, which may be a password, is never copied (`user=-`), and an event without an actor is `guest`.
- **The scheduler:** `TimeVault: scheduler.queued source=<id> user=scheduler` (an automatic backup was queued; `info`), `TimeVault: scheduler.error [source=<id>] user=scheduler` (a problem that the scheduler also warns about in the console, at most once per folder and minute; `warning`, status 500) and, at the debug level only, `TimeVault: scheduler.skipped source=<id> user=scheduler` (the NTFS index saw no change).
- **The service:** `TimeVault: started version=<version>` once everything is built, and `TimeVault: stopped` when it stops.
- **Debug lines,** written only with `log_level` `debug`: the page line of `GET /api/…` (the polling of a job) and of the reloads that the page script makes by itself. The other log levels change nothing in the web log: `warn` and `error` do not take the `info` lines out of it.

**Never written:** a path, the name of a file or a folder, a query string, a text that somebody typed (a search, or the name typed at the sign-in form) or an error text. A line holds routes, numbers (monitored folder, recovery point, job), employee IDs and addresses.

**Limits, and what happens when Redis is down.** The web log is best effort and never gets in the way of the service.

- `Write` only puts the line into a queue of 1,024 and returns, so a request, an audit record or a scheduler run never waits for Redis. A worker sends up to 100 lines with one `RPUSH`, through a pool of 2 connections of its own; each send waits at most `TIMEVAULT_REDIS_TIMEOUT`.
- A flood is cut at the door: page lines at 10 per second on average (a burst of 100) and event lines (audit events, errors, the scheduler, start and stop) at 20 per second (a burst of 200). A full queue drops the line. A visitor who clicks like mad cannot flood the shared list. The lines that the limits and a full queue drop are counted and reported with the warning `web log: lines were dropped because too many came at once`, at most once a minute.
- After a failed `RPUSH` the worker leaves Redis alone for 30 seconds, then longer, up to 5 minutes, and drops what comes meanwhile. It warns once, `web log: the lines cannot be written to Redis, so they are dropped until it answers`, with the password of the URL masked, again at most every 5 minutes while it lasts, and logs `web log: Redis takes the lines again` when it takes lines again. A dropped line is not written later. The service itself starts and works as before while Redis is down.
- At shutdown the queue is sent first, for at most 3 seconds (`web log: the last lines could not be written before the service stopped` when that was not enough).
- TimeVault never trims or expires the list: it is shared, and the other applications manage it. A line is about 100 to 150 bytes in Redis. The volume is bounded by the limits above but it is not zero. The web log is not a replacement for the audit trail: it is lost while Redis is down, and an audit event reaches it only after it is stored in the database.
- The lines hold employee IDs and client addresses, which everybody who can read the list reads.

**Look at it (read only).** `LLEN web_log` counts the lines and `LRANGE web_log -5 -1` shows the last five; use the same host, port, database number and password as in `TIMEVAULT_REDIS_URL`, and never a command that changes the list. The elements are compressed pickles, so they look like binary data. A person reads one with Python (only from a list that you trust: `pickle.loads` runs what the data tells it to), or on the log page of the other application:

```python
import zlib, pickle
print(pickle.loads(zlib.decompress(element)))   # element: the bytes of one list element
```

**Check it.** Open a page and look at `LLEN web_log` again: it grows within a moment. If it does not, see [Troubleshooting](#troubleshooting).

**Not verified.** The web log was not written to the company's Redis: it was tested with an in-process Redis and with pickles that Python printed. That the Redis account may `RPUSH` there, and that the log page of the other application shows these lines, is to be looked at after the first start.

## Operating

- **Who may do what.** Anyone can browse (Explorer, Time Machine, file versions, the state of the Backups page and of backup jobs) the folders that let guests browse, without signing in; a folder that does not is not there for a visitor at all. Everything else needs an active account, and what an account may do with a folder is decided for each folder: **Browse** (also restoring, and the jobs and audit events of the folder), **Back up** (back up now, full re-read, verify, stop jobs) and **Full control** (change the folder's settings and who may do what with it); see [Folder permissions](#folder-permissions). The audit log (events of the folders the account may browse, and the sign-ins and other events that belong to no folder), the status page and the settings page are for every signed-in account. Only an `admin` account (Redis `Type` is `admin`, or the local administrator) adds monitored folders and has full control of all of them, and only an administrator sees the folders on the server's disk; everybody else sees display folders. See [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md), [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md), [ADR 0013](./adr/0013-per-folder-access-levels.md) and [api.md](./api.md#who-may-call-what).
- **Browsing long folders.** The Explorer and the Time Machine list a folder in pages (100 items by default; 50, 100, 200 or 500 can be chosen, with the count and page links above and below). The **Type** column shows the extension in capitals ("Folder" for folders, "File" for a file without an extension); the **Type** drop-down above the list keeps only one kind (all folders, all files, a file without an extension, or one extension, each with its count). Click **Name**, **Type**, **Size** or **Modified** to sort the whole folder, and click again to reverse it; folders always come first. The choice is in the address, so a link keeps it. **Select all** in the Time Machine selects the items on the current page, and a restore can take up to 500 paths. At the bottom right there are four round buttons: two arrows that jump to the top and to the bottom, which appear on a long page only, and the **Documentation** and **Guide** buttons, which are always there (next item). A folder of 10,000 files opens in about 0.1 s and one of 50,000 in about 0.5 s (measured on the development machine, with all files in one folder); several hundred thousand entries in one folder take seconds, because every page reads the whole folder.
- **User guide and page guide.** The **Documentation** button (the book, at the bottom right of every page except the sign-in page) opens the user guide in a new tab: `GET /help`, a page of the application that is open to everybody and has an English and a Chinese version (the language of the EN / 中文 switch); it explains what TimeVault is for and how each page is used. The **Guide** button (the compass) starts a step-by-step tour of the page you are on: the page is dimmed, one part is lit at a time, and a bubble says what it is, with "Step n of N", **Back**, **Next** (**Done** on the last step) and a close button. The right and left arrow keys go on and back, **Esc** closes it, and **Enter** presses the button that has the focus; clicking the dimmed page does nothing, and nothing is clicked or changed for you. The parts of the page itself come first, then the header (the navigation, the theme, the language, the account or the sign-in) and the round buttons; a part that the page does not show (a button that only some visitors get, for example) has no step. On a narrow screen (640 pixels or less) the bubble sits at the bottom of the window (at the top when it explains the round buttons). The guide ends when another page is shown or the back button is used, and it goes on when the same page refreshes itself, as the job page does while its job runs. The sign-in page has neither button. The guide is `web/assets/js/tour.js`; the steps are written in the templates (`Page.Tour`) and the texts are in `internal/server/i18n_tour.go`. `help` is a page of the application, so `base_path` may not start with `/help`.
- **Health.** `GET /healthz` returns `ok` without signing in and only proves the process is up. The **Status** page (accounts only) checks the database, the repository (a write test and free space; "degraded" below 5% free), the search index, the Redis user directory ("degraded" when it does not answer: browsing, existing sessions and the local administrator still work), the restore folder and the job queue, and lists VSS as "disabled". Without a session only `/healthz`, the sign-in page, the language switch, the user guide (`/help`), the static assets and the browsing pages work.
- **Logs.** The service writes its log to the console (standard error), as text or, with `log_format` `json`, as JSON; `log_level` sets the level. Every request except the static files and `/healthz` has one line, `msg=request`, with `ip=<address of the browser>` (the connection, or the browser address that a proxy of `trusted_proxies` reports), `user=<employee ID>` (`guest` for a visitor who is not signed in; the local administrator under its name, `local-admin` when sign-in is switched off), `method`, `path`, `status` and `ms`. The user comes from the session cookie only, without asking the user directory, and the request that signs somebody in (`POST /login`, `POST /auth/sso/poll`) is logged as the account that signed in. `request failed` and `panic while handling a request` carry the same `ip`, `user`, `method` and `path`, and `id`, the reference that the error page shows (the web log has it as `ref=`); a request that panicked also has its `request` line, with `status=500`. `job finished` and `job finished with an error` carry `by=<who requested the job>` (`scheduler` for an automatic backup). Anything that reads these lines by position has to expect `ip` and `user` first, after `msg=request`. The web log in Redis is a separate thing: see [Web log](#web-log).
- **Monitored folders.** An administrator adds them on the **Settings** page (a local folder; it must not overlap the repository, the restore folder or another monitored folder; give it its display folder too, see [Where settings live](#where-settings-live); and say who may see and use it, see [Folder permissions](#folder-permissions)). An administrator, or whoever has full control of the folder, can edit the name, the display folder, the exclude patterns, the on/off switch, the schedule, the versions kept and the permissions at any time. The folder on the server itself can be corrected, by an administrator, until the first recovery point exists (in any state, even a failed one); after that, switch the old one off and add the new one. A switched-off folder keeps its history and is still browsable; it cannot be backed up.
- **Backups.** Any signed-in account that has Back up (or Full control) on a folder can press **Back up now** in **Backups**. A normal backup trusts size and modification time to skip unchanged files; **Full re-read** reads and hashes every file again. Only one backup per source runs at a time. A backup that cannot read a file (locked, changed while reading, link, special file) records it as skipped with the reason, keeps the previous good version, and still completes with a warning.
- **Automatic backups.** Tick "Back up automatically every N minutes" for a folder (1 to 43200). The service looks every 15 seconds; a folder is due when it never ran or its last run ended N minutes ago, whatever the outcome, and a run that finds nothing changed records nothing: no recovery point, no job, no audit event. Setting a schedule, or switching a folder on again, starts a backup at once when the last run is older than the interval. Folders that are due start one at a time and spread out (see [Scheduler settings](#scheduler-settings)), so a folder can start later than the moment it is due. Without a limit of versions per file (next item) a short interval on a folder that keeps changing grows the repository without bound. See [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md) and [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md).
- **Versions kept per file.** Each monitored folder has a limit (Settings, "Versions kept per file", 1 to 10000; 0 or empty keeps everything). After each backup the files it changed are looked at: when a file has more versions than the limit, its older ones are deleted for good, and repository content that no version needs any more is freed (only while no backup runs, so it can take a while to show in the free space). A recovery point that lost versions is marked **Partial** on the Backups and Time Machine pages: it no longer shows the files whose versions were deleted, so a restore from it can be incomplete, and verifying it checks the objects but not the manifest. Setting a limit on a folder that has history, or lowering it, queues a **Version cleanup** job for the whole folder; lowering it first asks for a tick ("I understand that these versions are deleted for good") next to a count of the versions and files that would go. A cleanup is also queued when the deletion right after a backup could not run, because a restore or verification of the folder was queued or running or because it failed: it would otherwise be forgotten, as the next backup only looks at the files it changed itself. A queued cleanup holds no worker; it starts when no backup, restore or verification of the folder is running, and while it runs the backups of the folder wait in the queue. While a cleanup runs, restores and verifications of the folder are refused, and so is a change of its limit; a restore or verification that was queued before waits for it to end. The audit log records **Old versions deleted**. See [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md).
- **Verify.** The verify job on a recovery point checks the catalog against the manifest digest and that every referenced object exists with the right size; "deep" also re-hashes the content. Run it after copying or restoring the repository.
- **Restore.**
  1. Sign in, open **Time Machine**, choose a source and a recovery point, then tick files or folders and choose **Restore selected…**, or choose **Restore this folder…** / **Restore everything…**. The version page of a single file has **Restore…**. Visitors who are not signed in see a **Sign in to restore files** link instead. Whoever may browse a folder may restore from it.
  2. Review the preview: counts, size, conflicts, blocked items (missing content, unsafe names, never captured) and the planned changes.
  3. Choose the destination folder name (a folder below `TIMEVAULT_RESTORE_ROOT`, created if it does not exist), the conflict policy (skip, rename, overwrite) and, if you accept restoring only part of the selection, the partial option. Overwriting needs an extra confirmation.
  4. **Start restore** within 15 minutes of the preview (the token expires and is bound to your user and the exact plan; any change needs a new preview). Follow the job page. Do not start another restore into the same folder until it has finished. Copy the result from the destination to its final place yourself: the preview and the job page name it by its display folder (`<restore_display_root>\<destination>`), which is where you find it over the network; administrators also see `<TIMEVAULT_RESTORE_ROOT>\<destination>`. Restores never write into a source.
- **Audit.** The **Audit** page (accounts only) lists sign-ins and failed sign-ins (with the account name as it was typed, cut to 64 characters; a single sign-in shows its method and the Windows user), refused requests, changes to monitored folders (the old and new value of every field that changed, the access lists included), backups, verifies, restore previews and results, the deletion of old versions, cancellations, and the steps of a program update (**Program update**, by the actor `updater`; see [Releasing and automatic updates](#releasing-and-automatic-updates)). An administrator sees all of them; everybody else sees the events of the folders they may browse and the events that belong to no folder (sign-ins and the like). The **Duration** column shows how long a backup, a verification, a restore or a cleanup ran, and for a backup the speed of the data it copied: the first backup of a big folder tells how fast the disk and the repository are (a later backup copies little, so its speed says little). The application has no way to change or delete these records, and a database trigger rejects UPDATE and DELETE statements on them. Automatic runs that find nothing changed leave no record.
- **Backing up TimeVault itself.** Back up both the PostgreSQL database (for example with `pg_dump`) and the whole repository folder. The repository only grows, so an older database copy with a newer repository is consistent, but a newer database with an older repository reports missing objects (verify shows them and restores block). After restoring either, run a deep verify.

## Troubleshooting

| Symptom | Cause and action |
|---|---|
| The service stops with "is not a loopback address" | Use `127.0.0.1` behind a reverse proxy, or set `TIMEVAULT_ALLOW_NON_LOOPBACK=true` knowingly. |
| The service stops with a schema message | Run `timevault-server migrate`, or set `TIMEVAULT_AUTO_MIGRATE=true`. |
| The service stops with "no way to sign in" | Set `TIMEVAULT_REDIS_URL` (company accounts) and/or `TIMEVAULT_ADMIN_PASSWORD_HASH` (a local administrator; create the value with `timevault-server hash-password`). |
| The service stops with an error about `.env` | The message names the file and the line number, never the line. `TIMEVAULT_ENV_FILE` must name an existing file. |
| The Site settings page refuses `server_host` or `restore_display_root` (or the service stops with an error about them) | The message names the setting and what is wrong, never the value. The host name may only hold letters, digits, `-`, `_` and `.`; the display root must look like `\\server\share\folder`, with a share that does not end in `$`, and must be on `server_host`. |
| Adding or editing a monitored folder is refused with "Enter the display folder" or "Folders on other servers cannot be monitored here" | `server_host` is set, so the display folder is required and its host must be that server (the message names both). Folders that were added before the setting existed have no display folder; give each one on the Settings page. |
| The service stops with "another TimeVault service is already running against this database" | A second `timevault-server` uses the same database (schema). Stop the other one first; its lock is released when its process ends. |
| The service stops with "not a TimeVault repository" | `TIMEVAULT_REPOSITORY_PATH` points at a non-empty folder without `timevault-repository.json`. Use an empty folder. |
| `go` is not found by the scripts | Add Go to `PATH`, or put the line `TIMEVAULT_GO_ROOT=<folder that contains bin\go.exe>` into `.env`. Setting the variable in one terminal only lasts for that terminal. |
| After a rebuild the footer, the Settings page or `timevault-server version` still shows an old version number | Either the build command still has `-X main.version=<old number>` (a copy of an older command), so the program has the new code under the old number: build with the command in [Building the executable](#building-the-executable), which has no `-X`. Or the service still runs the old file: Windows does not let a running program file be replaced, so stop the service, copy the new file, start it, and run `version` on the file that is really started. A build made with `-trimpath` does not record `-ldflags`, so `go version -m` cannot show the flag; `version` is the check. |
| The service listens on another port (or uses another setting) than the one written in `.env` | A `TIMEVAULT_*` variable in the process environment beats `.env`. The start-up log has a warning that names every setting it overrides (`TIMEVAULT_LISTEN_ADDR`, for a port). The usual cause is a variable left in the terminal by an older `dev-run.ps1`: start the service with `scripts\dev-run.ps1`, or load the session with `. .\scripts\dev-env.ps1`, which remove such variables. If it comes back, it is set in your Windows user or system environment. |
| `dev-db.ps1` cannot find PostgreSQL | Set `TIMEVAULT_PG_BIN` or pass `-PgBin`. |
| Sign-in answers 429 | Too many attempts: 5 per address or 10 failures per account name within 10 minutes (`Retry-After` says how long). Behind a reverse proxy all visitors share the proxy's address unless `trusted_proxies` lists the proxy. Wait, or restart the service (the counters are in memory). |
| Sign-in says the user accounts cannot be reached (503) | Redis does not answer. Browsing, sessions that already exist and the local administrator keep working. Check `TIMEVAULT_REDIS_URL` and run `timevault-server check-redis-users`; the **Status** page shows the directory as "degraded". Attempts made meanwhile count for the address, not against the account. |
| A company account cannot sign in | Wrong password, account not activated, no password stored, or the login matches more than one account (employee ID, name or e-mail address; the service log says so without naming anyone). Run `check-redis-users`: unreadable records, records without a password and employee IDs on more than one key are counted there. Try the employee ID, which is the least ambiguous. |
| `check-redis-users` reports unreadable records | A hash that the decoder cannot read (a field is missing or has an unexpected type). The report gives the position of the key among all keys sorted by name, so it can be found with `redis-cli`. Such a record cannot sign in. |
| There is no "Sign in with Windows (SSO)" button | `sso_host` is empty, or there is no Redis user directory (`TIMEVAULT_REDIS_URL`). The start-up log and the Site settings page show `sso_host`. |
| The single sign-in says "The sign-in could not be matched to this browser" | The address that the sign-on service recorded is not the address TimeVault sees for the browser. Behind a reverse proxy set `trusted_proxies` to the proxy (the proxy has to send `X-Forwarded-For`), or set `sso_require_same_ip` to `false`. The Audit page shows the failure with the reason `address_mismatch` (`browser_address_changed` when the address of the browser changed during the sign-in), with what TimeVault saw: `started_from` (the address it took for the browser), `sso_address`, `connection`, `trusted_proxy` and `forwarded`. If `started_from` is the address of a server, another proxy sits in front of the one next to TimeVault and hides the browser: see [Two proxies in a row](#two-proxies-in-a-row). If `trusted_proxy` is `false` and `forwarded` is set, the proxy that connects is not in `trusted_proxies`. |
| Opening `/tv/` ends on another application (for example the portal), or the pages have no style and their links leave `/tv/` | TimeVault does not know its prefix. Use version 0.1.5 or later, set the Site setting `base_path` to `/tv` and restart; the start-up log must say "serving under a path prefix". Without it every redirect and link starts at the root of the host, outside `/tv/`. |
| Behind `/tv/` the audit log shows `127.0.0.1` for everybody, or single sign-on says it could not be matched to this browser | The proxy does not pass the address of the browser on, or TimeVault does not believe the proxy. Set `proxy_set_header X-Forwarded-For $remote_addr;` in the proxy block, and the Site setting `trusted_proxies` to `127.0.0.1` (the address the proxy connects from). |
| After signing in, a page whose address has no `/tv` shows you as signed out | With a base path the cookies are limited to it. Use the addresses under `/tv/`; every link and redirect of the pages already leads there. |
| The single sign-in says the browser blocked the window | Allow pop-ups for the TimeVault site. The window can only be opened by the click itself. |
| The single sign-in waits and then says it did not finish in time | The sign-on service did not record a result. Check that `sso_host` is the service people really reach, and that it writes `sso_login_<token>` to the Redis of `TIMEVAULT_REDIS_URL` (the same database number). |
| The single sign-in says "Your Windows account has no active account here" | The employee ID that Windows gave has no active account in Redis (the Audit page shows `no_active_account` and the employee ID). Check the account with `check-redis-users`. |
| Settings says "Only an administrator can add monitored folders" | That is how it is: adding a folder is for administrators (the account's `Type` in Redis is `admin`; a change of role applies within a minute). Changing a folder's settings needs **Full control** of that folder, which an administrator can give on the Settings page. |
| A person cannot see a monitored folder that they should, or sees "not found" for it | Nobody sees a folder that nobody was given. Open the folder's **Edit** form on the Settings page (as an administrator, or with full control of the folder) and check that the person's employee ID is in a list, or that **Everyone who is signed in** (or, for visitors who have not signed in, **Guests**) is ticked. A visitor who has not signed in only has what **Guests** gives. See [Folder permissions](#folder-permissions). |
| Backing up (or saving a folder's settings) answers "You are not allowed to do this" | The account can see the folder but has too low a level: **Back up** is needed to back up, verify or stop a job, **Full control** to change the settings. The Audit page shows the attempt as **Access denied** with the folder and `needs`. Only an administrator can change the folder on the server. |
| The permissions part of a folder's form says the user directory cannot list its accounts, or refuses to add a person | Redis does not answer (or `TIMEVAULT_REDIS_URL` is not set). People who already have access are shown and can stay; nobody else can be added until the directory answers. Check the **Status** page. |
| Back up now answers that the monitored folder is switched off (409) | An administrator has to switch it on in Settings. |
| Moving a folder answers 409 or says it already has backups | A backup of that folder is running (try again when it has finished), or the folder has a recovery point in some state and can no longer be changed: switch it off and add the new folder. |
| An automatic backup does not run | The folder is switched off or has no schedule; or its last run ended less than the interval ago (the Backups and Settings pages show the next time); or the pace does not allow it yet (one automatic backup runs at a time, with a gap and a rest; see [Scheduler settings](#scheduler-settings)); or, with the change probe on (`scheduler_probe`), the NTFS index says nothing changed; or the service is not running. The scheduler looks every 15 seconds. |
| The job I was watching says "not found" | An automatic backup that finds nothing changed leaves no job behind. |
| Folders show "-" instead of a size on the Explorer | The NTFS index is off (`search_index`), not ready yet, or cannot read the volume (the **Status** page says why: no administrator rights, the volume is being built, no change journal, not NTFS), or the folder is not on a local drive letter. Files are not affected. The Time Machine and the versions page never show folder sizes. |
| Changing the limit of versions is refused with "This limit permanently deletes … older version(s)" (409 `prune_not_confirmed`) | Lowering the limit would delete versions. Tick the confirmation box and save again (API: `confirm_prune: true`). |
| A restore or a verification is refused with "old versions of this folder are being deleted" (409 `prune_running`) | A version cleanup of the folder is running; changing the folder's version limit is refused meanwhile too. Try again in a minute; the Backups page lists the job. A cleanup that is only queued, behind a backup or a verification of the folder, refuses nothing. |
| The repository does not shrink after versions were deleted | The content is only freed while no backup runs, and within a limited time per sweep; the scheduler repeats it. A server that is always backing something up delays it. |
| "Restore is not enabled" | `TIMEVAULT_RESTORE_ROOT` is empty. |
| The restore preview is rejected as stale or expired | Preview again; tokens last 15 minutes and are tied to the exact plan. After a restart without `TIMEVAULT_SESSION_KEY` the key changes too. |
| Files are listed as skipped (locked) | The file was open by another program, or changed while it was read. Run the backup again later; there is no VSS in the MVP. |
| A restore reports a missing or corrupt object | The repository lost or damaged a file. Run a deep verify, restore the repository from its backup, or choose another recovery point. Nothing is restored from a bad object. |
| An interrupted restore left `.tvpart-*` files | Temporary files in the destination folder from a crashed restore. They can be deleted. |
| Search says "unavailable" | See [Search index](#search-index). History and restore keep working. Look at the **Status** page: the service has no administrator rights, the index of the volume is still being built, the volume cannot be indexed (not NTFS, no change journal), the monitored folder is on a network share, or the Site setting `search_index` is `off`. |
| A job stays "failed: interrupted by a service restart" | The service was stopped while the job ran. Start the job again; unfinished recovery points were already cleaned up. An update never does this: it waits for the running jobs. |
| The Settings page says **Waiting for the running jobs**, and the notice under the header stays | A job is running (see the Backups page) and the update waits for it, without a limit. New jobs wait in the queue until the service has restarted. If the restart cannot wait, stop the service by hand; the job is then recovered as failed when the service starts again, and the update starts over at the first look after that start (10 seconds later). |
| **Program updates** says **Problem**, and the administrator sees "read the manifest: …" | The service cannot read the manifest at `update_url`: the file is not there yet, the share or the web address is wrong or not reachable from the server, the account of the service cannot read it, or an `http(s)` address answers something other than `200` (a `404`, a redirect to a login page, a `202`). Open the address from the server. |
| **Problem**: "the program is not the one the manifest describes" | The size or the sha256 of `timevault.exe` is not the one in `timevault.json`: the release is still being copied (it is tried again at every look and resolves itself), or the two files are from different builds, or `package_url` leads to another file. Copy both files again, the manifest last, or run `scripts\release.ps1` again. |
| **Problem**: "the program of the release says it is version X, but the manifest says Y" | `latest_version` in the manifest is not the version that is in the program (a hand-edited manifest, or a build of sources whose `var version` was not raised). It is not tried again until the manifest changes. Run `scripts\release.ps1`, which takes the version from the program. |
| **Problem**: "the new program does not accept the settings of this service: …" | `timevault.exe check-config` of the new version refuses the `.env` (a setting that the new version validates more strictly, or one that it requires). Read the reason, change the `.env`; the next look tries again. The service keeps running with the old program meanwhile. |
| **Problem**: "the new program could not be put in place: … Access is denied" | The account that runs the service cannot rename or create files in the folder of the program, or an antivirus holds the file. Give the account **Modify** on that folder (the start-up log warns about this). The service keeps running with the old program and does not try this release again until the manifest changes or the service is restarted. |
| After an update the console window or the scheduled task shows the program as ended, with the exit code 75 | `update_restart` is `exit` and nothing starts the program again. Use `supervise` (the default), or make the service manager restart the program when it ends (NSSM: `AppExit 75 Restart`). |
| After an update the old program file `timevault.old-<version>.exe` cannot be deleted | With `supervise` the launcher still runs from it (see [Releasing and automatic updates](#releasing-and-automatic-updates)). It is deleted at a later update when the launcher has ended; it can be deleted by hand after the service has been stopped and started again. |
| The service updated itself back to the newer version after a manual way back | The release folder still has the newer manifest. Put the older release there (or empty `update_url`) before you go back. |
| The pictures of the accounts are not shown | The start-up log has `avatar_url`: empty means no pictures, because `avatar_url` is `off`, or it is not set and `server_host` is not either. Open one picture in the browser (`http://<server host>/api/static/hr/<employee ID>.webp`): it must show the picture. A picture of 480×320 pixels (the placeholder, answered with `202`) and a `404` leave the default picture on purpose. If the picture opens but TimeVault does not show it: the start-up log says "cannot be named in the content security policy" (a host name with an underscore and no domain of two labels around it: open the site by that same name, or write the IP address of the server in the picture address), or the site is served over `https` and the address is `http`. Opening the site by another name than the host of the pictures is fine, because the policy lists the host of the address. |
| There are no lines in `web_log` although Redis is configured | Look in this order. The line `starting TimeVault` of the start-up log has `web_log=false` when it is off: the Site setting `web_log` is `off`, or it is `auto` and there is no `TIMEVAULT_REDIS_URL`. The start-up log has the line `web log is on` when it is on. The warning `web log: the lines cannot be written to Redis` (with the reason, the password of the URL masked) means that Redis does not answer, or that its account may not `RPUSH` to the list; the service goes on, and the writer tries again after 30 seconds, then after longer, up to 5 minutes. Check that you look at the list and the database number that the service uses (`web_log_key`, and the number in `TIMEVAULT_REDIS_URL`). Static files, `/healthz`, redirects and API calls are not page lines, and the debug lines (the polling of a job, the automatic reloads, `scheduler.skipped`) need `log_level` `debug`. See [Web log](#web-log). |
| The Site settings page refuses `web_log`, `web_log_key` or `web_log_host` (or the service stops with an error about them) | The message names the setting. `web_log` must be `on`, `off` or `auto`, and `on` needs `TIMEVAULT_REDIS_URL`. The key may only have ASCII letters, digits and `_ . : -` (at most 128 characters), and it must not start, in any case, like `TIMEVAULT_REDIS_KEY_PREFIX` or `sso_login_`; when `web_log` itself starts with the prefix (for example `web_`), choose another key, or switch the web log off. The host name may only have ASCII letters, digits and `. - _` (at most 63 characters). |
| The Site settings page refuses `base_path` and says it must not start with `/help` | `help` is a page of the application (the user guide), like `explorer` or `settings`. Choose another prefix. |

---

# 中文

## 概述

介绍如何安装、配置、运行、运维和排查 TimeVault（1.0.1 版）。它描述的是目前实际存在的内容：服务是一个 Go 程序 `timevault-server`，需要 PostgreSQL 和一个仓库文件夹；公司账号登录还需要另一个应用的 Redis 用户目录（同一个 Redis 也接收服务的 Web 日志，见 [Web 日志](#web-日志)）。为了搜索、文件夹大小和跳过未变化的文件夹，服务会自己读取监控文件夹所在的 NTFS 卷，这需要管理员权限（见[搜索索引](#搜索索引)）；除此之外没有别的东西要安装。`.env` 文件只放服务在能连上数据库之前就需要的设置，以及机密；其余所有设置都是**站点设置**，保存在数据库中，由本地管理员在**站点设置**页编辑（见[设置放在哪里](#设置放在哪里)）。目前没有安装程序，也没有 Windows 服务封装。

## 前置条件

| 项目 | 说明 |
|---|---|
| 带 NTFS 的 Windows | 在 Windows 11 Pro 上开发和测试。服务也能在其他系统上编译（部分路径规则和测试仅适用于 Windows），但只在 Windows 上实际运行过。 |
| Go 1.25 或更新版本 | 构建所需（`go.mod` 声明了 `go 1.25.0`）；恢复代码使用了 `os.Root`。可以使用便携版 Go：把 `TIMEVAULT_GO_ROOT=<Go 文件夹>`（包含 `bin\go.exe` 的那个文件夹）这一行写入 `.env`，开发脚本就会在每个终端里把它加入 `PATH`，而不必修改系统 `PATH`。 |
| PostgreSQL | 只测试过 16.4。所用 SQL 至少需要 PostgreSQL 11（identity 列、`hashtextextended`），但没有试过低于 16 的版本。 |
| Redis（可选） | 另一个应用（dash）的用户目录：名为 `app_user_<ee_code>_<name>_<email>` 的哈希。没有它时只有本地管理员可以登录。测试使用的是进程内的模拟服务和 Python 生成的 pickle 数据，没有连接过公司自己的 Redis：请在它上面运行一次 `timevault-server check-redis-users`。服务还会把它的 Web 日志追加到该 Redis 中的一个列表里（设置了 `TIMEVAULT_REDIS_URL` 时默认开启；见 [Web 日志](#web-日志)）：`TIMEVAULT_REDIS_URL` 中的账号必须被允许对它执行 `RPUSH`，这是唯一的写入，同样只用进程内的模拟服务测试过。 |
| 管理员权限（用于搜索索引） | 服务直接读取监控文件夹所在的 NTFS 卷（见[搜索索引](#搜索索引)），而 Windows 只允许以管理员身份（或 SYSTEM 账号）运行的进程这样做：请在已提升权限的 shell 中启动服务，或把它作为以最高权限运行的计划任务来启动。没有这个权限时，搜索显示"不可用"，文件夹大小显示"-"，调度器照旧扫描，状态页会说明原因；其他功能正常。只有位于本地 NTFS 盘符（`D:\...`）上的监控文件夹才会被建立索引。 |

## 开发环境快速开始

脚本把数据放在被 git 忽略的 `data\` 文件夹下，**所有设置放在同一个被 git 忽略的文件里：仓库文件夹中的 `.env`**（见[设置放在哪里](#设置放在哪里)）。脚本不会触碰已有的 PostgreSQL 服务，也不会打印任何机密信息。如果 PowerShell 拦截脚本，请用 `powershell -NoProfile -ExecutionPolicy Bypass -File scripts\<name>.ps1` 运行。如果 Go 不在 `PATH` 中，请先把 `TIMEVAULT_GO_ROOT=<包含 bin\go.exe 的文件夹>` 这一行写入 `.env`；否则 `dev-run.ps1` 会立即以"The go command was not found"退出。早期版本还把设置放在另外三个文件里（`data\local.env`、`data\pgdev\db.env`、`data\dev\server.env`）；脚本第一次运行时会把它们的设置移入 `.env` 并删除这些文件。

1. `scripts\dev-db.ps1 start`（首次）创建并启动一个私有 PostgreSQL 集群，位于 `data\pgdev`，监听 `127.0.0.1:55432`，包含数据库 `timevault` 和 `timevault_test`。随机密码写入 `.env`（`TIMEVAULT_DATABASE_URL` 和 `TIMEVAULT_TEST_DATABASE_URL`）。它需要已安装的 PostgreSQL 二进制文件（用 `.env` 中的 `TIMEVAULT_PG_BIN` 或 `-PgBin` 指定位置）。其他动作：`stop`、`status`、`init`、`reset`（会删除该集群）。**也可以跳过这一步，改用你已有的 PostgreSQL 服务器**：自己创建好数据库和角色后，把 `TIMEVAULT_DATABASE_URL`（以及测试用的 `TIMEVAULT_TEST_DATABASE_URL`）写入 `.env`。这样脚本不会启动私有集群，并且只要 `.env` 指向其他服务器，`dev-db.ps1` 就会拒绝创建或重置私有集群。
2. `scripts\dev-run.ps1` 在 `.env` 不存在时创建它（带注释，已被 git 忽略）；在 `TIMEVAULT_DATABASE_URL` 指向私有集群（端口 55432）时启动它，否则认为你配置的服务器已在运行；构建 `data\dev\bin\timevault-server.exe`；并把服务需要而 `.env` 里还没有的内容加入 `.env`：`data\dev` 下的仓库和恢复文件夹、`TIMEVAULT_AUTO_MIGRATE=true`、会话密钥，以及在第一次运行时为本地管理员 `admin` 生成的随机密码（密码放在 `data\dev\admin-password.txt`，只有它的哈希进入 `.env`）。**在启动服务之前，它会移除终端里遗留的 `TIMEVAULT_*` 变量**，因为它们会覆盖 `.env`，并且会把它们的名字列出来。然后在前台运行服务，地址为 `http://127.0.0.1:8660/`（用 `-Port` 修改这一次运行的端口；不带该参数时使用 `.env` 中的 `TIMEVAULT_LISTEN_ADDR`），并打印是否已配置 Redis 用户目录。如果想要搜索和文件夹大小，请从**已提升权限**的 PowerShell 启动该脚本：NTFS 索引要读取卷，这需要管理员权限（见[搜索索引](#搜索索引)）；从普通 shell 启动时其他功能都正常，状态页会说明索引需要管理员权限。按 Ctrl+C 停止。
3. `scripts\dev-demo.ps1`（服务运行时）把 `data\dev\source` 登记为监控文件夹"Demo files"，并通过 API 创建三个恢复点，同时留下一些未备份的改动，使文件浏览页能显示已变更、新增和已删除的项目。它从 `.env` 中的 `TIMEVAULT_LISTEN_ADDR` 取得地址；用 `dev-run.ps1 -Port` 启动之后，请传入 `-BaseUrl http://127.0.0.1:<port>`。
4. 打开该地址。任何人都可以浏览。要备份、恢复或修改设置，请用 `admin` 和 `data\dev\admin-password.txt` 中的密码登录；或者在 `.env` 中写入 `TIMEVAULT_REDIS_URL` 后使用公司账号登录（在仓库文件夹中运行 `data\dev\bin\timevault-server.exe check-redis-users` 检查）。这也会开启 Web 日志（见 [Web 日志](#web-日志)）：在使用公司 Redis 的开发电脑上，请同时把站点设置 `web_log` 设为 `off`（**设置 → 站点设置**）。页眉有语言切换（English / 中文）和浅色/深色主题切换按钮（在使用该按钮之前，页面跟随系统设置；之后浏览器会记住你的选择）。

5. 可选，用于搜索框、文件夹大小和调度器的变更探测：从已提升权限的 PowerShell 运行 `scripts\dev-run.ps1`。监控文件夹在添加之后不久就会被建立索引（见[搜索索引](#搜索索引)）；没有其他东西要安装或打开。
6. 可选，用于其他设置（人们使用的地址、登录选项、工作线程、调度器、更新、日志）：以本地管理员 `admin` 登录，打开**设置 → 站点设置**（见[设置放在哪里](#设置放在哪里)）。

`. .\scripts\dev-env.ps1` 为你自己的 PowerShell 会话做好运行 `go test` 和手动运行 `timevault-server` 的准备。它会移除会话里遗留的 `TIMEVAULT_*` 设置（它们会覆盖 `.env`），把 `TIMEVAULT_ENV_FILE` 设为仓库的 `.env`，加载只有测试和工具才读取的内容（`TIMEVAULT_TEST_*`、`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN`），并把 Go 加入该会话的 `PATH`。服务自己的设置不会被复制到环境变量中：服务直接读取 `.env`。

## 命令

| 命令 | 用途 |
|---|---|
| `timevault-server serve` | 运行服务（不带命令时的默认行为）。 |
| `timevault-server migrate` | 应用数据库迁移后退出。 |
| `timevault-server hash-password` | 读取密码（在终端中输入时不回显，或从标准输入读取一行），并输出可用于 `TIMEVAULT_ADMIN_PASSWORD_HASH`（可选的本地管理员）的值。 |
| `timevault-server check-redis-users` | 连接 Redis 用户目录（`TIMEVAULT_REDIS_URL`），并输出其中有多少可以使用：键的数量、可读和不可读的记录、已启用的账号、类型为 `admin` 和 `user` 的数量、没有密码的账号、出现在多个键上的工号，以及每个字段有多少值可以解码。它不输出任何姓名、邮箱、键或密码。Redis 没有应答或没有任何可读记录时退出码为 1。请在包含 `.env` 的文件夹中运行，或设置 `TIMEVAULT_ENV_FILE`。 |
| `timevault-server version` | 输出版本号。 |
| `timevault-server check-config` | 检查环境变量和 `.env` 文件中的设置对服务是否有效，输出 `the settings are valid`，或输出无效的原因（退出码为 1）。它不连接任何东西，所以不检查站点设置：站点设置在数据库中，保存时就会检查。会自己更新的服务在替换自己之前，会用新程序运行它（见[发布与自动更新](#发布与自动更新)）。 |
| `timevault-server settings list`、`get <key>`、`set <key> <value>`、`reset <key>` | 在命令行上操作站点设置，针对 `TIMEVAULT_DATABASE_URL` 所指的数据库（请在包含 `.env` 的文件夹中运行，或设置 `TIMEVAULT_ENV_FILE`）。`list` 显示每个站点设置当前生效的值，已保存的值前面标 `*`；`set` 像页面一样先检查再保存一个值（与默认值相同的值不会被保存）；`reset` 忘掉已保存的值，使默认值生效。当站点设置页无法访问时（例如某个设置把所有人都锁在了外面），这是回头的路。修改之后必须重启服务才会使用。见[设置放在哪里](#设置放在哪里)。 |

## 构建可执行文件

除了 `.env`，服务只需要 `timevault-server.exe` 这一个文件：Web 模板、静态资源和数据库迁移脚本都已嵌入其中。程序是包 `./cmd/timevault-server`；仓库根目录不是程序（`go build .` 会报 "no Go files"）。

```powershell
cd <repository folder>
go build -trimpath -ldflags "-s -w" -o timevault-server.exe ./cmd/timevault-server
.\timevault-server.exe version
```

只需 `go build -o timevault-server.exe ./cmd/timevault-server` 就够了。`-trimpath -ldflags "-s -w"` 只是让文件更小（约 23 MB，而不是 32 MB）。需要 Go 1.25 或更新版本（见 `go.mod`）。

**版本号**只写在一个地方，即 `cmd/timevault-server/main.go` 中的 `var version`，构建时直接从那里取，所以上面的命令得到的总是它所在源码的版本。`version` 命令、启动日志、每个页面的页脚、设置页和 `GET /api/v1/settings` 都会显示它。每次构建之后请运行 `.\timevault-server.exe version`。不要在命令里加 `-X main.version=<number>`：它会替换 `main.go` 中的版本号，所以为旧版本写的命令副本会一直打上旧的版本号。用 0.1.5 的源码加上 `-X main.version=0.1.4` 构建出来的程序，具备 0.1.5 的全部功能，却自称 0.1.4。

**依赖和 `vendor\` 文件夹。** 构建需要 `go.mod` 中列出的 Go 模块，它们来自模块缓存（`go env GOMODCACHE`，由 Go 从模块代理下载填充），或者来自 `go.mod` 旁边的 `vendor\` 文件夹。存在 `vendor\` 文件夹时，Go 会自动使用它（等同于 `-mod=vendor`；`go build`、`go vet` 和 `go test` 都是如此）。这适合没有外网的服务器：在有这些模块的计算机上运行一次 `go mod vendor`（会写出约 600 个文件、7 MB），然后把 `vendor\` 文件夹和源码一起复制过去。

- `vendor\` 不属于仓库，所以克隆仓库得不到它。请把它和源码一起复制，或者在有它的计算机上构建。
- 没有 `vendor\` 文件夹时使用 `-mod=vendor` 会失败，提示 `go: inconsistent vendoring ... is explicitly required in go.mod, but not marked as explicit in vendor/modules.txt`。`vendor\` 比 `go.mod` 或 `go.sum` 旧时也会出现同样的提示。请重新运行 `go mod vendor`（每次更改依赖之后都要运行），或者用 `-mod=readonly` 从模块缓存构建，它会忽略 `vendor\`。
- 部署到没有外网的服务器，最简单的做法是在开发电脑上构建 `timevault-server.exe`，然后把这个文件连同 `.env` 复制到服务器上。
- 会自己更新的服务器只需要复制这一次：`scripts\release.ps1` 会生成这类服务器读取的发布文件夹（见[发布与自动更新](#发布与自动更新)）。

## 设置放在哪里

| 内容 | 位置 | 由谁修改 | 何时生效 |
|---|---|---|---|
| 引导设置和机密：数据库 URL、监听地址、备份文件夹（`TIMEVAULT_REPOSITORY_PATH`）、恢复文件夹（`TIMEVAULT_RESTORE_ROOT`）、用户目录的 Redis URL、本地管理员、会话密钥（完整列表见[配置参考](#配置参考)） | `.env` 文件（进程环境变量会覆盖它） | 安装服务的人，通过编辑文件 | 重启之后 |
| **站点设置**：本服务器的主机名和恢复文件夹的显示路径、账号头像、反向代理之后的路径、受信任的代理、登录选项、worker 和进程优先级、调度器、搜索索引、自动更新、日志和 Web 日志（分 7 组共 26 个设置，列在[站点设置](#站点设置)中） | 数据库（表 `site_settings`） | **只有本地管理员**，在**站点设置**页、通过 `PATCH /api/v1/site-settings`，或用 `timevault-server settings` | 重启之后（页面上有**立即重启**按钮） |
| 监控文件夹：名称、显示文件夹、服务器上的文件夹、排除规则、启用或停用、自动备份计划、每个文件保留的版本数 | 数据库（表 `sources`） | 管理员添加文件夹，并可以修改它们的一切；对某个文件夹拥有"完全"权限的人，在**设置**页或通过 `PATCH /api/v1/sources/{id}` 修改它的设置，但不能修改服务器上的文件夹 | 立即 |
| 每个文件夹上谁能做什么（浏览、备份、完全；可以授予访客、所有已登录的人或单个工号） | 数据库（表 `source_access`） | 管理员，以及对该文件夹拥有"完全"权限的人，在**设置**页该文件夹表单的权限部分，或通过 API 的 `access`（见[文件夹权限](#文件夹权限)） | 立即，对每个访问者的下一次请求生效 |
| 账号和角色 | Redis（`app_user_*`），由另一个应用写入 | 那个应用 | 对已经登录的会话，在一分钟之内 |

- `.env` 文件是 `TIMEVAULT_ENV_FILE` 所指的文件（它必须存在），否则是工作目录下的 `.env`，否则是 `timevault-server.exe` 旁边的 `.env`。使用找到的第一个；不会合并多个文件。开发时它就是仓库文件夹中的 `.env`，也是唯一的设置文件：开发脚本读写它，`dev-run.ps1` 让服务使用它。
- **进程环境变量中的 `TIMEVAULT_*` 变量优先于文件中的同名键**，而且除非你去查，否则没有任何提示：终端里遗留的某个变量会让服务无视你在 `.env` 里写的内容（典型表现是端口改不动）。因此服务在启动时会用一条警告点名所有被环境变量覆盖的设置（`check-redis-users` 也会打印它们），开发脚本则会从终端里移除这类变量。如果这条警告反复出现，说明该变量设在你的 Windows 用户或系统环境变量里。这只涉及引导设置：仍写在环境变量或 `.env` 里的站点设置（例如 `TIMEVAULT_JOB_WORKERS`，或已删除的 `TIMEVAULT_EVERYTHING_*` 设置）无论值是什么都会被**忽略**，服务启动时会在警告里点名它们，这样旧的一行就永远不会成为"在页面上保存的值没有生效"的原因。
- 语法：每行一个 `KEY=VALUE`。空行和以 `#` 开头的行会被忽略，允许行首的 `export `，接受 UTF-8 字节顺序标记和 Windows 行尾。值按字面一直取到行尾，所以带 `\` 的路径或带 `#` 的密码无需转义（行尾的 ` # 注释` 会成为值的一部分）；值两侧成对的单引号或双引号会被去掉；同一个键以最后一次赋值为准。出错时只指出行号，绝不显示该行内容。
- 该文件包含机密信息（数据库和 Redis 密码、会话密钥）。`/.env` 已在 `.gitignore` 中；请让它只能被运行服务的账户读取，并且绝不要提交填好的副本。[configs/timevault.example.env](../configs/timevault.example.env) 是模板。
- **设置**页（任何已登录账号都可以打开）显示访问者可以浏览的监控文件夹（每个都标明访问者对它能做什么）、备份存储和恢复文件夹（含仓库的剩余空间），一张带版本号的**服务**卡片（本地管理员还会在那里看到**站点设置**按钮），以及自动更新的状态（见[发布与自动更新](#发布与自动更新)：发布地址和最近的问题只有管理员能看到，管理员还可以点击**立即检查**）。机密信息绝不显示。这个页面以前显示的当前运行配置（本服务器的主机名、监听地址、显示为 `host:port/name` 的数据库和 Redis、本地管理员的名称、设置文件和搜索索引的状态）现在在**站点设置**页上，只有本地管理员能看到。添加监控文件夹的表单只给管理员；修改文件夹的表单给管理员，以及对该文件夹拥有"完全"权限的人（其中没有服务器磁盘上的文件夹，那一项只有管理员能看到和修改）。只有管理员能看到服务器磁盘上的文件夹（每个监控文件夹的真实文件夹、备份文件夹和真实的恢复文件夹）；其他人看到的是显示文件夹。
- **备份文件夹和恢复文件夹。** 它们是两个不同的地方。备份文件夹（`TIMEVAULT_REPOSITORY_PATH`，即仓库）是 TimeVault 保存所有被备份内容的地方。恢复文件夹（`TIMEVAULT_RESTORE_ROOT`）是放恢复出来的文件的地方：恢复不会覆盖原文件，也绝不会写入监控文件夹，所以它把恢复出来的副本写入恢复文件夹下的一个新文件夹（名称在恢复表单中填写，默认是 `restore-<日期>-<时间>`），需要哪些再从那里复制回去。两者都不能位于监控文件夹之内。
- **显示文件夹。** 人们通过网络访问监控文件夹和恢复文件夹，而不是 `D:\TimeVault\monitor` 这样的路径。因此每个监控文件夹都有一个**显示文件夹**，即人们打开它时使用的网络路径（`D:\TimeVault\monitor` 共享为 `TimeVault` 时是 `\\sgmeslt595.co.murata.local\TimeVault\monitor`），恢复文件夹则有站点设置 `restore_display_root`（`\\sgmeslt595.co.murata.local\TimeVault\restore`）。把站点设置 `server_host` 设为本服务器的名称（`sgmeslt595.co.murata.local`）：设置页和 API 就会拒绝没有显示文件夹的监控文件夹，也会拒绝显示文件夹位于另一台服务器上的监控文件夹（它的搜索、备份和恢复只能处理本服务器自己的文件夹）。没有设置 `server_host` 时，显示文件夹是可选的，也不会与主机名比较。管理员在设置页填写该字段，设置页还会标出没有显示文件夹的监控文件夹（"还没有显示文件夹"；设置主机名之后，之前添加的每个文件夹都要先填上一个，才能再次编辑）。服务检查的是主机，而不是共享是否真的通向该监控文件夹：请先共享文件夹，再写下你打开它所用的路径。显示文件夹出现的地方，文件浏览、时间机器和文件版本页面都有一行带**复制**按钮的**位置**。
- **谁能看到哪个文件夹。** 管理员同时看到显示文件夹和真实文件夹。其他人无论是否登录，都只看到显示文件夹；引用真实文件夹的文本（任务错误和日志、恢复消息、审计详情、状态详情、文件未能备份的原因）中，会显示显示文件夹、`[<监控文件夹名称>]`、`[backup folder]` 或 `[restore folder]`。见 [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md)。

## 站点设置

服务在能连上数据库之前并不需要的一切设置都是**站点设置**：地址与显示选项、登录选项、工作线程和优先级、调度器、搜索索引、自动更新和日志。站点设置保存在数据库中（表 `site_settings`，每个有值的设置一行），并且只有**本地管理员**可以查看和修改：即 `TIMEVAULT_ADMIN_USER` 所指、用 `TIMEVAULT_ADMIN_PASSWORD_HASH` 登录的那个账号（开发时如果关闭了登录，每个人都是本地管理员）。公司账号看不到也改不了它们，即使是类型为 `admin` 的账号。原因见 [ADR 0017](./adr/0017-site-settings-in-the-database.md)。

- **页面。** **设置 → 站点设置**（`/settings/site`）把 26 个设置分成七组显示，每个设置都有当前生效的值、默认值和简短说明；上方显示环境变量和 `.env` 里的内容（版本、监听地址、以 `host:port/name` 表示的数据库和 Redis、备份和恢复文件夹、本地管理员、设置文件和搜索索引的状态；绝不显示机密）。其他已登录的人请求该页面会被告知没有这个页面（`404`，API 中是 `not_found`），并且这次尝试会作为"访问被拒绝"、带 `reason: site`，写入审计日志；菜单中也不会提供这个页面。没有登录的访问者会被要求先登录，和其他需要账号的页面一样。
- **只有与默认值不同的值才会被保存。** 与默认值相同的设置不会被保存，这样以后版本改变的默认值，会惠及所有从未选择过值的人。字段为空表示"使用默认值"。
- **保存之前先检查。** 各个值会连同它们所依赖的引导设置一起检查（Web 日志需要 `TIMEVAULT_REDIS_URL` 所指的 Redis，更新地址需要 `TIMEVAULT_AUTO_MIGRATE=true`，等等）。有问题的表单会按输入的样子重新显示，并指出问题所在，什么都不会保存。提示信息绝不重复任何值。
- **重启之后才生效。** 保存不会改变正在运行的服务。只要已保存的内容与服务启动时所用的不同，页面就会显示"有些已保存的设置还没有生效"并提供**立即重启**。服务随后会干净地停止，并像自动更新之后那样重新启动（`update_restart`：`supervise` 让进程作为小型启动器保留并再次启动它，`exit` 让它以退出码 75 结束，由服务管理器启动）。有任务在运行时它会拒绝（"有任务正在运行。请等它们结束后再重启。"；API 中是 `409 jobs_running`）：它会暂时停止接收新任务，在不重启时再恢复接收。手动重启的效果相同。
- **审计。** 修改会作为**修改站点设置**（`site.update`）、重启会作为**为站点设置而重启**（`site.restart`）写入审计日志，带有操作者和地址。详情会点出发生变化（`changed`）或恢复为默认值（`reset`）的设置，**绝不包含它们的值**，因为值可能是地址或路径。只有本地管理员能在**审计**页和 `GET /api/v1/audit` 中看到这些事件；其他人的列表会略去它们，也不会把它们作为筛选项提供。
- **API 和命令行。** `GET /api/v1/site-settings`、`PATCH /api/v1/site-settings` 和 `POST /api/v1/site-settings/restart` 为脚本提供同样的功能（见 [api.md](./api.md)）。`timevault-server settings list|get|set|reset`（见[命令](#命令)）直接操作数据库。当页面无法访问时（例如某个设置把所有人都锁在了外面），这是回头的路：运行 `timevault-server settings reset <key>`，然后重启服务。`.env` 中没有本地管理员时，它是修改站点设置的唯一方式，服务启动时会这样提示。
- **启动。** 服务读取 `.env`，用一个小连接池连接数据库，应用迁移（`TIMEVAULT_AUTO_MIGRATE=true`）或检查迁移已应用，读取站点设置，把所有内容放在一起校验，然后才打开 `job_workers` 所要求大小的连接池。如果已保存的设置放在一起无效，服务不会启动并会这样说（`the Site settings in the database are not valid; correct them with "timevault-server settings"`），同时点出问题所在。
- **从 1.0.0 升级之后**，该表是空的，每个设置都取默认值，无论 `.env` 里原来写了什么：请在页面上（或用 `settings set`）重新填写这些值，然后重启。升级之前请把这些值记下来，因为新程序一启动就会忽略它们。其中有两个马上就有影响：会自己更新的服务器在重新填写 `update_url` 之前就不再自动更新（带来 1.0.1 的这次更新，就是它在那之前自己安装的最后一次更新），而放在带路径前缀的代理之后的站点，在重新填写 `base_path` 之前链接是坏的；请用服务器自己的地址（`http://127.0.0.1:8660/settings/site`，不需要前缀）打开站点设置页，或运行 `timevault-server settings set base_path /tv`。仍写在 `.env` 里的站点设置会被忽略，服务启动时会在警告里点名它们；请删除这些行。已删除的 `TIMEVAULT_EVERYTHING_*` 设置和 `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` 也会在那里被点名。数据库 URL、各个文件夹、Redis、本地管理员和会话密钥仍留在 `.env` 中。

| 分组 | 键 | 默认值 | 含义 |
|---|---|---|---|
| 地址与显示 | `server_host` | 空 | 人们在网络路径中输入的本服务器名称，例如 `sgmeslt595.co.murata.local`。只能包含字母、数字、`-`、`_` 和 `.`。设置后，每个监控文件夹都需要一个位于该主机上的显示文件夹（比较时不区分大小写），并且 `restore_display_root` 也必须位于该主机上。为空时，显示文件夹是可选的，也不会与主机比较。 |
| 地址与显示 | `restore_display_root` | 空 | 人们通过网络访问恢复文件夹的路径，`\\server\share\folder`（两种斜杠都可以）。必须是网络路径，设置了 `server_host` 时还必须位于该主机上；没有恢复文件夹时会被忽略。没有它时，普通账号只会被告知恢复文件夹在服务器上。 |
| 地址与显示 | `avatar_url` | `http://<server_host>/api/static/hr/{id}.webp` | 浏览器读取账号头像的地址，其中 `{id}` 恰好出现一次，用来放工号：可以是 `http(s)` 地址（不能包含用户名或密码），也可以是以 `/` 开头的路径，表示站点被打开时所用的那个主机上的路径。未设置时：使用 `server_host` 所指服务器上其他公司应用的头像；该设置也没有设置时，没有头像。`off` 或 `none` 表示关闭头像。见[账号头像](#账号头像)。 |
| 地址与显示 | `base_path` | 空 | 站点放在反向代理之后时所处的路径前缀，例如 `http://host/tv/` 对应 `/tv`。为空表示站点位于其地址的根路径。`tv`、`/tv` 和 `/tv/` 都表示 `/tv`；只能包含字母、数字和 `- _ . ~`，并且不能以应用自己的路径开头（`api`、`static`、`login`、`explorer`、`history`、`versions`、`backups`、`jobs`、`restore`、`audit`、`status`、`settings`、`lang`、`logout`、`auth`、`healthz`、`help`）。页面、重定向和 Cookie 给出的每个地址都带有它；带或不带它的请求都会被接受。见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)。 |
| 地址与显示 | `trusted_proxies` | 空 | 反向代理，写成用逗号分隔的地址或 CIDR 范围（`127.0.0.1,::1,10.0.0.0/24`），它们的 `X-Forwarded-For` 头会被信任。此后客户端地址取自该头；它用于单点登录检查、审计日志的**来源**列、登录限流、控制台日志的 `ip=` 和 Web 日志中的地址。代理必须自己设置这个头；见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)；它前面还有另一层代理时，见[两层代理](#两层代理)。 |
| 登录 | `session_ttl` | `8h` | 会话有效期，范围 `5m` 到 `720h`。 |
| 登录 | `cookie_secure` | `false` | 给 Cookie 加上 `Secure`。在 HTTPS 之后请设为 `true`。 |
| 登录 | `sso_host` | 空 | 公司单点登录服务，写成 `host`、`host:port` 或 `http(s)://host[:port]`（协议默认为 `http`）。设置后，登录页会提供"使用 Windows 账号登录（SSO）"；见[单点登录](#单点登录)。需要 `TIMEVAULT_REDIS_URL`。 |
| 登录 | `sso_require_same_ip` | `true` | 单点登录服务看到的地址必须与请求登录的浏览器的地址一致。只有在无法得知浏览器的地址时才关闭它（更好的做法是 `trusted_proxies`）。 |
| 性能 | `job_workers` | `2` | 同时运行的任务数（1 到 16）。数据库连接池的大小与之匹配（2 × worker 数 + 8，至少 16 个连接）；PostgreSQL 的 `max_connections` 必须允许这个数量。 |
| 性能 | `hash_workers` | 4 与 CPU 数中较小者 | 一次备份内并行哈希和复制的文件数（1 到 32）。 |
| 性能 | `process_priority` | `below_normal` | 服务在这台电脑上各程序之间的优先级：`normal`、`below_normal` 或 `background`（同时降低磁盘优先级）。仅限 Windows。 |
| 调度器 | `scheduler_parallel` | `1` | 允许同时排队或运行的自动备份数量（1 到 4）。手动启动的备份不受限制。见[调度器设置](#调度器设置)。 |
| 调度器 | `scheduler_gap` | `auto` | 两个自动备份启动之间的最短时间：`auto`，或 `0s` 到 `6h` 的时长（`0s` 关闭间隔）。 |
| 调度器 | `scheduler_rest` | `auto` | 一个自动备份结束之后到下一个启动之前的停顿，仅在 `scheduler_parallel` 为 1 时生效：`auto`，或 `0s` 到 `6h` 的时长（`0s` 关闭）。 |
| 调度器 | `scheduler_probe` | `false` | 扫描文件夹之前先问搜索索引它是否有变化，没有变化就跳过扫描。只有在 NTFS 索引运行时才有效（见[搜索索引](#搜索索引)）；没有它时，每个到期的文件夹都照旧扫描。 |
| 调度器 | `scheduler_full_scan_every` | `24h` | 开启探测后，一个文件夹最长可以多久不做完整扫描，范围 `1h` 到 `720h`。 |
| 搜索索引 | `search_index` | `auto` | `auto`：只要服务被允许读取各个卷，监控文件夹的 NTFS 索引就会运行（见[搜索索引](#搜索索引)）。`off`：它从不运行，所以搜索显示"不可用"，文件夹大小显示"-"，调度器探测也没有可问的对象。 |
| 自动更新 | `update_url` | 空 | 发布清单 `timevault.json` 所在的位置：`http(s)` 地址，或文件、其所在文件夹、共享的绝对路径（`D:\TimeVault\release`、`\\server\share\tv-release`）；以斜杠结尾的地址和文件夹路径都表示该文件夹。不能包含用户名、密码、查询串或片段。为空表示服务从不更新自己。需要 `TIMEVAULT_AUTO_MIGRATE=true`。见[发布与自动更新](#发布与自动更新)。 |
| 自动更新 | `update_interval` | `10m` | 读取清单的频率，范围 `1m` 到 `24h`。第一次查看在启动后约 10 秒；管理员可以立即要求查看一次（**立即检查**）。 |
| 自动更新 | `update_restart` | `supervise` | 更新之后、以及为站点设置而重启之后，怎样启动新程序：`supervise`（被启动的那个进程作为小型启动器保留，由它启动新程序）或 `exit`（进程以退出码 75 结束，由服务管理器启动）。 |
| 日志 | `log_level` | `info` | `debug`、`info`、`warn` 或 `error`。`debug` 还会写出 Web 日志的调试行；其他级别对 Web 日志没有任何影响（见 [Web 日志](#web-日志)）。 |
| 日志 | `log_format` | `text` | `text` 或 `json`。 |
| 日志 | `web_log` | `auto` | 服务是否把行追加到公司各应用共用的 Web 日志，即 `TIMEVAULT_REDIS_URL` 所指 Redis 中的一个列表。`auto`：恰好在设置了 `TIMEVAULT_REDIS_URL` 时开启。`on`（也可写 `true`、`1`、`yes`）：需要 `TIMEVAULT_REDIS_URL`，否则该设置会被拒绝。`off`（也可写 `false`、`0`、`no`）：从不写入。见 [Web 日志](#web-日志)。 |
| 日志 | `web_log_key` | `web_log` | Web 日志所用的 Redis 列表。只能包含 ASCII 字母、数字和 `_ . : -`，最多 128 个字符；无论大小写，都不能以 `TIMEVAULT_REDIS_KEY_PREFIX` 或 `sso_login_`（账号和单点登录结果的键）开头，否则该设置会被拒绝。 |
| 日志 | `web_log_host` | 小写的计算机名 | Web 日志每一行的最后一个字段。只能包含 ASCII 字母、数字和 `. - _`，最多 63 个字符；设置的值按原样使用。 |

## 配置参考

`.env` 文件只放下面这些设置；每个设置都是一个环境变量，或 `.env` 文件中的一行（见[设置放在哪里](#设置放在哪里)；环境变量优先）。无效的值会使程序退出并给出点名该变量的提示。机密信息绝不会写入日志。其余一切都是[站点设置](#站点设置)。

| 变量 | 默认值 | 含义 |
|---|---|---|
| `TIMEVAULT_ENV_FILE` | 无 | 要读取的设置文件路径，用来代替查找 `.env`。该文件必须存在。只能通过进程环境变量设置。 |
| `TIMEVAULT_LISTEN_ADDR` | `127.0.0.1:8660` | 监听地址。除非设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，否则会拒绝非回环地址。 |
| `TIMEVAULT_ALLOW_NON_LOOPBACK` | `false` | 确认由 TLS 反向代理保护非回环监听。服务在监听本机之外时，启动时会记录一条警告。 |
| `TIMEVAULT_DATABASE_URL` | 无（必填） | PostgreSQL 连接 URL。机密。 |
| `TIMEVAULT_AUTO_MIGRATE` | `false` | 启动时应用迁移。为 `false` 时，如果数据库结构不是最新，服务会拒绝启动；请先运行 `migrate`。 |
| `TIMEVAULT_REPOSITORY_PATH` | 无（必填） | 备份文件夹（仓库）：保存被备份文件内容的地方。新文件夹或空文件夹会被初始化；不是 TimeVault 仓库的非空文件夹会被拒绝。修改后需要重启。 |
| `TIMEVAULT_RESTORE_ROOT` | 空 | 恢复文件夹。恢复不会覆盖原文件，也绝不会写入监控文件夹：每次恢复都会在此文件夹下新建一个文件夹（名称在恢复表单中填写），并把恢复出来的副本写到那里。为空表示禁用恢复。不得位于仓库或监控文件夹之内，也不得包含它们。 |
| `TIMEVAULT_AUTH_DISABLED` | `false` | 关闭登录：每个人都是本地管理员。只在回环监听时被接受。仅用于开发。 |
| `TIMEVAULT_REDIS_URL` | 空 | Redis 用户目录，`redis://[:password@]host:port/db`（TLS 用 `rediss://`）。为空表示没有公司账号。Web 日志写入同一个 Redis（见 [Web 日志](#web-日志)）。机密。 |
| `TIMEVAULT_REDIS_KEY_PREFIX` | `app_user_` | 用户哈希的键前缀。 |
| `TIMEVAULT_REDIS_TIMEOUT` | `3s` | Redis 的连接、读取和写入超时，范围 `100ms` 到 `30s`。Web 日志的写入也使用它。 |
| `TIMEVAULT_ADMIN_USER` | `admin` | 可选的本地管理员的名称，也是唯一可以打开站点设置页的账号。 |
| `TIMEVAULT_ADMIN_PASSWORD_HASH` | 无（可选） | `timevault-server hash-password` 的输出。创建一个即使 Redis 不可用也能登录的本地管理员；它的名字永远不会到 Redis 中查找。没有它时，服务需要 `TIMEVAULT_REDIS_URL`（除非关闭了登录），并且站点设置只能用 `timevault-server settings` 修改。机密。 |
| `TIMEVAULT_SESSION_KEY` | 每次启动随机生成 | 解码后至少 32 字节的 Base64 文本。设置它可使会话和恢复确认在重启后仍然有效。机密。 |

仅供辅助脚本和测试使用、同样放在这个 `.env` 里的变量（服务会忽略它们）：`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN` 和 `TIMEVAULT_TEST_DATABASE_URL`（见[测试](#测试)）。[configs/timevault.example.env](../configs/timevault.example.env) 是带占位符的模板。

## 不使用开发脚本时的运行方式

请先构建 `timevault-server.exe`（见[构建可执行文件](#构建可执行文件)）。

1. 为 TimeVault 创建 PostgreSQL 角色和数据库（请使用你自己的密码）：

   ```sql
   CREATE ROLE timevault LOGIN PASSWORD '<choose-a-password>';
   CREATE DATABASE timevault OWNER timevault;
   ```

2. 把 [configs/timevault.example.env](../configs/timevault.example.env) 复制为 `timevault-server.exe` 旁边的 `.env`，替换每个 `<placeholder>`，并删除不需要的行。登录需要 `TIMEVAULT_REDIS_URL`（公司账号；用 `timevault-server check-redis-users` 检查）和/或一个本地管理员：`timevault-server hash-password` 会生成 `TIMEVAULT_ADMIN_PASSWORD_HASH` 的值。
3. 运行 `timevault-server migrate`（或设置 `TIMEVAULT_AUTO_MIGRATE=true`）和 `timevault-server serve`；如果想要搜索，请从已提升权限的 shell 启动（见[搜索索引](#搜索索引)）。打开 `http://127.0.0.1:8660/`；由管理员在**设置**页添加监控文件夹，由本地管理员在**设置 → 站点设置**中设置其余一切（见[站点设置](#站点设置)；已保存的设置在重启之后使用，页面上有**立即重启**按钮）。
4. 如需让其他计算机使用，请在前面放置 HTTPS 反向代理，让服务监听回环地址，并把站点设置 `cookie_secure` 设为 `true`。直接监听非回环地址需要 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`，并且依然没有 TLS。能访问该地址的任何人都可以不用账号浏览每个监控文件夹的文件名、大小和历史，公司密码也是在这个网站上输入的，所以 HTTPS 不是可选项。登录限流统计的是连接过来的地址，所以放在反向代理之后时统计的是代理的地址，除非站点设置 `trusted_proxies` 列出了该代理（见[单点登录](#单点登录)和 [security.md](./security.md)）。当代理把 TimeVault 放在多个应用共用的主机名的某个路径下（例如 `/tv/`）时，见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)。
5. 服务没有 Windows 服务封装。请用你信任的方式启动它（例如计划任务或服务管理器）。0.1.8 用普通用户账号（没有管理员权限）的计划任务试过：它能启动服务，服务会一直运行，自动更新期间也是如此（见[发布与自动更新](#发布与自动更新)）；从 1.0.1 起，这样的账号不能读取各个卷，所以搜索索引会报告它需要管理员权限，其他功能照常工作。要使用搜索，服务必须以管理员权限运行（见[搜索索引](#搜索索引)）：以**使用最高权限运行**的计划任务或 SYSTEM 账号运行应该可以，但只试过已提升权限的 shell，NSSM 之类的 Windows 服务封装也没有试过。请关闭任务设置**如果任务运行时间超过以下时间，停止任务**（任务计划程序对新任务默认设为 3 天），否则时间一到服务就会被停止。收到 Ctrl+C 或 SIGTERM 时它会正常退出，最多等待 30 秒让 HTTP 服务和正在运行的任务结束。

启动时，服务会把上次进程结束时仍在运行的任务标记为失败（"interrupted by a service restart"），清除未完成恢复点的部分历史，然后继续处理排队的任务。

## 放在带路径前缀的反向代理之后

当一个主机名下有多个应用、TimeVault 只是其中之一时使用：例如 `http://prod_eng.co.murata.local/tv/` 与 `/qdb/`、`/mer/`、`/gitea/` 并列。直到 0.1.4，TimeVault 写出的每个地址都以斜杠开头（`Location: /explorer`、`/static/...`、表单、脚本发出的请求）。这在主机的根路径下是对的，在 `/tv/` 下就是错的：第一次重定向就会离开前缀，落到代理在主机根路径上提供的内容（在第一次部署中是门户页），页面也会失去样式。此时 TimeVault 的日志里只有一次请求，浏览器却已经在另一个应用上。自 0.1.5 起，服务知道自己的前缀；设计见 [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md)。

**开启方法**

1. 把站点设置 `base_path` 设为 `/tv`（**设置 → 站点设置**，分组**地址与显示**），然后重启服务（**立即重启**）。启动日志会写出 `serving under a path prefix`，站点设置页会显示**地址前缀**。不设置这一项则什么都不变。
2. 在代理中把 `/tv/` 转给本服务。服务既接受带前缀的请求，也接受不带前缀的请求，所以代理可以原样转发地址（推荐，最简单），也可以去掉前缀。
3. 紧挨着 TimeVault 的代理必须把浏览器的地址告诉它，并且必须列入站点设置 `trusted_proxies`（代理和服务在同一台电脑上时填 `127.0.0.1`）。下面的配置块适用于浏览器直接访问的代理；如果它前面还有另一层代理，请再加上[两层代理](#两层代理)中所说的内容。下面是用本版本运行过的 nginx 配置块：

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

对于来自前面 nginx 的请求，nginx 现在会把 `$remote_addr` 设为浏览器的地址；来自其他任何来源的请求保持它连接时所用的地址，它发来的头会被忽略。`trusted_proxies` 保持不变（`127.0.0.1`，即紧挨着 TimeVault 的 nginx）。重新加载 nginx 即可，TimeVault 不需要重启。`set_real_ip_from` 后面的地址是前面的 nginx 连接过来所用的地址，是紧挨着 TimeVault 的 nginx 看到的那个（它的访问日志里有，被拒绝的单点登录的 `started_from` 里也有）；它不一定是人们输入的地址。

**另一种做法。** 在紧挨着 TimeVault 的 nginx 里改为追加而不是替换（`proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;`），把两个代理都写进站点设置 `trusted_proxies`（`127.0.0.1,10.135.36.81`），并重启 TimeVault。TimeVault 取该头中最右边的、不是已列出代理的地址，所以浏览器在左边写的内容不会被信任。`real_ip` 的做法不需要改 `trusted_proxies`，并且让那个 nginx 的访问日志显示浏览器的地址。

两种做法都在本版本上运行过，经过前面的 nginx 和直接访问，有伪造的 `X-Forwarded-For` 和没有时都试过：每一次 TimeVault 得到的都是浏览器的地址。

**如何检查。** 通过前面的服务器打开 TimeVault 并登录：**审计**页的**来源**列显示的是浏览器的地址，而不是某台服务器的地址。当单点登录因为地址被拒绝时，**登录失败**记录和服务日志里的一条警告会说明 TimeVault 看到了什么：`started_from`（它当作浏览器的地址）、`sso_address`（单点登录服务记录的地址）、`connection`（连接过来的地址：最近的代理）、`trusted_proxy`（该地址是否列在 `trusted_proxies` 中）和 `forwarded`（`X-Forwarded-For` 头，截断为 256 个字符）。如果 `started_from` 是某台服务器的地址，说明前面有代理把浏览器隐藏了：按上面的方法设置。如果 `trusted_proxy` 是 `false` 且设置了 `forwarded`，说明连接过来的代理不在 `trusted_proxies` 中。如果浏览器通过 IPv4 访问前面的服务器、通过 IPv6 访问单点登录服务（或者相反），也会被看成两个地址；这两个值能看出这一点。

**未验证。** 公司自己的服务器：以上步骤是在一台电脑上，用两个 `/tv/` 配置块的副本和代表服务器的回环地址运行的。

## 测试

`go test ./...` 运行全部测试。需要 PostgreSQL 的测试在 `TIMEVAULT_TEST_DATABASE_URL` 未指向某个数据库时会被跳过；每个测试都会创建名为 `tvtest_<random>` 的私有 schema 并在结束后删除，所以请使用单独的测试数据库，例如 `timevault_test`，不要使用正式使用的数据库。`dev-db.ps1` 会把私有集群的 URL 写入 `.env`；如果 `.env` 指向的是你自己的 PostgreSQL 服务器，请先在那里创建测试数据库（`CREATE DATABASE timevault_test OWNER timevault;`），并在 `.env` 中设置 `TIMEVAULT_TEST_DATABASE_URL`；该角色不需要其他权限。涉及联接点和 Windows 文件锁的测试只在 Windows 上运行。NTFS 索引在所有系统上都用卷的模型测试，并且还有一个读取真实卷的在线测试（`internal/ntfsindex` 中的 `TestWindowsVolumeMatchesAWalkAndFollowsTheJournal`，仅限 Windows）：它需要管理员权限，所以要从已提升权限的 shell 运行，否则会被跳过；它只读取卷，并只向它事后会删除的临时文件夹写入（见[搜索索引](#搜索索引)）。Redis 测试不需要 Redis：它们使用进程内的模拟服务和 Python 生成的 pickle 数据。

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

1. 把站点设置 `sso_host` 设为单点登录服务的地址，`<sso.example.local>`（`host`、`host:port` 或 `http(s)://host[:port]`），在**设置 → 站点设置**，分组**登录**。因为地址可能变更，所以它是一个单独的设置。重启服务（**立即重启**）。登录页现在有按钮**使用 Windows 账号登录（SSO）**，启动日志和站点设置页会显示单点登录服务（`sso_host`）。没有这个设置时，登录页与以前一样。
2. 单点登录服务必须把它的结果（`sso_login_<token>`）写入 `TIMEVAULT_REDIS_URL` 所指的 Redis（相同的数据库编号），并且 TimeVault 的 Redis 账号必须被允许读取它们。TimeVault 只读取它们；它从不删除这个键，单点登录服务会在 5 分钟后让它过期。
3. 在反向代理之后（通常的情况），二选一：
   - **推荐。** 把代理列在站点设置 `trusted_proxies` 中（用逗号分隔的地址或 CIDR 范围）。代理必须把浏览器的地址放进 `X-Forwarded-For` 头，并且原样传递 `Host` 头，因为"请求来自站点本身"的检查会比较 `Origin` 和 `Host`。这样 TimeVault 也会把真实地址写入审计日志的**来源**列，并按浏览器而不是按代理统计登录尝试。测试过的 nginx 配置块见[放在带路径前缀的反向代理之后](#放在带路径前缀的反向代理之后)。
   - 把站点设置 `sso_require_same_ip` 设为 `false`。这样就不再拿浏览器的地址与单点登录服务看到的地址比较。这样做较弱；见 ADR 0009。

   两者都没有时，每次单点登录都会以"无法把这次登录与当前浏览器对应起来"结束，因为 TimeVault 拿代理的地址与浏览器的地址比较。

   在直接访问 TimeVault 的开发机上（例如 `http://localhost:8660`）也会这样：TimeVault 看到的是回环地址（`127.0.0.1` 或 `::1`），而单点登录服务看到的是这台电脑的网络地址。请改用经过代理的站点地址访问，或者只在这台电脑上把 `sso_require_same_ip` 设为 `false`。

**人们会看到什么。** 按钮会打开一个带有单点登录页面的小窗口，登录页显示"正在等待 Windows 登录"。几秒钟后窗口自行关闭，此人登录成功，拥有 Redis 中该账号的角色（`admin` 或不是），并回到他来时的页面。出问题时页面会说明原因：浏览器拦截了窗口（请允许本站弹出窗口）、Windows 无法识别你的身份、你的 Windows 账号在这里没有可用的账号、无法把这次登录与当前浏览器对应起来、登录耗时过长或已被使用、窗口被关闭，或无法连接用户账号服务。密码表单照常可用。

**如何检查。** 打开登录页并点击按钮。**审计**页随后会显示**已登录**，带有 `method: sso` 和 Windows 用户；被拒绝的尝试显示**登录失败**，带有 `reason`（`no_active_account`、`no_windows_user`、`address_mismatch`、`browser_address_changed` 或 `result_unreadable`）。对于与地址有关的两种原因，它还会显示比较的两个地址和请求是怎样到达服务器的（`started_from`、`sso_address`、`connection`、`trusted_proxy`、`forwarded`；见[两层代理](#两层代理)），服务日志里也有一条包含同样内容的警告。

## 搜索索引

搜索、文件浏览中的文件夹大小和调度器的变更探测都来自同一个地方：服务保存在自己内存中的一个索引，其中是监控文件夹之下的文件和文件夹。它由 NTFS 卷的文件记录（主文件表）建立，并由该卷的变更日志（USN 日志）保持更新。它不需要任何其他程序，不需要 HTTP 服务，除站点设置 `search_index`（默认 `auto`；`off` 表示关闭它）之外也不需要任何设置。它回答的是*此刻*存在什么；它不是历史搜索：浏览过去的状态使用目录库。原因见 [ADR 0018](./adr/0018-native-ntfs-index-replaces-everything.md)。

**要求**

- **管理员权限。** Windows 只允许以管理员身份（或 SYSTEM 账号）运行的进程以读取方式打开卷，所以服务必须这样运行，例如从已提升权限的 PowerShell 启动。服务只读取卷，从不向它写入。没有这个权限时，状态页会把搜索显示为 `degraded`（"服务没有管理员权限，无法读取卷来建立索引。"），搜索回答"不可用"，文件夹大小显示"-"，调度器照旧扫描；其他功能不受影响。
- **带盘符的本地 NTFS 卷。** 位于网络共享（`\\server\share\...`）上的监控文件夹不会被建立索引，不是 NTFS 的卷也不会（状态页会说明哪个卷无法建立索引以及原因）：它的搜索回答"不可用"，文件夹大小显示"-"，对它的扫描也从不会被跳过。只试过 NTFS；没有试过 ReFS、FAT 和 exFAT 卷。
- 卷上有**处于活动状态的变更日志**。如果没有，状态页会这样说，并给出创建它的命令（`fsutil usn createjournal m=33554432 a=8388608 <盘符>:`）。这种情况只用卷的模型测试过，没有在没有日志的真实卷上试过。

**保存的是什么。** 只有监控文件夹：卷的文件记录会被读取，建立完成时，监控文件夹之外的一切都会被丢弃。对于每个文件和文件夹，索引保存它的名称、大小（未命名数据流的长度，也就是备份所读取的内容）、最后写入时间，以及它所在的文件夹。联接点或符号链接（重解析点）不会被进入，有多个硬链接的文件只按它的第一个名称保存，短（8.3）名称会被跳过。每个被保存的条目大约占用 230 字节内存：一百万个文件大约 230 MB。

**建立。** 服务启动时，以及该卷上的监控文件夹发生变化（添加、移动或移除了一个）时，该卷的索引会在后台建立：文件记录会被读取两遍，先读文件夹，再读监控文件夹之内的内容。这需要几秒钟，而不是几分钟，取决于卷而不是保存了多少内容（在一个有 200 万个条目的卷上是 13 秒）。一个卷正在建立时，搜索对它的文件夹回答"不可用"（状态页写着"正在建立 C: 的索引。"）；正在被替换的索引会一直应答，直到新的就绪。当变更日志在索引读取它之前被替换或被覆盖（日志回绕）时，索引会重新建立；当有文件夹从外部被移入监控文件夹时（索引无法知道其中有什么），也会重新建立，最多每分钟一次。

**保持最新。** 服务每 10 秒读取一次变更日志，并且在回答问题之前，如果过去 2 秒内没有读过，也会先读（调度器的探测总是会读），所以刚刚创建、写入、重命名、移动或删除的文件都能被找到：在对真实服务的测试中，它们在 2 秒内就出现在搜索中。发生变化的文件的大小和时间稍后会从磁盘重新读取。

**它回答什么**

- **搜索**（**文件浏览**中的搜索框）。输入的文本会被拆分成词（空白分隔，双引号表示一个短语；最多 256 字节和 16 个词）。`*` 和 `?` 被当作分隔符，因为它们不可能出现在 Windows 文件名中。词是纯文本：`|`、`!`、`<>`、`regex:`、`size:` 或 `content:` 会按输入的样子去搜索。名称包含每一个词（不区分大小写）就算匹配。搜索只在监控文件夹之内进行，每个结果还会再对照它的根目录检查一次；范围之外的结果会被丢弃并计数，绝不会显示。结果先列文件夹，再按名称排序，每一页的顺序都一样；每页最多 200 条结果（可选 50、100 或 200），带页码链接。
- **文件浏览中的文件夹大小。** 文件浏览会向索引询问当前文件夹里的各个文件夹，并显示它们的大小（其下所有文件大小之和，递归计算），所以文件夹行有大小，文件夹也可以按大小排序。文件夹的修改时间仍然是它自己的，从磁盘读取。答案来自索引，所以可能比磁盘稍微滞后。TimeVault 会把一个答案保留 10 秒，最多等待 2 秒，当索引没有应答时显示"-"，并且 15 秒内不再询问。时间机器和版本页面读取的是目录库，不显示文件夹大小。
- **调度器的变更探测。** 某个文件夹之下自某个时间以来是否有变化，以及其下有多少条目；见[调度器设置](#调度器设置)。

**状态。** **状态**页把搜索显示为 `ok`（"N 个条目，位于 C:。"，或者在没有监控任何东西时显示"没有需要索引的监控文件夹。"）、`degraded`（某个卷正在建立，或无法建立索引：没有管理员权限、没有变更日志、不是 NTFS，或服务日志点名的其他失败）或 `disabled`（站点设置 `search_index` 为 `off`）。`GET /api/v1/health` 的 `search` 组件中也有同样的内容。某个卷建立完索引时，日志里有一行（`ntfs index: the volume is indexed`，带有卷、条目数和所用秒数），不能建立时有一条警告。

**已验证与未验证。** 已在开发电脑的 NTFS 卷上、从已提升权限的 shell 中，用 Windows 11 验证：真实文件夹的索引在名称、大小和时间上与对相同文件夹的遍历一致，并能跟上创建、写入、重命名、移入移出以及删除的文件或文件夹（在线测试 `TestWindowsVolumeMatchesAWalkAndFollowsTheJournal`）；一个有 200 万个条目的索引（`C:\Windows`、`C:\Program Files`、`C:\Program Files (x86)` 和 `C:\Users`）在 13 秒内建好，占用 453 MB，在其中最大的那个文件夹（175 万个条目）之下搜索大约需要 0.13 秒；真实的服务在一个新数据库上从头到尾做到了这些：它在 14 秒内为一个监控文件夹建好了索引，用多个词和不同的字母大小写找到了文件，给出了正确的文件夹大小，让创建、删除或重命名的文件在 2 秒内出现，在索引已经监视大约 7.5 分钟（探测回看的 5 分钟、1 分钟的间隔和间隙）之后跳过了未变化文件夹的扫描，并且在那之后某个文件发生变化时再次扫描并生成了恢复点。未验证：ReFS、FAT 或 exFAT 卷、网络共享、没有变更日志的卷以及服务运行期间日志回绕的情况（只用卷的模型测试过）、作为计划任务或以 SYSTEM 账号启动的服务（只用过已提升权限的 shell），以及真实卷上 500 GB 或一百万个文件的监控文件夹（上面的数字来自所列的文件夹和每个条目开销的推算）。

## 调度器设置

自动备份按文件夹在**设置**页设置（"自动备份，间隔 N 分钟"）。下面这些设置，即分组**调度器**的站点设置（以及 `process_priority`），只决定到期的备份如何错开，这样有大约 10 个监控文件夹的服务器就不会一次把它们全部扫描。原因见 [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md)；变更探测现在询问的是 NTFS 索引（[ADR 0018](./adr/0018-native-ntfs-index-replaces-everything.md)）。

- **一次一个。** `scheduler_parallel` = `1`（1 到 4）。用**立即备份**启动的备份不受限制，也不计入。
- **间隔。** `scheduler_gap` = `auto` 是刚好跟得上所有计划的间隔，`1 / Σ(1 / 间隔)`，限制在 30 秒到 10 分钟之内：十个间隔为 60 分钟的文件夹每 6 分钟启动一个。可以用 `5m` 这样的时长固定它；`0s` 关闭间隔。
- **休息。** `scheduler_rest` = `auto` 在一次运行之后等待与它所花时间一样长的时间（30 秒到 10 分钟），所以大文件夹的长时间扫描之后会有一段长时间的停顿。下一次启动时间取"上次启动之后的间隔"和"上次结束之后的休息"中较晚的那个。`0s` 关闭它。只在 `scheduler_parallel` 为 1 时生效。
- **顺序。** 没有恢复点的文件夹先来，然后是相对于自己的间隔超期最多的，再然后是上次运行最快的，最后是 id 最小的。上次运行花了多久显示在设置页上（"上次耗时"）。
- **优先级。** 默认是 `process_priority` = `below_normal`，这样备份扫描会让位于同一台电脑上的文件共享。`background` 还会降低磁盘优先级；`normal` 就是 0.1.4 之前的样子。
- **变更探测。** `scheduler_probe` = `true`（默认关闭）让调度器先问 NTFS 索引，并跳过没有变化的文件夹的扫描。它需要该文件夹的索引已经就绪（见[搜索索引](#搜索索引)）。在以下情况下文件夹仍然会被扫描：索引不认识该文件夹、尚未就绪，或 3 秒内没有应答；该文件夹之下的条目数量不等于上次完整扫描时的数量（删除就是这样被发现的）；自上次扫描前 5 分钟以来其下有任何内容被创建、修改、重命名、移动、删除或更改了属性；索引自上次扫描前 5 分钟以来没有一直在监视该卷（见下）；最新的恢复点有未捕获的项目；自上次扫描以来更改过该文件夹的排除规则；以及至少每隔 `scheduler_full_scan_every`（24 小时）。索引统计的是监控文件夹之下任何位置的变化，所以探测说"有变化"的次数可能比严格需要的多，但绝不会更少。一个周期最多询问索引 25 次，没有应答的索引在一分钟内不会再被询问。设置 `log_level` 为 `debug` 后，每次跳过都会在日志中写下"scheduled scan skipped: the search engine sees no change"。被跳过的文件夹不会在列表中留下任何内容，就像任何没有发现变化的运行一样，它的间隔会重新开始计算。
- **启动或重建索引之后。** 索引不知道它开始监视该卷之前发生过什么，所以在一个文件夹最近一次完整扫描是在那一刻之后至少 5 分钟做的之前，它不会被跳过。因此，在服务重启之后，或者在某个卷的索引被重新建立（例如添加了一个监控文件夹）之后，每个文件夹都会再照常扫描一次（间隔短于 5 分钟时，则一直扫描到过了 5 分钟）才恢复跳过。为站点设置而重启的代价相同。每次扫描之前都会统计该文件夹之下的条目数，之后的探测拿它做比较；在索引未就绪时进行的扫描（例如启动之后的第一次）没有这个数，所以下一次探测不能跳过。
- 间隔状态保存在内存中：重启后，第一个自动备份可能立即启动。

## 发布与自动更新

站点设置 `update_url` 已设置的服务器，会在发布了更新的版本时替换自己的程序：它等正在运行的任务结束，交换文件，然后重启，不需要任何人守在服务器旁。决定和备选方案见 [ADR 0014](./adr/0014-self-update-from-a-release-manifest.md)。

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
      "latest_version":  "1.0.1",
      "sha256":  "<64 hexadecimal characters>",
      "package_url":  "timevault.exe",
      "size":  23352320
  }
  ```

  `package_url` 是文件名，服务会在它读取的清单旁边查找（适用于文件夹、共享，或同时存放这两个文件的 Web 文件夹）；使用 `-BaseUrl` 时则是程序的完整地址。手写的清单也可以：版本前面的 `v` 和未知字段都会被接受。
- 把 `timevault.exe`，再把 `timevault.json`，复制到服务器读取的**发布文件夹**。服务在复制过程中读取该文件夹时，会看到大小或哈希不一致，并在下次查看时再试；它绝不会安装只复制了一半的文件。

**服务器从哪里读取。** `update_url` 可以是：

- 文件夹或共享，`D:\TimeVault\release` 或 `\\server\share\tv-release`（运行服务的账号需要有读取权限；服务启动时无法访问的共享，在能访问之后就会被找到）；或者
- 清单或其所在文件夹的 `http(s)` 地址，由任何能原样提供这两个文件的 Web 服务器提供。**其他公司应用的静态文件就是放置它的地方：** 清单是 `http://<server.example.local>/api/static/cd/timevault.json`，其中 `package_url` 为 `timevault.exe`（只是一个文件名，不是地址）时指向 `http://<server.example.local>/api/static/cd/timevault.exe`。把 `timevault.exe`，再把 `timevault.json`，复制到该应用的 `static\cd` 文件夹（文件夹只需创建一次）；地址原样填写，**不会**从 `server_host` 推导。对于不存在的文件，该应用会回答 `202` 和一张图片，所以在文件复制过去之前，设置页会显示**出现问题**以及 `… answered 202 Accepted`。每次查看（默认每 10 分钟一次）都是对该应用的一次请求，它会把这次请求写入自己的 Web 日志。其他任何 Web 服务器的用法相同，例如 nginx：`location /tv-release/ { alias D:/TimeVault/release/; }`。会跟随重定向（至多五次）。`200` 以外的应答（例如 `202` 或 `404`）都是错误。

**在服务器上启用（只需一次）。**

1. 把 0.1.8 或更新版本的 `timevault.exe` 复制到服务器上，放在 `.env` 旁边。这是最后一次手工复制：更早的版本不能自己更新。
2. 把 `TIMEVAULT_AUTO_MIGRATE=true` 写入 `.env`（有更新地址的服务没有它会拒绝启动，因为服务自己更新时没有人会运行 `migrate`）。然后以本地管理员身份，把站点设置 `update_url` 设为清单的地址，例如 `http://<server.example.local>/api/static/cd/timevault.json`，如果需要，再把 `update_interval` 设为 `5m`（**设置 → 站点设置**，分组**自动更新**），并重启服务。
3. 运行服务的账号需要对 `timevault.exe` 所在的文件夹有**修改**权限（`timevault.new.exe`、`timevault.old-<版本>.exe` 和 `timevault.exe.update.json` 都写在那里），并对发布文件夹有**读取**权限。
4. 启动服务。启动日志会写出 `automatic updates are on` 以及地址和间隔，并在程序所在的文件夹不能写入新文件时发出警告。**设置**页有一张**程序更新**卡片：状态、已发布的版本、清单最近一次读取的时间，以及（仅管理员能看到的）发布地址、最近的问题和**立即检查**。

**发布了新版本之后会发生什么。**

1. 在下一次查看时（在间隔之内；管理员点击**立即检查**或调用 `POST /api/v1/update/check` 时立即查看；启动后 10 秒也会查看），服务发现一个不是正在运行的版本。它把程序下载到 `timevault.new.exe`，检查大小和 sha256，并用 `version`（必须输出清单中的版本）和 `check-config`（必须接受本服务的设置）运行它。
2. 它停止取用任务：设置页显示**等待正在运行的任务**及其数量，每个已登录的账号都会在页眉下面看到提示（_版本 1.0.1 将在正在运行的任务结束后安装。新任务会等到那时再开始。_）。正在运行的任务继续运行到结束；新的任务（包括调度器的）在队列中等待。没有时间上限。
3. 没有任务运行时，它把正在运行的 `timevault.exe` 重命名为 `timevault.old-<版本>.exe`，把新文件放到 `timevault.exe` 这个名字上，写下说明文件，停止调度器、Web 服务器和任务运行器，然后启动新程序。之后排队的任务开始运行。
4. 新程序在日志中写出 `automatic update: this program has taken over`，并写入审计事件；**审计**页会列出操作者为 `updater` 的**程序更新**事件（`staged`、`restart`、`done`，以及 `cancelled` 或 `failed`），详情中没有路径，也没有错误文本（它们在服务的日志中）。

如果发布又回到了正在运行的版本，等待就会结束，任务会重新被取用。出现问题（读不到清单、程序的大小、哈希或版本不对、新程序拒绝这些设置、交换失败）时，会显示在设置页上（文本只给管理员看），在日志中只记录一次，并在下次查看时再试；服务继续使用旧程序运行。大小或哈希不对也会再试，因为还在复制中的发布就是这个样子；版本与清单不一致的程序在清单改变之前不会再试。对 `http(s)` 地址的下载如果两分钟内没有收到任何数据就会放弃；下载之后被移除的程序（例如被病毒扫描程序删除）会在下次查看时重新下载。

**更新之后怎样启动程序（`update_restart`）。**

- `supervise`（默认）：被启动的那个进程作为一个小型**启动器**继续存在，并把新程序作为子进程运行，参数、环境变量、文件夹和控制台都与之前相同。在控制台窗口中 Ctrl+C 会同时停止两者；作为计划任务时，任务始终只有一个运行中的进程，也不需要"失败时重新启动"。启动器以其他任何方式结束时，例如任务被结束（任务计划程序会立即强制终止进程），程序会发现启动器已经不在，有序关闭并释放端口，所以结束任务不会留下任何东西。启动器是最先被启动的那个程序，它的代码保持不变：更新替换的是磁盘上的文件，而不是启动器，所以要改动启动器本身，需要重启启动器。它活着的时候，它所运行的那个文件（`timevault.old-<版本>.exe`）会一直被锁定。
- `exit`：进程以退出码 **75** 结束，启动它的东西必须再次启动它。由服务管理器运行程序时使用它，例如设置了 `AppExit 75 Restart` 的 NSSM（没有试过：测试时没有 NSSM）。使用 Windows 任务计划程序时，触发器每分钟重复一次并选择**不要启动新实例**（`MultipleInstances IgnoreNew`）的任务，会在下一次重复时启动新程序，而程序运行期间的重复不会启动任何东西：这一点试过。"如果任务失败，每隔……重新启动"这个设置也试过，它在程序以 75 结束之后**没有**再次启动程序，所以不要依赖它。由启动器启动的程序无论设置如何，始终以 75 退出，因为启动器在等待这个退出码。

**回退。** 上一个程序是 `timevault.old-<版本>.exe`（保留最新的三个）。**请先把较旧的发布放回发布文件夹，或者清空 `update_url`：** 否则服务会在下次查看时再次更新到较新的版本，因为清单中的版本不是正在运行的版本。

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

页眉、审计日志、任务页和任务列表、文件夹访问权限的摘要，以及权限编辑器中的人员列表，都会在账号名称旁边显示该账号的头像。浏览器从站点设置 `avatar_url` 读取每张头像。未设置时，它就是 `http://<server_host>/api/static/hr/{id}.webp`：即 `server_host` 所指服务器上，公司其他应用保存的员工照片（`{id}` 是工号）。`server_host` 没有设置时没有头像，除非直接写明地址。

- **设置。** `off`（或 `none`）表示关闭头像。完整的 `http(s)` 地址表示从另一个主机读取。像 `/api/static/hr/{id}.webp` 这样的路径表示从站点被打开时所用的主机读取（不会放到 `base_path` 之下），适合代理在所有名称下都提供那个应用的站点；直接通过服务自己的端口打开时没有这样的路径，每个人显示的都是默认头像。`{id}` 必须恰好出现一次，地址中不能带用户名或密码。
- **由浏览器读取，而不是服务。** 服务从不获取或提供头像，也不存储任何东西。站点和头像可能在不同的主机上，所以每个页面的内容安全策略都会把头像地址的主机列入图片来源，并且只用于图片，不用于其他任何东西（`img-src 'self' data: http://<host>`）。带下划线的主机名，例如 `prod_eng.co.example.local`，无法写进策略（浏览器会忽略整条），所以改为列出它外面的域：`http://*.co.example.local`。如果没有剩下两个标签的域（`prod_eng`、`prod_eng.local`），头像只有在用同一个地址打开站点时才会显示，启动日志会写出"cannot be named in the content security policy"。通过 `https` 提供的站点需要 `https` 地址：浏览器不会在 `https` 页面中显示来自 `http` 的图片。
- **没有头像的人。** 另一个应用把头像以 `text/plain` 发送，图片并不在意这一点；对于没有头像的人，它回答 `202` 并附带一张 480×320 像素的占位图，而不是 `404`。恰好是这个尺寸的图片、失败的请求以及没有应答的主机，都会保留默认头像。0.1.8 中头像不显示的原因就在这里：它只接受 `image/*`。
- **请求频率。** 页面存活期间，浏览器对每张头像只请求一次，并且只请求即将被看到的头像（权限编辑器的人员列表很长）。缺失的头像在浏览器会话中会被记住十分钟，这样列表不会一遍又一遍地请求，而曾经不可用的主机也会很快被再次尝试。
- 本地管理员、服务自己使用的名称（`scheduler`、`updater`、`system`）以及不可能是工号的名称，都不会去请求头像。

## Web 日志

从 1.0.0 起，服务会把行追加到公司各应用共用的 **Web 日志**：Redis 列表 `web_log`，位于 `TIMEVAULT_REDIS_URL` 所指的 Redis 中（同一个数据库编号）。设置了 `TIMEVAULT_REDIS_URL` 时它默认开启。这是 TimeVault 第一次写 Redis：账号和单点登录结果仍然只读取，`internal/redisuser` 仍然从不写入。决定和备选方案见 [ADR 0016](./adr/0016-web-log-in-the-shared-redis-list.md)。

**设置**（站点设置，分组**日志**；见[站点设置](#站点设置)）

- `web_log`：`auto`（默认）恰好在设置了 `TIMEVAULT_REDIS_URL` 时开启；`on`（`true`、`1`、`yes` 含义相同）需要 `TIMEVAULT_REDIS_URL`，没有它站点设置页会拒绝；`off`（`false`、`0`、`no`）从不写入。**要关闭它，请在站点设置页把 `web_log` 设为 `off` 并重启服务。**
- `web_log_key`（默认 `web_log`）：列表。只能包含 ASCII 字母、数字和 `_ . : -`，最多 128 个字符。无论大小写，它都不能以账号的键（`TIMEVAULT_REDIS_KEY_PREFIX`，默认 `app_user_`）或单点登录结果的键（`sso_login_`）开头，这样这个设置就永远不会让服务写入它们的命名空间；站点设置页会拒绝这样的键。
- `web_log_host`：每一行的最后一个字段。未设置时：小写的计算机名。设置的值按原样使用；只能包含 ASCII 字母、数字和 `. - _`，最多 63 个字符。
- `TIMEVAULT_REDIS_URL` 中的 Redis 账号必须被允许对该列表执行 `RPUSH`，这是 TimeVault 唯一的写入。

启动日志的 `starting TimeVault` 一行中有 `web_log`、`web_log_key` 和 `web_log_host`，Web 日志开启时还有第二行 `web log is on`，其中是列表、主机名，以及显示为 `host:port/db` 的 Redis（绝不显示密码）。这三个设置在**站点设置**页上，保存时会检查（服务启动时会再检查一次），`GET /api/v1/settings` 中有 `web_log`（仅限本地管理员）。

**一行的格式。** 列表中的每个元素都是 `zlib.compress(pickle.dumps(line, protocol=4))`，按 Python 写出的方式写出，所以能读其他应用那些行的东西也能读这些行。一行有六个字段，用 ` | ` 分隔，与它们的一样：

```text
ip | YYYY-MM-DD HH:MM:SS.ffffff | message | status | level | host
10.0.0.5 | 2026-11-18 14:05:12.250431 | TimeVault: /explorer user=E123456 0.112(s) | 200 | info | sgmeslt595
```

- `ip` 是浏览器的地址（连接的地址，或 `trusted_proxies` 中的代理所报告的地址），服务自己做的事（调度器、更新器、启动和停止）用 `127.0.0.1`。时间是服务器的本地时间。`status` 是 HTTP 状态码，审计记录和调度器报告的内容则是 200、403 或 500。`level` 是 `info`、`warning`、`error` 或 `debug`。
- TimeVault 从不增加字段，所以按位置拆分一行的东西仍然可用：用户以 `user=<工号>` 写在 message 中（本地管理员用它的名称，没有登录的访问者是 `guest`）。每条 message 都以 `TimeVault: ` 开头。message 中的 `|` 会变成 `/`，控制字符会变成空格，message 最长 400 个字符，所以一行始终有六个字段。

**写入什么。**

- **有人打开的页面**，即页面的每一次 `GET`：`TimeVault: <route> user=<工号或 guest> <seconds>(s)`，例如 `TimeVault: /explorer user=E123456 0.112(s)`。route 是匹配到的模式，其中的数字已填入（`/explorer`、`/jobs/12`），绝不是输入时的地址（没有查询串、没有路径、没有搜索文本）；没有匹配任何路由的地址写作 `(not found)`。`2xx` 是 `info`，`4xx` 是 `warning`。不写入：`/static/…`、`/healthz`、`/api/…`、重定向（`3xx`；它们指向的页面在被打开时才写入），以及页面脚本自己发起的重新加载，它带有请求头 `X-TV-Auto: 1`（任务页在任务运行期间会自己重新加载，备份页在它显示的某个任务结束时也会）。
- **在服务器上失败的请求**，任何 `5xx`（包括 API 的）和 panic：`TimeVault: error <route> user=<工号> ref=<参考编号>`，`error`，状态码就是实际应答的状态码。`ref` 是错误页面显示的参考编号，控制台日志把它记作 `id=`，并连同错误文本一起保存（文本本身绝不写入 Redis）；没有自己参考编号的 `5xx`，例如文件夹无法读取时的 `503`，没有 `ref=`。
- **每一个审计事件**，在它被存入数据库之后，也就是人们做的事，以及调度器和更新器做的事：`TimeVault: <action> [method=… route=…] [stage=… from=… to=…] [source=<文件夹 id>] [<对象类型>=<id>] user=<操作者> [<seconds>(s)]`。例如 `TimeVault: backup.request source=3 user=E123456`、`TimeVault: backup.finish source=3 point=17 user=scheduler 12.400(s)` 和 `TimeVault: access.denied method=POST route=/settings/sources user=E1001`。结果决定状态码和级别：`ok` 是 200 和 `info`，`denied` 是 403 和 `warning`，`failed` 是 500 和 `error`。地址是该请求的地址（调度器和更新器用 `127.0.0.1`）。事件的详情中只使用被拒绝请求的方法和路由，以及程序更新的阶段、`from` 和 `to`；登录失败时输入的名称（它可能是密码）绝不复制（`user=-`），没有操作者的事件是 `guest`。
- **调度器：** `TimeVault: scheduler.queued source=<id> user=scheduler`（排入了一个自动备份；`info`）、`TimeVault: scheduler.error [source=<id>] user=scheduler`（调度器同时也会在控制台中警告的问题，每个文件夹每分钟最多一次；`warning`，状态码 500），以及只在调试级别才写的 `TimeVault: scheduler.skipped source=<id> user=scheduler`（NTFS 索引看到没有变化）。
- **服务本身：** 一切构建完成后写 `TimeVault: started version=<版本>`，停止时写 `TimeVault: stopped`。
- **调试行**，只在 `log_level` 为 `debug` 时写入：`GET /api/…`（任务的轮询）以及页面脚本自己发起的重新加载的页面行。其他日志级别不会改变 Web 日志的任何内容：`warn` 和 `error` 不会把 `info` 行从中去掉。

**绝不写入：** 路径、文件或文件夹的名称、查询串、有人输入的文本（搜索，或登录表单中输入的名称）以及错误文本。一行中只有路由、数字（监控文件夹、恢复点、任务）、工号和地址。

**限制，以及 Redis 不可用时会怎样。** Web 日志是尽力而为的，并且从不妨碍服务。

- `Write` 只把行放进容量为 1,024 的队列就返回，所以请求、审计记录或调度器的一次运行从不等待 Redis。一个 worker 用一次 `RPUSH` 发送最多 100 行，使用它自己的、含 2 个连接的连接池；每次发送最多等待 `TIMEVAULT_REDIS_TIMEOUT`。
- 大量涌入的行在入口处被截断：页面行平均每秒 10 行（突发 100 行），事件行（审计事件、错误、调度器、启动和停止）每秒 20 行（突发 200 行）。队列满了就丢弃这一行。疯狂点击的访问者无法让共用的列表被淹没。被这些限制和满队列丢弃的行会被计数，并用警告 `web log: lines were dropped because too many came at once` 报告，最多每分钟一次。
- `RPUSH` 失败之后，worker 会先暂停访问 Redis 30 秒，然后更久，最长 5 分钟，期间到来的行都被丢弃。它会警告一次 `web log: the lines cannot be written to Redis, so they are dropped until it answers`，其中 URL 的密码被遮盖，问题持续期间最多每 5 分钟再警告一次，重新接收行时会记录 `web log: Redis takes the lines again`。被丢弃的行以后不会再写。Redis 不可用期间，服务本身照常启动和运行。
- 关闭时先发送队列，最多等待 3 秒（不够时会记录 `web log: the last lines could not be written before the service stopped`）。
- TimeVault 从不裁剪列表，也不让它过期：它是共用的，由其他应用管理。一行在 Redis 中约 100 到 150 字节。写入量受上面的限制约束，但不为零。Web 日志不能替代审计记录：Redis 不可用期间它会丢失，而审计事件只有在被存入数据库之后才会进入它。
- 这些行里有工号和客户端地址，所有能读取这个列表的人都能读到。

**如何查看（只读）。** `LLEN web_log` 统计行数，`LRANGE web_log -5 -1` 显示最后五行；请使用与 `TIMEVAULT_REDIS_URL` 中相同的主机、端口、数据库编号和密码，并且绝不要使用会改变列表的命令。元素是压缩过的 pickle，所以看起来像二进制数据。人可以用 Python 读取它（只能读取你信任的列表：`pickle.loads` 会执行数据要求它执行的内容），也可以在另一个应用的日志页面上查看：

```python
import zlib, pickle
print(pickle.loads(zlib.decompress(element)))   # element: the bytes of one list element
```

**如何检查。** 打开一个页面，再看一次 `LLEN web_log`：它会在片刻之内增加。如果没有增加，见[故障排查](#故障排查)。

**未验证。** Web 日志没有写入过公司的 Redis：它是针对进程内的 Redis 和 Python 打印出的 pickle 测试的。该 Redis 账号是否可以在那里执行 `RPUSH`，以及另一个应用的日志页面是否会显示这些行，需要在首次启动之后去查看。

## 运维

- **谁可以做什么。** 任何人无需登录就可以浏览允许访客浏览的文件夹（文件浏览、时间机器、文件版本、备份页和备份任务的状态）；不允许的文件夹对访问者来说根本不存在。其他一切都需要已启用的账号，而账号能对某个文件夹做什么是按文件夹分别决定的：**浏览**（也包括恢复，以及该文件夹的任务和审计事件）、**备份**（立即备份、完整重读、校验、停止任务）和**完全控制**（修改文件夹的设置以及谁能对它做什么）；见[文件夹权限](#文件夹权限)。审计日志（账号可以浏览的文件夹的事件，以及登录等不属于任何文件夹的事件）、状态页和设置页对每个已登录的账号开放。只有 `admin` 账号（Redis 中 `Type` 为 `admin`，或本地管理员）才能添加监控文件夹，并对所有文件夹拥有完全控制权，也只有管理员能看到服务器磁盘上的文件夹；其他人看到的是显示文件夹。见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)、[ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md)、[ADR 0013](./adr/0013-per-folder-access-levels.md) 和 [api.md](./api.md#谁可以调用什么)。
- **浏览很长的文件夹。** 文件浏览和时间机器把文件夹分页列出（默认每页 100 项；可选 50、100、200 或 500，列表上方和下方有数量和页码链接）。**类型**列以大写显示扩展名（文件夹显示"文件夹"，没有扩展名的文件显示"文件"）；列表上方的**类型**下拉框只保留一种（所有文件夹、所有文件、没有扩展名的文件，或某一种扩展名，每项都带数量）。点击**名称**、**类型**、**大小**或**修改时间**会对整个文件夹排序，再点一次反向；文件夹始终排在最前面。你的选择写在地址里，所以链接会保留它。时间机器中的**全选**选中当前页的项目，一次恢复最多可带 500 个路径。右下角有四个圆形按钮：两个箭头可以跳到顶部和底部，只在较长的页面上出现；以及始终可见的**文档**和**页面导览**按钮（见下一项）。包含 10,000 个文件的文件夹约 0.1 秒打开，包含 50,000 个文件的约 0.5 秒（在开发机器上测得，所有文件都在同一个文件夹里）；单个文件夹中有几十万个条目时需要几秒钟，因为每一页都会读取整个文件夹。
- **用户手册和页面导览。** **文档**按钮（书本图标，位于除登录页之外每个页面的右下角）在新标签页中打开用户手册：`GET /help`，应用自己的一个页面，对所有人开放，有英文和中文两个版本（取决于 EN / 中文 切换）；它说明 TimeVault 是做什么的，以及每个页面怎么用。**页面导览**按钮（指南针图标）开始逐步介绍你当前所在的页面：页面变暗，一次点亮一个部分，一个气泡说明它是什么，并带有"第 n 步，共 N 步"、**上一步**、**下一步**（最后一步是**完成**）和一个关闭按钮。左右方向键用于前进和后退，**Esc** 关闭导览，**Enter** 按下获得焦点的按钮；点击变暗的页面没有任何作用，也不会替你点击或更改任何东西。页面自己的部分排在最前面，然后是页眉（导航、主题、语言、账号或登录）和圆形按钮；页面没有显示的部分（例如只有部分访问者才有的按钮）没有对应的步骤。在窄屏幕（640 像素或更窄）上，气泡停靠在窗口底部（介绍圆形按钮时在顶部）。显示另一个页面或使用后退按钮时，导览结束；同一个页面自己刷新时（任务页在任务运行期间会这样），导览继续。登录页两个按钮都没有。导览是 `web/assets/js/tour.js`；步骤写在模板中（`Page.Tour`），文字在 `internal/server/i18n_tour.go` 中。`help` 是应用自己的页面，所以 `base_path` 不能以 `/help` 开头。
- **健康检查。** `GET /healthz` 无需登录即返回 `ok`，只能证明进程在运行。**状态**页（仅限账号）检查数据库、仓库（写入测试和剩余空间；剩余不足 5% 时为"降级"）、搜索索引、Redis 用户目录（没有应答时为"降级"：浏览、已有会话和本地管理员仍可使用）、恢复目录和任务队列，并把 VSS 列为"已禁用"。没有会话时，只有 `/healthz`、登录页、语言切换、用户手册（`/help`）、静态资源和浏览页面可以访问。
- **日志。** 服务把日志写到控制台（标准错误），格式为文本，设置 `log_format` 为 `json` 时为 JSON；`log_level` 设置级别。除静态文件和 `/healthz` 之外，每个请求都有一行 `msg=request`，其中有 `ip=<浏览器的地址>`（连接的地址，或 `trusted_proxies` 中的代理所报告的浏览器地址）、`user=<工号>`（没有登录的访问者是 `guest`；本地管理员用它的名称，登录被关闭时是 `local-admin`）、`method`、`path`、`status` 和 `ms`。用户只从会话 Cookie 读取，不询问用户目录，让某人登录的那个请求（`POST /login`、`POST /auth/sso/poll`）会按登录成功的账号记录。`request failed` 和 `panic while handling a request` 带有同样的 `ip`、`user`、`method` 和 `path`，以及 `id`，即错误页面显示的参考编号（Web 日志中是 `ref=`）；发生 panic 的请求也有它的 `request` 行，其中 `status=500`。`job finished` 和 `job finished with an error` 带有 `by=<谁请求了这个任务>`（自动备份是 `scheduler`）。按位置读取这些行的任何东西，都要预料到 `ip` 和 `user` 在 `msg=request` 之后排在最前面。Redis 中的 Web 日志是另一回事：见 [Web 日志](#web-日志)。
- **监控文件夹。** 管理员在**设置**页添加（本地文件夹；不能与仓库、恢复目录或其他监控文件夹重叠；同时填上它的显示文件夹，见[设置放在哪里](#设置放在哪里)；并说明谁可以看到和使用它，见[文件夹权限](#文件夹权限)）。管理员，或对该文件夹拥有完全控制权的人，可以随时编辑名称、显示文件夹、排除规则、启停开关、计划、保留的版本数和权限。服务器上的文件夹本身，管理员在第一个恢复点出现（任何状态，包括失败的）之前可以更正；之后请停用旧的并添加新的。停用的文件夹保留历史，仍可浏览，但不能被备份。
- **备份。** 对某个文件夹拥有"备份"（或"完全控制"）权限的已登录账号，可以在**备份**页点击**立即备份**。普通备份依据大小和修改时间跳过未变化的文件；**完整重读**会重新读取并哈希每个文件。每个备份源同一时间只运行一个备份。无法读取的文件（被锁定、读取时发生变化、链接、特殊文件）会被记录为"跳过"并写明原因，保留之前的良好版本，备份本身仍以警告完成。
- **自动备份。** 为文件夹勾选"自动备份，间隔 N 分钟"（1 到 43200）。服务每 15 秒查看一次；文件夹从未运行过，或上次运行结束已满 N 分钟（无论结果如何）时到期，而没有发现变化的运行不记录任何内容：没有恢复点、没有任务、没有审计事件。设置计划或重新启用文件夹时，如果上次运行早于间隔，会立即启动备份。到期的文件夹会一次一个、错开时间启动（见[调度器设置](#调度器设置)），所以文件夹可能比它到期的时刻晚一些启动。没有每个文件的版本数上限（下一项）时，对一直在变化的文件夹使用很短的间隔，会让仓库无限增长。见 [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md) 和 [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md)。
- **每个文件保留的版本数。** 每个监控文件夹有一个上限（设置页的"每个文件保留的版本数"，1 到 10000；0 或留空表示全部保留）。每次备份之后，会检查它改动过的文件：当一个文件的版本多于上限时，它较旧的版本会被永久删除，并释放没有版本再需要的仓库内容（只在没有备份运行时进行，所以空闲空间的变化可能要过一阵子才看得到）。失去版本的恢复点会在备份页和时间机器页上被标记为**不完整**：它不再显示其版本已被删除的文件，所以从它恢复可能不完整，校验它时会检查对象但不检查清单。对已有历史的文件夹设置上限或调低上限，会为整个文件夹排入一个**版本清理**任务；调低时会先要求勾选（"我知道这些版本会被永久删除"），旁边会给出将被删除的版本数和文件数。当备份之后立即进行的删除无法运行时（因为该文件夹的恢复或校验排队或正在运行，或者它失败了），也会排入一个清理任务：否则它会被遗忘，因为下一次备份只查看它自己改动过的文件。排队的清理不占用 worker；它在该文件夹没有备份、恢复或校验运行时开始，运行期间该文件夹的备份在队列中等待。清理运行期间，该文件夹的恢复和校验会被拒绝，更改它的上限也会被拒绝；在此之前已排队的恢复或校验会等到它结束。审计日志记录**已删除旧版本**。见 [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md)。
- **校验。** 对恢复点运行校验任务，会对照清单摘要检查目录库，并检查每个被引用的对象都存在且大小正确；"深度"校验还会重新哈希内容。复制或恢复仓库之后请运行一次。
- **恢复。**
  1. 登录后打开**时间机器**，选择备份源和恢复点，然后勾选文件或文件夹并选择**恢复所选项…**，或选择**恢复此文件夹…** / **恢复全部内容…**。单个文件的版本页面有**恢复…**。未登录的访问者看到的是一个**登录后可恢复文件**的链接。能浏览某个文件夹的人，就可以从它恢复。
  2. 查看预览：数量、大小、冲突、受阻项目（内容缺失、不安全的名称、从未备份成功）以及计划的变更。
  3. 选择目标文件夹名称（`TIMEVAULT_RESTORE_ROOT` 之下的文件夹，不存在时会创建）、冲突策略（跳过、改名、覆盖），以及在接受只恢复所选内容的一部分时勾选的部分恢复选项。覆盖需要额外确认。
  4. 在预览后 15 分钟内点击**开始恢复**（令牌会过期，并与你的用户和精确的计划绑定；任何更改都需要重新预览）。在任务页面查看进度。在它结束之前，不要向同一个文件夹再启动另一个恢复。请自行把结果从目标位置复制到最终位置：预览和任务页面用显示文件夹（`<restore_display_root>\<destination>`）来称呼它，你可以通过网络在那里找到它；管理员还会看到 `<TIMEVAULT_RESTORE_ROOT>\<destination>`。恢复绝不会写入备份源。
- **审计。** **审计**页（仅限账号）列出登录和登录失败（账号名按输入的内容记录，截断到 64 个字符；单点登录会显示登录方式和 Windows 用户）、被拒绝的请求、监控文件夹的变更（每个发生变化的字段的旧值和新值，包括权限列表）、备份、校验、恢复预览与结果、旧版本的删除、取消操作，以及程序更新的各个步骤（**程序更新**，操作者为 `updater`；见[发布与自动更新](#发布与自动更新)）。管理员能看到全部；其他人看到的是他们可以浏览的文件夹的事件，以及不属于任何文件夹的事件（登录等）。**耗时**列显示备份、校验、恢复或清理运行了多久，对备份还显示它所复制数据的速度：大文件夹的第一次备份能说明磁盘和仓库有多快（之后的备份复制得很少，所以它的速度说明不了什么）。应用程序没有修改或删除这些记录的途径，并且数据库触发器会拒绝对它们的 UPDATE 和 DELETE 语句。没有发现变化的自动运行不留下记录。
- **备份 TimeVault 自身。** 需要同时备份 PostgreSQL 数据库（例如用 `pg_dump`）和整个仓库文件夹。仓库只会增长，所以较旧的数据库副本配上较新的仓库是一致的，而较新的数据库配上较旧的仓库会报告对象缺失（校验会列出，恢复会被阻止）。无论恢复了哪一个，之后都请运行一次深度校验。

## 故障排查

| 现象 | 原因与处理 |
|---|---|
| 服务以"is not a loopback address"退出 | 在反向代理之后使用 `127.0.0.1`，或在明确知晓风险的情况下设置 `TIMEVAULT_ALLOW_NON_LOOPBACK=true`。 |
| 服务以数据库结构相关的提示退出 | 运行 `timevault-server migrate`，或设置 `TIMEVAULT_AUTO_MIGRATE=true`。 |
| 服务以"no way to sign in"退出 | 设置 `TIMEVAULT_REDIS_URL`（公司账号）和/或 `TIMEVAULT_ADMIN_PASSWORD_HASH`（本地管理员；用 `timevault-server hash-password` 生成该值）。 |
| 服务以与 `.env` 有关的错误退出 | 提示会指出文件和行号，绝不会显示该行内容。`TIMEVAULT_ENV_FILE` 必须指向一个存在的文件。 |
| 站点设置页拒绝 `server_host` 或 `restore_display_root`（或服务以与它们有关的错误退出） | 提示会指出设置名和问题所在，绝不会显示它的值。主机名只能包含字母、数字、`-`、`_` 和 `.`；显示根路径必须形如 `\\server\share\folder`，共享名不能以 `$` 结尾，并且必须位于 `server_host` 上。 |
| 添加或编辑监控文件夹被拒绝，提示"请填写显示文件夹"或"其他服务器上的文件夹不能在这里监控" | 设置了 `server_host`，所以显示文件夹是必填的，并且它的主机必须是那台服务器（提示里会点出两个主机名）。在该设置出现之前添加的文件夹没有显示文件夹；请在设置页给每个文件夹填上一个。 |
| 服务以"another TimeVault service is already running against this database"退出 | 另一个 `timevault-server` 正在使用同一个数据库（schema）。请先停止那一个；它的锁会在其进程结束时释放。 |
| 服务以"not a TimeVault repository"退出 | `TIMEVAULT_REPOSITORY_PATH` 指向的非空文件夹中没有 `timevault-repository.json`。请使用空文件夹。 |
| 脚本找不到 `go` | 把 Go 加入 `PATH`，或把 `TIMEVAULT_GO_ROOT=<包含 bin\go.exe 的文件夹>` 这一行写入 `.env`。只在某一个终端里设置该变量，仅对那个终端有效。 |
| 重新构建之后，页脚、设置页或 `timevault-server version` 显示的仍是旧版本号 | 要么构建命令里还带着 `-X main.version=<旧版本号>`（旧命令的副本），于是程序是新代码、版本号却是旧的：请用[构建可执行文件](#构建可执行文件)中不带 `-X` 的命令构建。要么服务仍在运行旧文件：Windows 不允许替换正在运行的程序文件，所以请先停止服务，复制新文件，再启动它，并对真正启动的那个文件运行 `version`。用 `-trimpath` 构建时不会记录 `-ldflags`，所以 `go version -m` 看不到这个参数；请以 `version` 为准。 |
| 服务监听的端口（或其他设置）与 `.env` 中写的不一致 | 进程环境变量中的 `TIMEVAULT_*` 变量优先于 `.env`。启动日志中有一条警告，会点名它覆盖的每一项设置（端口对应 `TIMEVAULT_LISTEN_ADDR`）。常见原因是旧版 `dev-run.ps1` 在终端里留下了变量：请用 `scripts\dev-run.ps1` 启动服务，或用 `. .\scripts\dev-env.ps1` 加载会话，它们都会移除这类变量。如果它又出现了，说明它设在你的 Windows 用户或系统环境变量里。 |
| `dev-db.ps1` 找不到 PostgreSQL | 设置 `TIMEVAULT_PG_BIN` 或传入 `-PgBin`。 |
| 登录返回 429 | 尝试次数过多：10 分钟内每个地址 5 次，或每个账号名 10 次失败（`Retry-After` 说明要等多久）。放在反向代理之后时，所有访问者共用代理的地址，除非 `trusted_proxies` 列出了该代理。请等待，或重启服务（计数保存在内存中）。 |
| 登录提示无法连接用户账号服务（503） | Redis 没有应答。浏览、已有的会话和本地管理员仍可使用。请检查 `TIMEVAULT_REDIS_URL` 并运行 `timevault-server check-redis-users`；**状态**页会把该目录显示为"降级"。这期间的尝试计入地址，不会算到账号头上。 |
| 公司账号无法登录 | 密码错误、账号未启用、没有保存密码，或登录名匹配到多个账号（工号、姓名或邮箱；服务日志会说明这一点，但不会点名任何人）。请运行 `check-redis-users`：不可读的记录、没有密码的记录和出现在多个键上的工号都会在那里计数。可以试试工号，它是歧义最小的。 |
| `check-redis-users` 报告有不可读的记录 | 有解码器无法读取的哈希（缺少字段或字段类型出乎意料）。报告会给出该键在所有按名称排序的键中的位置，可据此用 `redis-cli` 找到它。这样的记录无法登录。 |
| 没有"使用 Windows 账号登录（SSO）"按钮 | `sso_host` 为空，或者没有 Redis 用户目录（`TIMEVAULT_REDIS_URL`）。启动日志和站点设置页会显示 `sso_host`。 |
| 单点登录提示"无法把这次登录与当前浏览器对应起来" | 单点登录服务记录的地址不是 TimeVault 看到的该浏览器的地址。在反向代理之后，请把 `trusted_proxies` 设为该代理（代理必须发送 `X-Forwarded-For`），或把 `sso_require_same_ip` 设为 `false`。审计页会以原因 `address_mismatch` 显示这次失败（登录过程中浏览器的地址变了则为 `browser_address_changed`），并附上 TimeVault 看到的内容：`started_from`（它当作浏览器的地址）、`sso_address`、`connection`、`trusted_proxy` 和 `forwarded`。如果 `started_from` 是某台服务器的地址，说明紧挨着 TimeVault 的代理前面还有另一层代理，把浏览器隐藏了：见[两层代理](#两层代理)。如果 `trusted_proxy` 是 `false` 且设置了 `forwarded`，说明连接过来的代理不在 `trusted_proxies` 中。 |
| 打开 `/tv/` 后落到了另一个应用（例如门户页），或者页面没有样式、链接都离开了 `/tv/` | TimeVault 不知道自己的前缀。请使用 0.1.5 或更高版本，把站点设置 `base_path` 设为 `/tv` 并重启；启动日志必须写出"serving under a path prefix"。没有它时，每个重定向和链接都从主机根路径开始，在 `/tv/` 之外。 |
| 在 `/tv/` 之后，审计日志里所有人都是 `127.0.0.1`，或者单点登录提示无法与当前浏览器对应 | 代理没有传递浏览器的地址，或者 TimeVault 不信任该代理。请在代理配置块中设置 `proxy_set_header X-Forwarded-For $remote_addr;`，并把站点设置 `trusted_proxies` 设为 `127.0.0.1`（代理连接过来所用的地址）。 |
| 登录之后，某个地址里没有 `/tv` 的页面把你显示为未登录 | 设置了前缀之后，Cookie 被限制在该前缀之内。请使用 `/tv/` 之下的地址；页面上的每个链接和重定向本来就都指向那里。 |
| 单点登录提示浏览器拦截了窗口 | 请允许 TimeVault 站点的弹出窗口。窗口只能由点击本身打开。 |
| 单点登录一直等待，然后提示未能在规定时间内完成 | 单点登录服务没有记录结果。请检查 `sso_host` 是否是人们实际访问的服务，以及它是否把 `sso_login_<token>` 写入 `TIMEVAULT_REDIS_URL` 所指的 Redis（相同的数据库编号）。 |
| 单点登录提示"你的 Windows 账号在这里没有可用的账号" | Windows 给出的工号在 Redis 中没有可用的账号（审计页会显示 `no_active_account` 和该工号）。请用 `check-redis-users` 检查该账号。 |
| 设置页提示"只有管理员可以添加监控文件夹" | 就是这样：添加文件夹只有管理员能做（该账号在 Redis 中的 `Type` 为 `admin`；角色变化在一分钟内生效）。修改某个文件夹的设置需要对它拥有**完全控制**权限，管理员可以在设置页授予。 |
| 某人看不到本该看到的监控文件夹，或者看到的是"未找到" | 没有被授权给任何人的文件夹，谁也看不到。打开该文件夹在设置页的**编辑**表单（以管理员身份，或对它拥有完全控制权），检查这个人的工号是否在某个列表中，或者是否勾选了**所有已登录的人**（对未登录的访问者则是**访客**）。未登录的访问者只有**访客**所给的权限。见[文件夹权限](#文件夹权限)。 |
| 备份（或保存文件夹的设置）时提示"你没有权限执行此操作" | 该账号能看到这个文件夹，但级别太低：备份、校验或停止任务需要**备份**，修改设置需要**完全控制**。审计页会把这次尝试显示为**访问被拒绝**，并带上文件夹和 `needs`。服务器上的文件夹只有管理员能修改。 |
| 文件夹表单的权限部分提示用户目录目前无法列出账号，或者拒绝添加某个人 | Redis 没有应答（或没有设置 `TIMEVAULT_REDIS_URL`）。已经有权限的人会显示出来并可以保留；在目录恢复应答之前，不能添加其他人。请查看**状态**页。 |
| 点击立即备份提示监控文件夹已停用（409） | 需要管理员在设置页重新启用它。 |
| 迁移文件夹时返回 409，或提示它已有备份 | 该文件夹的备份正在运行（请在它结束后再试），或者该文件夹已有某种状态的恢复点，不能再更改：请停用它并添加新的文件夹。 |
| 自动备份没有运行 | 文件夹已停用或没有设置计划；或者它上次运行结束距今不到一个间隔（备份页和设置页会显示下次时间）；或者间隔状态暂不允许（自动备份一次运行一个，带有间隔和休息；见[调度器设置](#调度器设置)）；或者开启了变更探测（`scheduler_probe`）且 NTFS 索引说没有变化；或者服务没有运行。调度器每 15 秒查看一次。 |
| 我正在查看的任务显示"未找到" | 没有发现变化的自动备份不会留下任务。 |
| 文件浏览中文件夹的大小显示"-" | NTFS 索引已关闭（`search_index`）、还没有就绪，或无法读取该卷（**状态**页会说明原因：没有管理员权限、该卷正在建立索引、没有变更日志、不是 NTFS），或者该文件夹不在本地盘符上。文件不受影响。时间机器和版本页面从不显示文件夹大小。 |
| 更改版本数上限被拒绝，提示"这个上限会永久删除 … 个较旧版本"（409 `prune_not_confirmed`） | 调低上限会删除版本。请勾选确认框后再次保存（API：`confirm_prune: true`）。 |
| 恢复或校验被拒绝，提示该文件夹的旧版本正在被删除（409 `prune_running`） | 该文件夹的版本清理正在运行；更改它的版本数上限也会因此被拒绝。请一分钟后再试；备份页会列出该任务。只是排在该文件夹的备份或校验后面的清理，不会拒绝任何操作。 |
| 删除版本之后仓库没有变小 | 只有在没有备份运行时才会释放内容，而且每次清扫的时间有限；调度器会重复进行。总有备份在运行的服务器会推迟它。 |
| "Restore is not enabled" | `TIMEVAULT_RESTORE_ROOT` 为空。 |
| 恢复预览被判定为过期或失效 | 重新预览；令牌有效期 15 分钟，并与精确的计划绑定。在没有设置 `TIMEVAULT_SESSION_KEY` 的情况下重启，密钥也会变化。 |
| 文件被列为跳过（被锁定） | 文件被其他程序打开，或在读取时发生变化。请稍后再次备份；MVP 没有 VSS。 |
| 恢复报告对象缺失或损坏 | 仓库丢失或损坏了文件。请运行深度校验，从备份恢复仓库，或选择其他恢复点。不会从损坏的对象恢复任何内容。 |
| 中断的恢复留下了 `.tvpart-*` 文件 | 这是崩溃的恢复在目标文件夹中留下的临时文件，可以删除。 |
| 搜索显示"不可用" | 见[搜索索引](#搜索索引)。历史和恢复仍可使用。请查看**状态**页：服务没有管理员权限、该卷的索引仍在建立、该卷无法建立索引（不是 NTFS、没有变更日志）、监控文件夹在网络共享上，或者站点设置 `search_index` 为 `off`。 |
| 任务一直显示"failed: interrupted by a service restart" | 任务运行时服务被停止。请重新启动该任务；未完成的恢复点已经被清理。更新从不会这样做：它会等正在运行的任务结束。 |
| 设置页显示**等待正在运行的任务**，页眉下面的提示一直在 | 有任务正在运行（见备份页），更新在无限期地等它。新任务在队列中等待，直到服务重启。如果重启不能等，请手工停止服务；服务再次启动时，该任务会被恢复为失败，更新则在那次启动之后的第一次查看（10 秒后）重新开始。 |
| **程序更新**显示**出现问题**，管理员看到"read the manifest: …" | 服务读不到 `update_url` 处的清单：文件还不在那里、共享或 Web 地址有误或服务器访问不到、运行服务的账号不能读它，或者 `http(s)` 地址返回了 `200` 以外的应答（`404`、跳转到登录页、`202`）。请在服务器上打开这个地址试试。 |
| **出现问题**："the program is not the one the manifest describes" | `timevault.exe` 的大小或 sha256 与 `timevault.json` 中的不一致：发布还在复制中（每次查看都会重试，会自行恢复），或者这两个文件来自不同的构建，或者 `package_url` 指向了另一个文件。请重新复制这两个文件（清单最后复制），或再运行一次 `scripts\release.ps1`。 |
| **出现问题**："the program of the release says it is version X, but the manifest says Y" | 清单中的 `latest_version` 与程序里的版本不一致（手工改过清单，或者构建所用源码中的 `var version` 没有提高）。在清单改变之前不会再试。请运行 `scripts\release.ps1`，它会从程序中取版本号。 |
| **出现问题**："the new program does not accept the settings of this service: …" | 新版本的 `timevault.exe check-config` 拒绝了 `.env`（新版本对某个设置的检查更严格，或要求了某个设置）。请阅读原因并修改 `.env`；下次查看时会再试。这期间服务继续使用旧程序运行。 |
| **出现问题**："the new program could not be put in place: … Access is denied" | 运行服务的账号不能在程序所在的文件夹中重命名或创建文件，或者杀毒软件占用了该文件。请给该账号对该文件夹的**修改**权限（启动日志会对此发出警告）。服务继续使用旧程序运行，在清单改变或服务重启之前不会再尝试这个发布。 |
| 更新之后，控制台窗口或计划任务显示程序已结束，退出码为 75 | `update_restart` 为 `exit`，而没有任何东西再次启动程序。请使用 `supervise`（默认值），或者让服务管理器在程序结束时重新启动它（NSSM：`AppExit 75 Restart`）。 |
| 更新之后，旧程序文件 `timevault.old-<版本>.exe` 无法删除 | 使用 `supervise` 时，启动器仍在从它运行（见[发布与自动更新](#发布与自动更新)）。启动器结束之后，它会在以后的某次更新中被删除；在服务停止并重新启动之后，也可以手工删除。 |
| 手工回退之后，服务又自己更新回了较新的版本 | 发布文件夹里仍然是较新的清单。回退之前请先把较旧的发布放到那里（或者清空 `update_url`）。 |
| 账号头像没有显示 | 启动日志里有 `avatar_url`：为空表示没有头像，原因是 `avatar_url` 为 `off`，或者它没有设置而 `server_host` 也没有设置。在浏览器中打开一张头像（`http://<服务器主机>/api/static/hr/<工号>.webp`）：它必须显示这张图片。480×320 像素的图片（占位图，以 `202` 回答）和 `404` 会有意保留默认头像。如果图片能打开但 TimeVault 不显示：启动日志写着"cannot be named in the content security policy"（主机名带下划线，而且外面没有两个标签的域：请用同一个名称打开站点，或者在头像地址中写服务器的 IP 地址），或者站点通过 `https` 提供而地址是 `http`。用与头像所在主机不同的名称打开站点没有问题，因为策略列出的是地址中的主机。 |
| 配置了 Redis，但 `web_log` 中没有任何行 | 按这个顺序检查。启动日志的 `starting TimeVault` 一行中 `web_log=false` 表示它是关闭的：站点设置 `web_log` 为 `off`，或者为 `auto` 而没有 `TIMEVAULT_REDIS_URL`。开启时，启动日志中有 `web log is on` 一行。警告 `web log: the lines cannot be written to Redis`（带有原因，URL 的密码被遮盖）表示 Redis 没有应答，或者它的账号不被允许对该列表执行 `RPUSH`；服务继续运行，写入方会在 30 秒后再试，之后间隔更久，最长 5 分钟。请确认你查看的是服务所用的列表和数据库编号（`web_log_key`，以及 `TIMEVAULT_REDIS_URL` 中的编号）。静态文件、`/healthz`、重定向和 API 调用不是页面行，而调试行（任务的轮询、自动重新加载、`scheduler.skipped`）需要 `log_level` 为 `debug`。见 [Web 日志](#web-日志)。 |
| 站点设置页拒绝 `web_log`、`web_log_key` 或 `web_log_host`（或服务以与它们有关的错误退出） | 提示会点名该设置。`web_log` 必须是 `on`、`off` 或 `auto`，`on` 需要 `TIMEVAULT_REDIS_URL`。键只能包含 ASCII 字母、数字和 `_ . : -`（最多 128 个字符），并且无论大小写，都不能以 `TIMEVAULT_REDIS_KEY_PREFIX` 或 `sso_login_` 开头；当 `web_log` 本身就以该前缀开头时（例如 `web_`），请换一个键，或者关闭 Web 日志。主机名只能包含 ASCII 字母、数字和 `. - _`（最多 63 个字符）。 |
| 站点设置页拒绝 `base_path`，提示不能以 `/help` 开头 | `help` 是应用自己的页面（用户手册），与 `explorer` 或 `settings` 一样。请换一个前缀。 |
