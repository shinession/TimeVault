# English

## Overview

How to set up, configure, run, operate and troubleshoot TimeVault (version 0.1.3). It describes what exists today; the service is a single Go program, `timevault-server`, that needs PostgreSQL and a repository folder, and, for company sign-in, the Redis user directory of another application. There is no installer and no Windows service wrapper yet.

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

Build: `go build -o timevault-server.exe ./cmd/timevault-server`. The web templates and assets are embedded in the executable, and so are the migrations.

## Where settings live

| What | Where | Changed by | Takes effect |
|---|---|---|---|
| Deployment: database URL, Redis URL, listen address, backup folder (`TIMEVAULT_REPOSITORY_PATH`), restore folder (`TIMEVAULT_RESTORE_ROOT`), this server's host name (`TIMEVAULT_SERVER_HOST`), the restore folder's display path (`TIMEVAULT_RESTORE_DISPLAY_ROOT`), session key, search, workers, logging | The `.env` file (a process environment variable overrides it) | Whoever installs the service, by editing the file | After a restart |
| Monitored folders: name, display folder, folder on the server, exclude patterns, on or off, automatic schedule | The database (table `sources`) | Administrators, on the **Settings** page or with `PATCH /api/v1/sources/{id}` | At once |
| Accounts and roles | Redis (`app_user_*`), written by another application | That application | Within a minute for a session that is already signed in |

- The `.env` file is the one named by `TIMEVAULT_ENV_FILE` (it must exist), otherwise `.env` in the working directory, otherwise `.env` next to `timevault-server.exe`. The first one found is used; files are not merged. In development it is the `.env` in the repository folder, and it is the only settings file: the dev scripts read and write it, and `dev-run.ps1` points the service at it.
- **A `TIMEVAULT_*` variable in the process environment wins over the same key in the file**, and nothing tells you unless you look: a variable left in a terminal makes the service ignore what you wrote in `.env` (typically a port that does not change). The service therefore names every setting that the environment overrides in a warning when it starts (`check-redis-users` prints them too), and the dev scripts remove such variables from the terminal. If the warning keeps coming back, the variable is set in your Windows user or system environment.
- Syntax: one `KEY=VALUE` per line. Blank lines and lines that start with `#` are ignored, a leading `export ` is allowed, and a UTF-8 byte order mark and Windows line ends are accepted. The value is taken literally up to the end of the line, so a path with `\` or a password with `#` needs no escaping (and a trailing ` # comment` would become part of the value); one pair of matching single or double quotes around the value is removed; the last assignment of a key wins. An error names the line number and never shows the line.
- The file holds secrets (the database and Redis passwords, the session key). `/.env` is in `.gitignore`; keep the file readable only by the account that runs the service, and never commit a filled-in copy. [configs/timevault.example.env](../configs/timevault.example.env) is the template.
- The **Settings** page (any signed-in account may open it) shows the monitored folders, the backup storage and the restore folder with the free space of the repository, and the running configuration: version, this server's host name, listen address, database and Redis as `host:port/name`, the local administrator's name, the search server. Secrets are never shown. Only administrators see the forms that change the monitored folders, and only administrators see the folders on the server's disk (the real folder of each monitored folder, the backup folder, the real restore folder and the settings file); everybody else sees the display folders.
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
| `TIMEVAULT_EVERYTHING_URL` | empty | Base URL of the Everything HTTP server, for example `http://127.0.0.1:<port>/`. Empty disables search. |
| `TIMEVAULT_EVERYTHING_USERNAME`, `TIMEVAULT_EVERYTHING_PASSWORD` | empty | Optional HTTP Basic credentials for Everything. The password is a secret. |
| `TIMEVAULT_EVERYTHING_TIMEOUT` | `5s` | Per-search timeout, `100ms` to `30s`. |
| `TIMEVAULT_JOB_WORKERS` | `2` | Jobs that run at the same time (1 to 16). The database connection pool is sized to match (2 × workers + 8, at least 16 connections); PostgreSQL's `max_connections` must allow that. |
| `TIMEVAULT_HASH_WORKERS` | smaller of 4 and the CPU count | Files hashed and copied in parallel inside one backup (1 to 32). |
| `TIMEVAULT_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `TIMEVAULT_LOG_FORMAT` | `text` | `text` or `json`. |

Variables used only by the helper scripts and tests, kept in the same `.env` (the service ignores them): `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN`, `TIMEVAULT_EVERYTHING_EXE`, `TIMEVAULT_TEST_DATABASE_URL` and `TIMEVAULT_TEST_EVERYTHING_*` (see [Tests](#tests)). [configs/timevault.example.env](../configs/timevault.example.env) is a template with placeholders.

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

`go test ./...` runs everything. Tests that need PostgreSQL are skipped unless `TIMEVAULT_TEST_DATABASE_URL` points at a database; each test creates a private schema named `tvtest_<random>` and drops it, so use a separate test database such as `timevault_test`, never the working database. `dev-db.ps1` writes the URL for the private cluster into `.env`; if `.env` points at your own PostgreSQL server instead, create the test database there first (`CREATE DATABASE timevault_test OWNER timevault;`) and set `TIMEVAULT_TEST_DATABASE_URL` in `.env`; the role needs no other rights. Tests that exercise junctions and Windows file locking run only on Windows. Everything live tests are described in [Everything search](#everything-search). The Redis tests need no Redis: they use an in-process fake with pickle data written by Python.

```powershell
. .\scripts\dev-env.ps1      # loads TIMEVAULT_TEST_DATABASE_URL (and the other TIMEVAULT_TEST_* settings) from .env
go test ./... -count=1
```

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

## Operating

- **Who may do what.** Anyone can browse (Explorer, Time Machine, file versions, the state of the Backups page and of backup jobs) without signing in. Everything else needs an active account: backing up, verifying, cancelling a job, restoring, the restore jobs, why a job failed and its log, who asked for it, the audit log, the status page, the settings page. Only an `admin` account (Redis `Type` is `admin`, or the local administrator) can add, edit, switch off and schedule monitored folders, and only an administrator sees the folders on the server's disk; everybody else sees display folders. See [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md), [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md) and [api.md](./api.md#who-may-call-what).
- **Browsing long folders.** The Explorer and the Time Machine list a folder in pages (100 items by default; 50, 100, 200 or 500 can be chosen, with the count and page links above and below). The **Type** column shows the extension in capitals ("Folder" for folders, "File" for a file without an extension); the **Type** drop-down above the list keeps only one kind (all folders, all files, a file without an extension, or one extension, each with its count). Click **Name**, **Type**, **Size** or **Modified** to sort the whole folder, and click again to reverse it; folders always come first. The choice is in the address, so a link keeps it. **Select all** in the Time Machine selects the items on the current page, and a restore can take up to 500 paths. On a long page two round buttons at the bottom right jump to the top and to the bottom. A folder of 10,000 files opens in about 0.1 s and one of 50,000 in about 0.5 s (measured on the development machine, with all files in one folder); several hundred thousand entries in one folder take seconds, because every page reads the whole folder.
- **Health.** `GET /healthz` returns `ok` without signing in and only proves the process is up. The **Status** page (accounts only) checks the database, the repository (a write test and free space; "degraded" below 5% free), the search engine, the Redis user directory ("degraded" when it does not answer: browsing, existing sessions and the local administrator still work), the restore folder and the job queue, and lists VSS and USN as "disabled". Without a session only `/healthz`, the sign-in page, the language switch, the static assets and the browsing pages work.
- **Monitored folders.** An administrator adds them on the **Settings** page (a local folder; it must not overlap the repository, the restore folder or another monitored folder; give it its display folder too, see [Where settings live](#where-settings-live)) and can edit the name, the display folder, the exclude patterns, the on/off switch and the schedule at any time. The folder on the server itself can be corrected until the first recovery point exists (in any state, even a failed one); after that, switch the old one off and add the new one. A switched-off folder keeps its history and is still browsable; it cannot be backed up.
- **Backups.** Any signed-in account can press **Back up now** in **Backups**. A normal backup trusts size and modification time to skip unchanged files; **Full re-read** reads and hashes every file again. Only one backup per source runs at a time. A backup that cannot read a file (locked, changed while reading, link, special file) records it as skipped with the reason, keeps the previous good version, and still completes with a warning.
- **Automatic backups.** Tick "Back up automatically every N minutes" for a folder (1 to 43200). The service looks every 15 seconds; a folder is due when it never ran or its last run ended N minutes ago, whatever the outcome, and a run that finds nothing changed records nothing: no recovery point, no job, no audit event. Setting a schedule, or switching a folder on again, starts a backup at once when the last run is older than the interval. There is no retention yet, so a short interval on a folder that keeps changing grows the repository without bound. See [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md).
- **Verify.** The verify job on a recovery point checks the catalog against the manifest digest and that every referenced object exists with the right size; "deep" also re-hashes the content. Run it after copying or restoring the repository.
- **Restore.**
  1. Sign in, open **Time Machine**, choose a source and a recovery point, then tick files or folders and choose **Restore selected…**, or choose **Restore this folder…** / **Restore everything…**. The version page of a single file has **Restore…**. Visitors who are not signed in see a **Sign in to restore files** link instead.
  2. Review the preview: counts, size, conflicts, blocked items (missing content, unsafe names, never captured) and the planned changes.
  3. Choose the destination folder name (a folder below `TIMEVAULT_RESTORE_ROOT`, created if it does not exist), the conflict policy (skip, rename, overwrite) and, if you accept restoring only part of the selection, the partial option. Overwriting needs an extra confirmation.
  4. **Start restore** within 15 minutes of the preview (the token expires and is bound to your user and the exact plan; any change needs a new preview). Follow the job page. Do not start another restore into the same folder until it has finished. Copy the result from the destination to its final place yourself: the preview and the job page name it by its display folder (`<TIMEVAULT_RESTORE_DISPLAY_ROOT>\<destination>`), which is where you find it over the network; administrators also see `<TIMEVAULT_RESTORE_ROOT>\<destination>`. Restores never write into a source.
- **Audit.** The **Audit** page (accounts only) lists sign-ins and failed sign-ins (with the account name as it was typed, cut to 64 characters), refused requests, changes to monitored folders (the old and new value of every field that changed), backups, verifies, restore previews and results, and cancellations. The application has no way to change or delete these records, and a database trigger rejects UPDATE and DELETE statements on them. Automatic runs that find nothing changed leave no record.
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
| The service listens on another port (or uses another setting) than the one written in `.env` | A `TIMEVAULT_*` variable in the process environment beats `.env`. The start-up log has a warning that names every setting it overrides (`TIMEVAULT_LISTEN_ADDR`, for a port). The usual cause is a variable left in the terminal by an older `dev-run.ps1`: start the service with `scripts\dev-run.ps1`, or load the session with `. .\scripts\dev-env.ps1`, which remove such variables. If it comes back, it is set in your Windows user or system environment. |
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
| Search says "not configured" or "unavailable" | See [Everything search](#everything-search). History and restore keep working. "Not configured" means `TIMEVAULT_EVERYTHING_URL` is empty; "unavailable" means the Everything it names does not answer (its HTTP server is switched off, or the port, user name or password differ). |
| A job stays "failed: interrupted by a service restart" | The service was stopped while the job ran. Start the job again; unfinished recovery points were already cleaned up. |

---

# 中文

## 概述

介绍如何安装、配置、运行、运维和排查 TimeVault（0.1.3 版）。它描述的是目前实际存在的内容：服务是一个 Go 程序 `timevault-server`，需要 PostgreSQL 和一个仓库文件夹；公司账号登录还需要另一个应用的 Redis 用户目录。目前没有安装程序，也没有 Windows 服务封装。

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

构建：`go build -o timevault-server.exe ./cmd/timevault-server`。Web 模板、静态资源和迁移脚本都已嵌入可执行文件。

## 设置放在哪里

| 内容 | 位置 | 由谁修改 | 何时生效 |
|---|---|---|---|
| 部署设置：数据库 URL、Redis URL、监听地址、备份文件夹（`TIMEVAULT_REPOSITORY_PATH`）、恢复文件夹（`TIMEVAULT_RESTORE_ROOT`）、本服务器的主机名（`TIMEVAULT_SERVER_HOST`）、恢复文件夹的显示路径（`TIMEVAULT_RESTORE_DISPLAY_ROOT`）、会话密钥、搜索、worker、日志 | `.env` 文件（进程环境变量会覆盖它） | 安装服务的人，通过编辑文件 | 重启之后 |
| 监控文件夹：名称、显示文件夹、服务器上的文件夹、排除规则、启用或停用、自动备份计划 | 数据库（表 `sources`） | 管理员，在**设置**页或通过 `PATCH /api/v1/sources/{id}` | 立即 |
| 账号和角色 | Redis（`app_user_*`），由另一个应用写入 | 那个应用 | 对已经登录的会话，在一分钟之内 |

- `.env` 文件是 `TIMEVAULT_ENV_FILE` 所指的文件（它必须存在），否则是工作目录下的 `.env`，否则是 `timevault-server.exe` 旁边的 `.env`。使用找到的第一个；不会合并多个文件。开发时它就是仓库文件夹中的 `.env`，也是唯一的设置文件：开发脚本读写它，`dev-run.ps1` 让服务使用它。
- **进程环境变量中的 `TIMEVAULT_*` 变量优先于文件中的同名键**，而且除非你去查，否则没有任何提示：终端里遗留的某个变量会让服务无视你在 `.env` 里写的内容（典型表现是端口改不动）。因此服务在启动时会用一条警告点名所有被环境变量覆盖的设置（`check-redis-users` 也会打印它们），开发脚本则会从终端里移除这类变量。如果这条警告反复出现，说明该变量设在你的 Windows 用户或系统环境变量里。
- 语法：每行一个 `KEY=VALUE`。空行和以 `#` 开头的行会被忽略，允许行首的 `export `，接受 UTF-8 字节顺序标记和 Windows 行尾。值按字面一直取到行尾，所以带 `\` 的路径或带 `#` 的密码无需转义（行尾的 ` # 注释` 会成为值的一部分）；值两侧成对的单引号或双引号会被去掉；同一个键以最后一次赋值为准。出错时只指出行号，绝不显示该行内容。
- 该文件包含机密信息（数据库和 Redis 密码、会话密钥）。`/.env` 已在 `.gitignore` 中；请让它只能被运行服务的账户读取，并且绝不要提交填好的副本。[configs/timevault.example.env](../configs/timevault.example.env) 是模板。
- **设置**页（任何已登录账号都可以打开）显示监控文件夹、备份存储和恢复文件夹（含仓库的剩余空间），以及当前运行的配置：版本、本服务器的主机名、监听地址、数据库和 Redis（显示为 `host:port/name`）、本地管理员的名称、搜索服务器。机密信息绝不显示。只有管理员能看到修改监控文件夹的表单，也只有管理员能看到服务器磁盘上的文件夹（每个监控文件夹的真实文件夹、备份文件夹、真实的恢复文件夹和设置文件）；其他人看到的是显示文件夹。
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
| `TIMEVAULT_EVERYTHING_URL` | 空 | Everything HTTP 服务的基础 URL，例如 `http://127.0.0.1:<port>/`。为空表示禁用搜索。 |
| `TIMEVAULT_EVERYTHING_USERNAME`、`TIMEVAULT_EVERYTHING_PASSWORD` | 空 | Everything 的可选 HTTP Basic 凭据。密码属于机密。 |
| `TIMEVAULT_EVERYTHING_TIMEOUT` | `5s` | 单次搜索的超时，范围 `100ms` 到 `30s`。 |
| `TIMEVAULT_JOB_WORKERS` | `2` | 同时运行的任务数（1 到 16）。数据库连接池的大小与之匹配（2 × worker 数 + 8，至少 16 个连接）；PostgreSQL 的 `max_connections` 必须允许这个数量。 |
| `TIMEVAULT_HASH_WORKERS` | 4 与 CPU 数中较小者 | 一次备份内并行哈希和复制的文件数（1 到 32）。 |
| `TIMEVAULT_LOG_LEVEL` | `info` | `debug`、`info`、`warn` 或 `error`。 |
| `TIMEVAULT_LOG_FORMAT` | `text` | `text` 或 `json`。 |

仅供辅助脚本和测试使用、同样放在这个 `.env` 里的变量（服务会忽略它们）：`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN`、`TIMEVAULT_EVERYTHING_EXE`、`TIMEVAULT_TEST_DATABASE_URL` 和 `TIMEVAULT_TEST_EVERYTHING_*`（见[测试](#测试)）。[configs/timevault.example.env](../configs/timevault.example.env) 是带占位符的模板。

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

`go test ./...` 运行全部测试。需要 PostgreSQL 的测试在 `TIMEVAULT_TEST_DATABASE_URL` 未指向某个数据库时会被跳过；每个测试都会创建名为 `tvtest_<random>` 的私有 schema 并在结束后删除，所以请使用单独的测试数据库，例如 `timevault_test`，不要使用正式使用的数据库。`dev-db.ps1` 会把私有集群的 URL 写入 `.env`；如果 `.env` 指向的是你自己的 PostgreSQL 服务器，请先在那里创建测试数据库（`CREATE DATABASE timevault_test OWNER timevault;`），并在 `.env` 中设置 `TIMEVAULT_TEST_DATABASE_URL`；该角色不需要其他权限。涉及联接点和 Windows 文件锁的测试只在 Windows 上运行。Everything 在线测试见 [Everything 搜索](#everything-搜索)。Redis 测试不需要 Redis：它们使用进程内的模拟服务和 Python 生成的 pickle 数据。

```powershell
. .\scripts\dev-env.ps1      # loads TIMEVAULT_TEST_DATABASE_URL (and the other TIMEVAULT_TEST_* settings) from .env
go test ./... -count=1
```

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

## 运维

- **谁可以做什么。** 任何人无需登录就可以浏览（文件浏览、时间机器、文件版本、备份页和备份任务的状态）。其他一切都需要已启用的账号：备份、校验、取消任务、恢复、恢复任务、任务失败的原因和日志、任务是谁发起的、审计日志、状态页、设置页。只有 `admin` 账号（Redis 中 `Type` 为 `admin`，或本地管理员）才能添加、编辑、停用监控文件夹并设置它们的计划，也只有管理员能看到服务器磁盘上的文件夹；其他人看到的是显示文件夹。见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)、[ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md) 和 [api.md](./api.md#谁可以调用什么)。
- **浏览很长的文件夹。** 文件浏览和时间机器把文件夹分页列出（默认每页 100 项；可选 50、100、200 或 500，列表上方和下方有数量和页码链接）。**类型**列以大写显示扩展名（文件夹显示"文件夹"，没有扩展名的文件显示"文件"）；列表上方的**类型**下拉框只保留一种（所有文件夹、所有文件、没有扩展名的文件，或某一种扩展名，每项都带数量）。点击**名称**、**类型**、**大小**或**修改时间**会对整个文件夹排序，再点一次反向；文件夹始终排在最前面。你的选择写在地址里，所以链接会保留它。时间机器中的**全选**选中当前页的项目，一次恢复最多可带 500 个路径。在较长的页面上，右下角的两个圆形按钮可以跳到顶部和底部。包含 10,000 个文件的文件夹约 0.1 秒打开，包含 50,000 个文件的约 0.5 秒（在开发机器上测得，所有文件都在同一个文件夹里）；单个文件夹中有几十万个条目时需要几秒钟，因为每一页都会读取整个文件夹。
- **健康检查。** `GET /healthz` 无需登录即返回 `ok`，只能证明进程在运行。**状态**页（仅限账号）检查数据库、仓库（写入测试和剩余空间；剩余不足 5% 时为"降级"）、搜索引擎、Redis 用户目录（没有应答时为"降级"：浏览、已有会话和本地管理员仍可使用）、恢复目录和任务队列，并把 VSS 和 USN 列为"已禁用"。没有会话时，只有 `/healthz`、登录页、语言切换、静态资源和浏览页面可以访问。
- **监控文件夹。** 管理员在**设置**页添加（本地文件夹；不能与仓库、恢复目录或其他监控文件夹重叠；同时填上它的显示文件夹，见[设置放在哪里](#设置放在哪里)），并且可以随时编辑名称、显示文件夹、排除规则、启停开关和计划。服务器上的文件夹本身在第一个恢复点出现（任何状态，包括失败的）之前可以更正；之后请停用旧的并添加新的。停用的文件夹保留历史，仍可浏览，但不能被备份。
- **备份。** 任何已登录的账号都可以在**备份**页点击**立即备份**。普通备份依据大小和修改时间跳过未变化的文件；**完整重读**会重新读取并哈希每个文件。每个备份源同一时间只运行一个备份。无法读取的文件（被锁定、读取时发生变化、链接、特殊文件）会被记录为"跳过"并写明原因，保留之前的良好版本，备份本身仍以警告完成。
- **自动备份。** 为文件夹勾选"自动备份，间隔 N 分钟"（1 到 43200）。服务每 15 秒查看一次；文件夹从未运行过，或上次运行结束已满 N 分钟（无论结果如何）时到期，而没有发现变化的运行不记录任何内容：没有恢复点、没有任务、没有审计事件。设置计划或重新启用文件夹时，如果上次运行早于间隔，会立即启动备份。目前没有保留策略，所以对一直在变化的文件夹使用很短的间隔，会让仓库无限增长。见 [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md)。
- **校验。** 对恢复点运行校验任务，会对照清单摘要检查目录库，并检查每个被引用的对象都存在且大小正确；"深度"校验还会重新哈希内容。复制或恢复仓库之后请运行一次。
- **恢复。**
  1. 登录后打开**时间机器**，选择备份源和恢复点，然后勾选文件或文件夹并选择**恢复所选项…**，或选择**恢复此文件夹…** / **恢复全部内容…**。单个文件的版本页面有**恢复…**。未登录的访问者看到的是一个**登录后可恢复文件**的链接。
  2. 查看预览：数量、大小、冲突、受阻项目（内容缺失、不安全的名称、从未备份成功）以及计划的变更。
  3. 选择目标文件夹名称（`TIMEVAULT_RESTORE_ROOT` 之下的文件夹，不存在时会创建）、冲突策略（跳过、改名、覆盖），以及在接受只恢复所选内容的一部分时勾选的部分恢复选项。覆盖需要额外确认。
  4. 在预览后 15 分钟内点击**开始恢复**（令牌会过期，并与你的用户和精确的计划绑定；任何更改都需要重新预览）。在任务页面查看进度。在它结束之前，不要向同一个文件夹再启动另一个恢复。请自行把结果从目标位置复制到最终位置：预览和任务页面用显示文件夹（`<TIMEVAULT_RESTORE_DISPLAY_ROOT>\<destination>`）来称呼它，你可以通过网络在那里找到它；管理员还会看到 `<TIMEVAULT_RESTORE_ROOT>\<destination>`。恢复绝不会写入备份源。
- **审计。** **审计**页（仅限账号）列出登录和登录失败（账号名按输入的内容记录，截断到 64 个字符）、被拒绝的请求、监控文件夹的变更（每个发生变化的字段的旧值和新值）、备份、校验、恢复预览与结果以及取消操作。应用程序没有修改或删除这些记录的途径，并且数据库触发器会拒绝对它们的 UPDATE 和 DELETE 语句。没有发现变化的自动运行不留下记录。
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
| 服务监听的端口（或其他设置）与 `.env` 中写的不一致 | 进程环境变量中的 `TIMEVAULT_*` 变量优先于 `.env`。启动日志中有一条警告，会点名它覆盖的每一项设置（端口对应 `TIMEVAULT_LISTEN_ADDR`）。常见原因是旧版 `dev-run.ps1` 在终端里留下了变量：请用 `scripts\dev-run.ps1` 启动服务，或用 `. .\scripts\dev-env.ps1` 加载会话，它们都会移除这类变量。如果它又出现了，说明它设在你的 Windows 用户或系统环境变量里。 |
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
| 搜索显示"未配置"或"不可用" | 见 [Everything 搜索](#everything-搜索)。历史和恢复仍可使用。"未配置"表示 `TIMEVAULT_EVERYTHING_URL` 为空；"不可用"表示它所指的 Everything 没有应答（它的 HTTP 服务被关闭，或端口、用户名、密码不一致）。 |
| 任务一直显示"failed: interrupted by a service restart" | 任务运行时服务被停止。请重新启动该任务；未完成的恢复点已经被清理。 |
