# English

## Overview

What changed in each version of TimeVault, and what to do when upgrading. The newest comes first.

## 0.1.5

TimeVault can be served under a path of a host name that other applications share, such as `http://prod_eng.co.murata.local/tv/`. Until now it assumed that it is at the root of its address, and behind `/tv/` the first redirect left the prefix and ended on another application. The reasons are in [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md).

### Added

- **`TIMEVAULT_BASE_PATH`** (for example `/tv`; empty by default, which changes nothing). When it is set, every address that the pages, the redirects and the cookies give out carries the prefix: the links, forms, files and icons of the pages, every `Location` header (also those of the API and the redirects of the web server itself), the progress polling of a job, the single sign-on, and the cookies `tv_session`, `tv_lang` and `tv_flash`, which are limited to the prefix so that the other applications of the host name do not receive them. A request is accepted with or without the prefix, so the proxy may keep it (recommended) or take it off. The bare prefix (`/tv`) is sent to `/tv/`. The start-up log says `serving under a path prefix`; the Settings page shows the **Address prefix** and `GET /api/v1/settings` has `base_path`.
- A tested nginx block, and what to check, in [runbook.md](./runbook.md#behind-a-reverse-proxy-under-a-path).

### Changed

- The page to return to after a sign-in, a language switch or a back link is accepted with the prefix written into it (the script puts the address of the page, with the prefix, into those links) and is checked again after the prefix is taken off, so `/tv//host` cannot become `//host`.
- The `ok` answer of `POST /auth/sso/poll` gives `next` with the prefix, ready for the browser.

### Upgrade notes

1. **Nothing changes without the setting.** A service that is at the root of its address is served exactly as before.
2. **To serve it under `/tv/`,** put `TIMEVAULT_BASE_PATH=/tv` into `.env` and restart. An nginx block that takes the prefix off (`rewrite ^/tv(/.*) $1 break;`) keeps working; the block in the runbook that passes the address on unchanged is simpler.
3. **The client address.** In the first nginx, set `proxy_set_header X-Forwarded-For $remote_addr;` and list the proxy in `TIMEVAULT_TRUSTED_PROXIES`. `$http_x_forwarded_for`, which a block written for a second proxy uses, lets a browser choose the address that TimeVault records (it was reproduced).

### Known limits

- Under a path prefix TimeVault shares its origin (scheme, host and port) with the other applications of the host name; a host name or a port of its own is the stronger setup (see [security.md](./security.md)).
- Opened on its own address without the prefix (`http://127.0.0.1:8660/explorer`), a page is shown, but the browser does not send the session cookie to it. Links and redirects lead to the prefixed addresses.
- Checked with the real nginx and a browser against a scratch database, not on the company's server, and not with the company's single sign-on service.

## 0.1.4

Single sign-on with the Windows account, folder sizes from Everything, the duration of every operation in the audit log, a limit of versions per file with rolling deletion, and automatic backups that are spread out instead of all starting together. The reasons are in [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md), [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md) and [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md).

### Added

- **Single sign-on.** When `TIMEVAULT_SSO_HOST` is set (the host of the company sign-on service; it can change, so it is one line in `.env`), the login page has a second button, "Sign in with Windows (SSO)". It opens the sign-on page in a window, waits for the sign-on service to record who the browser is in Redis (`sso_login_<token>`), and signs that employee in when the account in Redis is active and the address matches the browser's. The accounts, the roles and the password sign-in are unchanged. New endpoints `POST /auth/sso/start` and `POST /auth/sso/poll`. The Settings page and `GET /api/v1/settings` show the sign-on service (`sso_host`). The audit log records the method (`sso` or `password`), the Windows user and, for a refused sign-in, the reason.
- **`TIMEVAULT_SSO_REQUIRE_SAME_IP`** (default `true`) and **`TIMEVAULT_TRUSTED_PROXIES`**. Behind a reverse proxy TimeVault only sees the address of the proxy. List the proxies in `TIMEVAULT_TRUSTED_PROXIES` and their `X-Forwarded-For` header gives the real address of the browser, for the sign-in check, for the **From** column of the audit log and for the sign-in throttle.
- **Folder sizes from Everything** on the Explorer: a folder row shows the size of everything inside it, as the index of Everything knows it (also in search results), and folders can be sorted by size. The modified time of a folder, read from the disk, was already shown. It needs "Index folder size" in Everything and `TIMEVAULT_EVERYTHING_URL`; without them the size cell shows "-" as before. The Time Machine and the versions page do not show folder sizes.
- **A Duration column in the audit log.** Backups, verifications, restores and the deletion of old versions record how long they ran. For a backup the page also shows the speed of the data it copied, so the first backup of a big folder tells how fast the disk and the repository are. The Settings page shows "Last run took" for every monitored folder. The API has `duration_ms` on audit events and `last_run_ms` on monitored folders, and the `backup.finish` record holds `bytes`, `new_bytes` and `new_objects`.
- **A limit of versions per file** for each monitored folder: the field "Versions kept per file" on the Settings page (0 keeps everything, up to 10000) and `max_versions` in the API. When a file has more versions, the older ones are deleted and the repository content that nobody needs any more is freed. Recovery points that lost versions are marked **Partial**. A "Version cleanup" job runs when a limit is set or lowered, and lowering it asks for confirmation (`confirm_prune`). The same job is queued when the deletion right after a backup could not run (for example because a verification of the folder was running), so that it is not forgotten. New audit action `retention.prune` ("Old versions deleted").
- **Automatic backups that are spread out,** with the settings `TIMEVAULT_SCHEDULER_PARALLEL`, `TIMEVAULT_SCHEDULER_GAP`, `TIMEVAULT_SCHEDULER_REST`, `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` and `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY`, and the setting **`TIMEVAULT_PROCESS_PRIORITY`** (see Changed).
- Migration `0004_v014.sql` (`audit_events.duration_ms`, `sources.max_versions`, `last_run_ms`, `last_walk_at`, `last_walk_items`, `recovery_points.partial`, the job kind `prune`, an index on the versions' checksums).

### Changed

- **Automatic backups no longer start all together.** In 0.1.3 every folder whose interval had passed was queued at the next tick. Now one automatic backup runs at a time (`TIMEVAULT_SCHEDULER_PARALLEL=1`), with a gap between two starts (`auto`: the gap that just keeps up with all the schedules, so 10 folders of 60 minutes start one every 6 minutes, within 30 seconds and 10 minutes) and a rest after each run (`auto`: as long as the run took, within 30 seconds and 10 minutes). When several folders are due, the ones without a recovery point come first, then the most overdue in proportion to its interval, then the one whose last run was the quickest. A backup started by hand is not affected.
- **The service runs at a lower priority** by default (`TIMEVAULT_PROCESS_PRIORITY=below_normal`), so that a backup scan yields to the file shares of the same server. Set `normal` to get the old behaviour.
- A restore or a verification of a folder is refused (HTTP 409 `prune_running`) while its older versions are being deleted, and so is a change of its version limit. A version cleanup that is only queued refuses nothing. A restore or verification that was queued before the deletion began waits for it to end.
- **The job queue lets a version cleanup take turns with the other jobs of its folder.** A cleanup is not started while a backup, a restore or a verification of the folder runs, and none of them is started while it runs. A job that has to wait stays in the queue and holds no worker, and a backup that waits for a cleanup neither fails nor uses up its schedule interval.
- Changing the exclude patterns of a monitored folder makes its next scheduled run scan the folder, also when the Everything probe is on (it forgets the last scan).
- With the Everything probe on, a tick asks Everything at most 25 times, and an Everything that does not answer is left alone for a minute.
- The page that a single sign-in returns to is limited to 2,048 bytes (a longer one becomes `/`); the same limit applies to the `next` of the password sign-in.
- Verifying a partial recovery point still checks the objects, but skips the comparison with the manifest and says so.
- When `TIMEVAULT_TRUSTED_PROXIES` is set, the client address of a request is taken from `X-Forwarded-For` if the request comes from one of those proxies.
- Folder sizes, when they are known, take part in sorting by size. The size of a folder in `GET /api/v1/files` is the one from Everything, or 0 when it is not known.
- The audit log has one more column; the job pages show the new phases "Deleting old versions" and "Freeing space".
- The `.env` template that `scripts\dotenv.ps1` writes knows `TIMEVAULT_SSO_HOST`, and [configs/timevault.example.env](../configs/timevault.example.env) describes every new setting.

### Upgrade notes

1. **Database.** Apply migration `0004` (`TIMEVAULT_AUTO_MIGRATE=true`, or `timevault-server migrate`). It adds nullable columns, one flag that defaults to false, a wider check on the job kind and two indexes. The previous version keeps working against the migrated database, but it ignores the version limit.
2. **Every monitored folder keeps all its versions** until an administrator sets a limit.
3. **Single sign-on.** Set `TIMEVAULT_SSO_HOST` to turn it on; without it the login page is as before. The sign-on service must write its results to the same Redis as `TIMEVAULT_REDIS_URL`. Behind a reverse proxy set `TIMEVAULT_TRUSTED_PROXIES` (recommended; the proxy has to pass `X-Forwarded-For` and keep the `Host` header), or set `TIMEVAULT_SSO_REQUIRE_SAME_IP=false` if the proxy cannot pass the real address. With neither, every single sign-in ends with "The sign-in could not be matched to this browser".
4. **Folder sizes.** In Everything turn on Tools > Options > Indexes > "Index folder size", and keep `TIMEVAULT_EVERYTHING_URL` set.
5. **The Everything probe of the scheduler is off.** To turn it on set `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE=true` once Everything monitors the volumes of the folders and has "Index recently changed date" on (see [runbook.md](./runbook.md#scheduler-settings)).
6. **API callers.** Monitored folders have `max_versions` and `last_run_ms`, recovery points have `partial`, audit events have `duration_ms`, `GET /api/v1/settings` has `sso_host`, `PATCH /api/v1/sources/{id}` takes `max_versions` and `confirm_prune`, and two new 409 error codes exist (see [api.md](./api.md)).

### Known limits

- Deleted versions are gone for good. A recovery point that lost versions no longer shows the files whose versions were deleted, so a restore from it can be incomplete.
- The space of deleted versions is freed only at a time when no backup runs, and the service forgets that a sweep is pending when it restarts: the next deletion of old versions sweeps everything again.
- The sizes of folders are those of the index of Everything, which can lag a little behind the disk. The Time Machine and the versions page show none.
- The sign-on check compares addresses. With `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`, a sign-in can be forced on a person who opens a link that carries somebody else's token (see [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md)).
- The pace of the automatic backups is kept in memory: after a restart the first one may start at once.
- With the probe on, a change that Everything has not seen yet is found by the next scan that is not skipped, at the latest after `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY`.

## 0.1.3

Lists you can sort, filter and page, a Type column, display folders for the monitored folders, and no more real folders of the server for anyone but administrators. The reasons are in [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md) and [ADR 0008](./adr/0008-folder-lists-are-cut-on-the-server.md).

### Added

- **A Type column and a type filter** on the Explorer, the Time Machine and the search results. The type is the extension in capitals (`PDF`, `XLSX`), "Folder", "Link" for junctions and symbolic links, and "File" for a file without an extension. The filter lists what the folder holds with a count (`PDF (40)`) and offers "Folders" and "Files" as a whole.
- **Sorting by name, type, size or modified time, in both directions, over the whole folder.** Before, only the rows that had been loaded were sorted, in the browser. Folders always come first.
- **Pages instead of "Load more".** 100 items a page by default (50, 100, 200 or 500 can be chosen, 50, 100 or 200 for search results) with previous/next links and page numbers above and below the list, and a count ("Showing 101–200 of 10,000"). Sort, filter and page size are in the address, so a link keeps them, and changing the filter or the page size starts again at page 1. A folder of 10,000 files opens in about 0.1 s and one of 50,000 in about 0.5 s on the development machine. "Select all" selects the items of the current page.
- **Buttons that jump to the top and to the bottom of the page**, on every page that is long enough to need them.
- **Display folders.** A monitored folder can have a display folder, the network path people open it with, such as `\\sgmeslt595.co.murata.local\TimeVault\monitor` for `D:\TimeVault\monitor`. Administrators enter it on the Settings page (new field "Display folder"; the real folder is now called "Folder on the server") or with `display_path` in `POST` and `PATCH /api/v1/sources`. It can be changed at any time, also when the folder has backups.
- **`TIMEVAULT_SERVER_HOST`**, the name of this server as people type it in network paths. When it is set, a display folder is **required** for every monitored folder and its host must be this one (case does not matter; it is stored the way the setting spells it). A folder on another server is refused, because this server's search and restore only work with its own folders. When it is not set, a display folder is optional and only its syntax is checked.
- **`TIMEVAULT_RESTORE_DISPLAY_ROOT`**, the restore folder as people reach it over the network (`\\sgmeslt595.co.murata.local\TimeVault\restore`). It must be on `TIMEVAULT_SERVER_HOST`.
- **A "Location" line with a Copy button** on the Explorer, the Time Machine and the page of a file's versions: the display folder of what is on screen. Administrators also see the real folder there.
- Migration `0003_display_path.sql` (`sources.display_path`), `server_host` and `restore_display_root` in `GET /api/v1/settings`, and the list parameters of the API (see [api.md](./api.md)).

### Changed

- **Only administrators see the folders of the server's disk.** Everybody else, signed in or not, sees display folders: on the Explorer, the Time Machine, versions, Backups, Settings, restore previews and restore jobs. The backup folder (the repository) is not named for them; the Settings and Status pages only say how full it is. Texts that quote a real folder are rewritten for them: job errors and logs, restore item messages, the reasons a file was not captured, audit details and component details. A folder with a display folder becomes its display path, a folder without one becomes `[<name of the monitored folder>]`, the backup folder `[backup folder]` and the restore folder its display root or `[restore folder]`.
- `GET /api/v1/sources` returns `root_path` to administrators only (every signed-in account got it before) and `display_path` to everybody. `GET /api/v1/settings` returns `repository`, `restore_root` and `env_file` to administrators only. The `destination` of a restore preview is the display path; administrators also get `destination_real`.
- **A restore can select up to 500 paths** (it was 50), so that "Select all" on a page of 500 works.
- The Settings page for accounts that are not administrators no longer shows the repository folder, the real restore folder and the settings file.
- The deleted-items panel of the Explorer appears on the first page of a folder only, and follows the type filter.
- A long restore selection is folded into "N items selected" on the preview and job pages.
- `scripts\dev-demo.ps1` gives the demo folder a made-up display folder when `TIMEVAULT_SERVER_HOST` is set. `scripts\dotenv.ps1` and the `.env` template know the two new settings.

### Upgrade notes

1. **Database.** Apply migration `0003` (`TIMEVAULT_AUTO_MIGRATE=true`, or `timevault-server migrate`). It adds one nullable column; the previous version keeps working against the migrated database.
2. **Settings.** Add `TIMEVAULT_SERVER_HOST` and `TIMEVAULT_RESTORE_DISPLAY_ROOT` to `.env` (see [configs/timevault.example.env](../configs/timevault.example.env)). The service refuses to start when the restore display root is not a network path or is on another host.
3. **Existing monitored folders have no display folder.** With `TIMEVAULT_SERVER_HOST` set, an administrator has to give each of them one on the Settings page before it can be edited again (the Settings page marks them "No display folder yet"). Browsing, backups, automatic schedules and restores keep working meanwhile. Ordinary accounts see no location for such a folder, and `[<name>]` in texts that quote it.
4. **API callers.** Anyone who read `root_path` as an ordinary account has to use `display_path`, or an administrator account. `GET /api/v1/files` and `GET /api/v1/history/tree` behave as before unless they are given a new parameter; `files` now also returns `unfiltered`, `types` and a `type` for every entry, and its `total` counts the items that match the filter.
5. **Search (Everything).** Nothing changed in the service. To turn it on for an Everything you already run, see [runbook.md](./runbook.md#everything-search).

### Known limits

- The service checks the host of a display folder, not that the share really leads to the monitored folder. A wrong share name shows people a path that does not exist.
- Hiding the real folders works on text: a folder written in an unusual way (a short 8.3 name, a drive letter that is another name for the same disk) is not recognised.
- The deleted-items panel shows at most 500 items, on the first page only.
- Every page of a list reads the whole folder, so a folder with hundreds of thousands of entries takes seconds to open.

## 0.1.2

One settings file for the development setup, a warning when the environment overrides it, and a clearer Settings page.

### Changed

- **`.env` in the repository folder is the only settings file.** The dev scripts used to keep settings in four more files: `data\local.env` (`TIMEVAULT_GO_ROOT`), `data\pgdev\db.env` (the database URLs), `data\dev\server.env` (backup and restore folder, session key, local administrator) and `data\everything\everything.env` (search). They now read and write `.env`. The first time a script runs, it moves the settings of those files into `.env` (a key that `.env` already has keeps its value) and deletes the old files, but only after it has checked that every setting is in `.env`; a file with lines that are not `KEY=VALUE` is left alone with a warning.
- `scripts\dev-run.ps1` removes the `TIMEVAULT_*` variables that are left in your terminal before it starts the service, and says which, because they would override `.env`. `scripts\dev-env.ps1` does the same for your session, sets `TIMEVAULT_ENV_FILE`, and loads only what tests and tools read (`TIMEVAULT_TEST_*`, `TIMEVAULT_GO_ROOT`, `TIMEVAULT_PG_BIN`, `TIMEVAULT_EVERYTHING_EXE`); the settings of the service are no longer copied into the environment. `dev-run.ps1` no longer leaves `TIMEVAULT_LISTEN_ADDR` or `TIMEVAULT_ENV_FILE` in the terminal when it ends.
- `scripts\dev-demo.ps1` takes the address of the server from `TIMEVAULT_LISTEN_ADDR` in `.env` (it was a fixed default).
- The Settings page explains the backup folder and the restore folder, and says that a `TIMEVAULT_*` environment variable overrides the file. The comments in `.env` and in `configs/timevault.example.env` do the same.

### Added

- **The service names the settings that the environment overrides.** When a `TIMEVAULT_*` environment variable has another value than the same key in the `.env` file, the start-up log has a warning with the names (never the values), and `check-redis-users` prints them too.

### Fixed

- **The service listened on the old port 8470 although `.env` said 8660.** The 0.1.0 `scripts\dev-run.ps1` set `TIMEVAULT_LISTEN_ADDR=127.0.0.1:8470` in the PowerShell session that ran it and left it there; an environment variable wins over `.env`, silently. The scripts no longer leave variables behind, remove such leftovers, and the service reports the override.

### Upgrade notes

1. Run `scripts\dev-run.ps1` (or any other `scripts\dev-*.ps1`) once. It moves the settings of `data\local.env`, `data\pgdev\db.env`, `data\dev\server.env` and `data\everything\everything.env` into `.env` and deletes those files.
2. Close the terminals in which you ran an older `dev-run.ps1`, or run `Remove-Item Env:TIMEVAULT_LISTEN_ADDR` there. The scripts remove such variables too.
3. Whatever of your own read one of the four old files (a shortcut or a script that loads `data\pgdev\db.env`, for example) has to read `.env` now.

## 0.1.1

Open browsing, company sign-in from Redis, a Settings page for the monitored folders, automatic backups, and a new default port. The reasons are in [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md) and [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md).

### Added

- **Open browsing.** Anyone who can reach the address can use the Explorer, the Time Machine, file versions, Backups and backup jobs without an account. Everything that changes data needs an account, and so do the audit log, the status and settings pages and restore jobs. A visitor never sees the folders' paths on the server's disk, who requested a job, why it failed or its log. See [Who may call what](./api.md#who-may-call-what).
- **Company sign-in from Redis.** Accounts are read, read-only, from the `app_user_*` hashes of the other (dash) application. A user signs in with the employee ID, the name or the e-mail address. `Type` = `admin` makes an administrator and any other type a normal user. A deactivated account cannot sign in and loses an open session within a minute. If Redis does not answer, browsing keeps working. New settings: `TIMEVAULT_REDIS_URL`, `TIMEVAULT_REDIS_KEY_PREFIX` (default `app_user_`), `TIMEVAULT_REDIS_TIMEOUT` (default 3 seconds).
- **Two roles.** Every signed-in account can back up, verify, cancel a job, restore, and open the audit log, status and settings pages. Only `admin` accounts add or change monitored folders. The local administrator (`TIMEVAULT_ADMIN_PASSWORD_HASH`) is optional and counts as an `admin`.
- **Settings page** (`/settings`). It shows the monitored folders with their schedule, the backup folder and the restore folder with the free space of the repository, and the running configuration without secrets. Administrators add and edit monitored folders there (name, folder, exclude patterns, on or off, automatic schedule); the API offers `POST /api/v1/sources` and `PATCH /api/v1/sources/{id}`, and `GET /api/v1/settings` returns the running configuration.
- **Automatic backups.** A monitored folder can have a schedule of 1 to 43200 minutes. A run that finds nothing changed leaves no recovery point, no job and no audit record.
- **`.env` file.** Deployment settings (database, Redis, listen address, backup and restore folders, session key, search) are read from the environment and from a `.env` file; the environment wins. [configs/timevault.example.env](../configs/timevault.example.env) is the template.
- **`timevault-server check-redis-users`** reports how much of the Redis user directory can be used, without printing names, e-mail addresses, keys or passwords.
- Migration `0002_schedule.sql` (`sources.schedule_minutes`, `sources.last_run_at`), the audit action `source.update`, and the errors `source_busy` and `source_disabled` (HTTP `409`).

### Changed

- **The default port is `8660`** (it was `8470`).
- **A way to sign in is required.** At least one of `TIMEVAULT_REDIS_URL` and `TIMEVAULT_ADMIN_PASSWORD_HASH` must be set (unless `TIMEVAULT_AUTH_DISABLED=true`, loopback only). Before, the local administrator was required.
- **"Source" is called "monitored folder"** in the interface. The path of a monitored folder that already has a recovery point cannot be edited, and monitored folders are switched off, never deleted.
- **Session cookies have a new format.** The cookies of 0.1.0 are rejected, so everybody signs in again once.
- **Sign-in limits.** An attempt is counted when it starts: 5 attempts per client address and 10 failures per account name in 10 minutes. A sign-in that failed because Redis did not answer is not held against the account.
- `POST /backups/sources` was replaced by `POST /settings/sources` (administrators only), and `POST /api/v1/sources` now needs an administrator. Starting a backup of a switched-off folder returns `409 source_disabled`. Changing the folder of a monitored folder while its backup runs returns `409 source_busy`.
- **Scripts.** `scripts\dev-run.ps1` works with a PostgreSQL server you already have (the private cluster on port 55432 is started only when the database settings point at it), creates a commented `.env`, and `-Port` is optional. `scripts\dev-db.ps1` refuses to overwrite or delete a `db.env` that points at another server. `scripts\dev-demo.ps1` uses port 8660.
- **New Go modules.** `github.com/redis/go-redis/v9` (v9.22.0) to read the Redis user directory, and `github.com/alicebob/miniredis/v2` (v2.40.0, tests only). See [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md).

### Fixed

- The language links and the sign-in link returned to the page that was first loaded, instead of the page on screen, after navigating inside the page without a reload.

### Upgrade notes

1. **Port.** The default listen address is now `127.0.0.1:8660`. Update bookmarks and any reverse proxy, or set `TIMEVAULT_LISTEN_ADDR` to keep the old port.
2. **`.env`.** A `.env` file in the working directory, next to `timevault-server.exe`, or the one named by `TIMEVAULT_ENV_FILE` is now read. Environment variables win over the file.
3. **Database.** Apply migration `0002` before the new version serves requests: set `TIMEVAULT_AUTO_MIGRATE=true` or run `timevault-server migrate`. `scripts\dev-run.ps1` already does it for the development setup. The migration only adds two columns; existing data and monitored folders stay as they are, and every existing monitored folder stays manual-only until you give it a schedule.
4. **Redis.** To let company accounts sign in, put `TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0` (your own address and password) into `.env`. Then run `timevault-server check-redis-users` once from the same folder, against the real Redis, and fix what it reports. Without a Redis URL only the local administrator can sign in.
5. **Local administrator.** `TIMEVAULT_ADMIN_PASSWORD_HASH` and `TIMEVAULT_ADMIN_USER` keep working. The administrator is optional now.
6. **Sessions.** Everybody has to sign in again once.
7. **API.** Callers of `POST /api/v1/sources` need an administrator account. `GET /api/v1/jobs/{id}` hides `requested_by`, `error` and `events` from callers without an account.
8. **Tests.** `go test` needs `TIMEVAULT_TEST_DATABASE_URL`. When `data\pgdev\db.env` points at your own PostgreSQL server, create the `timevault_test` database there; the private cluster on port 55432 is no longer required.
9. **Network.** Do not open the service to other computers without HTTPS in front of it (see [security.md](./security.md)): company passwords are posted to it and are stored in plain text in Redis.

### Known limits

- The Redis user directory has been tested against an in-process fake with pickle data written by Python, not against the company's own Redis. Run `check-redis-users` on it once.
- Anonymous visitors see the names, sizes, modification times and structure of every monitored folder and its history. Not the contents.
- Behind a reverse proxy all visitors share one address in the sign-in limits, because `X-Forwarded-For` is not trusted. A name can be locked out for ten minutes by ten wrong attempts, the local administrator included.
- A login that matches more than 200 Redis keys is refused as ambiguous. A stale duplicate record of an employee ID is not resolved.
- There is no retention, so automatic runs that find changes add recovery points for good. A scheduled folder that cannot be read produces one failed job and one audit record per interval.
- A manual backup request that finds an automatic backup already queued or running is attached to it. If that run finds nothing changed, its job is deleted, so `GET /api/v1/jobs/{id}` then returns `404`.
- Two administrators who edit the same monitored folder at the same time: the last save wins.

The full lists are in [security.md](./security.md) and [mvp-status.md](./mvp-status.md).

## 0.1.0 (MVP)

A vertical slice on a local NTFS folder: browse, back up, keep history, verify and restore, with one administrator, a manual start only and port `8470`. See [mvp-status.md](./mvp-status.md) for what was done and what was not.

---

# 中文

## 概述

TimeVault 每个版本改了什么，以及升级时要做什么。最新的在最前面。

## 0.1.5

TimeVault 现在可以放在多个应用共用的主机名的某个路径下提供服务，例如 `http://prod_eng.co.murata.local/tv/`。此前它假定自己位于地址的根路径，所以在 `/tv/` 之后，第一次重定向就会离开前缀，落到另一个应用上。原因见 [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md)。

### 新增

- **`TIMEVAULT_BASE_PATH`**（例如 `/tv`；默认为空，此时什么都不变）。设置后，页面、重定向和 Cookie 给出的每个地址都带有该前缀：页面的链接、表单、文件和图标，每个 `Location` 头（包括 API 的，以及 Web 服务器自己的重定向），任务进度的轮询，单点登录，以及 Cookie `tv_session`、`tv_lang` 和 `tv_flash`，它们被限制在该前缀之内，所以同一主机名下的其他应用收不到它们。带前缀或不带前缀的请求都会被接受，所以代理可以保留前缀（推荐）也可以去掉它。只有前缀本身的请求（`/tv`）会被送到 `/tv/`。启动日志会写出 `serving under a path prefix`；设置页显示**地址前缀**，`GET /api/v1/settings` 有 `base_path`。
- [runbook.md](./runbook.md#放在带路径前缀的反向代理之后) 中有一个测试过的 nginx 配置块，以及要检查的内容。

### 变更

- 登录、切换语言或返回链接之后要回到的页面，现在也接受把前缀写进去的形式（脚本会把带前缀的页面地址放进这些链接），并且在去掉前缀之后再检查一遍，所以 `/tv//host` 不会变成 `//host`。
- `POST /auth/sso/poll` 的 `ok` 回答给出的 `next` 带有前缀，浏览器可以直接使用。

### 升级说明

1. **不设置该项就什么都不变。** 位于其地址根路径的服务，运行方式与以前完全一样。
2. **要放在 `/tv/` 之下提供服务，** 请在 `.env` 中写入 `TIMEVAULT_BASE_PATH=/tv` 并重启。去掉前缀的 nginx 配置块（`rewrite ^/tv(/.*) $1 break;`）仍然可用；runbook 中原样转发地址的配置块更简单。
3. **客户端地址。** 在第一层 nginx 中设置 `proxy_set_header X-Forwarded-For $remote_addr;`，并把该代理列入 `TIMEVAULT_TRUSTED_PROXIES`。为第二层代理写的配置块所用的 `$http_x_forwarded_for`，会让浏览器自己选择 TimeVault 记录的地址（已复现）。

### 已知限制

- 在路径前缀之下，TimeVault 与该主机名下的其他应用共享源（协议、主机和端口）；给它单独的主机名或端口是更强的做法（见 [security.md](./security.md)）。
- 通过自己的地址、不带前缀打开（`http://127.0.0.1:8660/explorer`）时，页面会显示，但浏览器不会把会话 Cookie 发给它。链接和重定向都指向带前缀的地址。
- 用真实的 nginx 和浏览器在临时数据库上检查过，没有在公司的服务器上检查，也没有配合公司的单点登录服务检查。

## 0.1.4

使用 Windows 账号的单点登录，来自 Everything 的文件夹大小，审计日志中每个操作的耗时，每个文件的版本数上限及滚动删除，以及错开时间而不是同时启动的自动备份。原因见 [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md)、[ADR 0010](./adr/0010-version-limit-and-garbage-collection.md) 和 [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md)。

### 新增

- **单点登录。** 设置了 `TIMEVAULT_SSO_HOST`（公司单点登录服务的主机；它可能变更，所以是 `.env` 中的一行）后，登录页有第二个按钮"使用 Windows 账号登录（SSO）"。它在一个窗口中打开单点登录页面，等待单点登录服务在 Redis 中记录浏览器是谁（`sso_login_<token>`），并且在 Redis 中的账号有效、地址与浏览器一致时让该员工登录。账号、角色和密码登录都没有变化。新增端点 `POST /auth/sso/start` 和 `POST /auth/sso/poll`。设置页和 `GET /api/v1/settings` 会显示单点登录服务（`sso_host`）。审计日志记录登录方式（`sso` 或 `password`）、Windows 用户，以及被拒绝登录的原因。
- **`TIMEVAULT_SSO_REQUIRE_SAME_IP`**（默认 `true`）和 **`TIMEVAULT_TRUSTED_PROXIES`**。在反向代理之后，TimeVault 只能看到代理的地址。把代理列在 `TIMEVAULT_TRUSTED_PROXIES` 中，它们的 `X-Forwarded-For` 头就会提供浏览器的真实地址，用于登录检查、审计日志的**来源**列和登录限流。
- **文件浏览中来自 Everything 的文件夹大小：** 文件夹行显示其中所有内容的大小，数据来自 Everything 的索引（搜索结果中也一样），文件夹也可以按大小排序。文件夹的修改时间（从磁盘读取）原本就已显示。它需要 Everything 中的"索引文件夹大小"和 `TIMEVAULT_EVERYTHING_URL`；没有它们时大小单元格像以前一样显示"-"。时间机器和版本页面不显示文件夹大小。
- **审计日志中的耗时列。** 备份、校验、恢复和删除旧版本都会记录运行了多久。对备份，页面还显示它所复制数据的速度，所以大文件夹的第一次备份能说明磁盘和仓库有多快。设置页面为每个监控文件夹显示"上次耗时"。API 在审计事件上有 `duration_ms`，在监控文件夹上有 `last_run_ms`，`backup.finish` 记录包含 `bytes`、`new_bytes` 和 `new_objects`。
- **每个监控文件夹每个文件的版本数上限：** 设置页面上的"每个文件保留的版本数"字段（0 表示全部保留，最大 10000）和 API 中的 `max_versions`。当一个文件的版本多于上限时，较旧的版本会被删除，并释放没有人再需要的仓库内容。失去版本的恢复点会被标记为**不完整**。设置或调低上限时会运行"版本清理"任务，调低时会要求确认（`confirm_prune`）。当备份之后立即进行的删除无法运行时（例如该文件夹的校验正在运行），也会排入同样的任务，这样它就不会被遗忘。新增审计操作 `retention.prune`（"已删除旧版本"）。
- **错开时间的自动备份，** 带有设置 `TIMEVAULT_SCHEDULER_PARALLEL`、`TIMEVAULT_SCHEDULER_GAP`、`TIMEVAULT_SCHEDULER_REST`、`TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` 和 `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY`，以及设置 **`TIMEVAULT_PROCESS_PRIORITY`**（见"变更"）。
- 迁移 `0004_v014.sql`（`audit_events.duration_ms`、`sources.max_versions`、`last_run_ms`、`last_walk_at`、`last_walk_items`、`recovery_points.partial`、任务类型 `prune`、版本校验值上的索引）。

### 变更

- **自动备份不再同时启动。** 在 0.1.3 中，间隔已过的每个文件夹都会在下一个周期被排入队列。现在自动备份一次运行一个（`TIMEVAULT_SCHEDULER_PARALLEL=1`），两次启动之间有间隔（`auto`：刚好跟得上所有计划的间隔，所以 10 个每 60 分钟的文件夹每 6 分钟启动一个，限制在 30 秒到 10 分钟之内），每次运行之后有休息（`auto`：等于这次运行所花的时间，限制在 30 秒到 10 分钟之内）。有多个文件夹到期时，没有恢复点的先来，然后是相对于自己的间隔超期最多的，再然后是上次运行最快的。手动启动的备份不受影响。
- **服务默认以较低的优先级运行**（`TIMEVAULT_PROCESS_PRIORITY=below_normal`），这样备份扫描会让位于同一台服务器上的文件共享。设为 `normal` 可恢复旧行为。
- 正在删除某个文件夹的较旧版本时，该文件夹的恢复或校验会被拒绝（HTTP 409 `prune_running`），更改它的版本数上限也会被拒绝。只是排在队列里的版本清理不会拒绝任何操作。在删除开始之前就已排队的恢复或校验，会等到删除结束。
- **任务队列让版本清理与其所在文件夹的其他任务轮流执行。** 该文件夹的备份、恢复或校验正在运行时不会启动清理，清理运行时也不会启动它们中的任何一个。需要等待的任务留在队列中，不占用任何 worker，而等待清理的备份既不会失败，也不会用掉它的计划间隔。
- 更改监控文件夹的排除规则会让它的下一次计划运行扫描该文件夹，即使开启了 Everything 探测也一样（它会忘掉上次的扫描记录）。
- 开启 Everything 探测后，一个周期最多询问 Everything 25 次，没有应答的 Everything 在一分钟内不会再被询问。
- 一次单点登录返回的页面地址限制在 2,048 字节以内（更长的会变成 `/`）；密码登录的 `next` 适用同样的限制。
- 校验不完整的恢复点仍然检查对象，但跳过与清单的比较，并且会说明这一点。
- 设置了 `TIMEVAULT_TRUSTED_PROXIES` 时，如果请求来自其中的某个代理，请求的客户端地址取自 `X-Forwarded-For`。
- 文件夹大小在已知时参与按大小排序。`GET /api/v1/files` 中文件夹的大小是来自 Everything 的大小，未知时为 0。
- 审计日志多了一列；任务页面显示新的阶段"删除旧版本"和"释放空间"。
- `scripts\dotenv.ps1` 写出的 `.env` 模板认识 `TIMEVAULT_SSO_HOST`，[configs/timevault.example.env](../configs/timevault.example.env) 则说明了每一个新设置。

### 升级说明

1. **数据库。** 应用迁移 `0004`（`TIMEVAULT_AUTO_MIGRATE=true`，或 `timevault-server migrate`）。它增加了可为空的列、一个默认为 false 的标志、对任务类型更宽的检查，以及两个索引。上一个版本在已迁移的数据库上仍可工作，但会忽略版本数上限。
2. **每个监控文件夹都保留全部版本，** 直到管理员设置上限。
3. **单点登录。** 设置 `TIMEVAULT_SSO_HOST` 即可开启；不设置时登录页与以前一样。单点登录服务必须把结果写入与 `TIMEVAULT_REDIS_URL` 相同的 Redis。在反向代理之后，请设置 `TIMEVAULT_TRUSTED_PROXIES`（推荐；代理必须传递 `X-Forwarded-For` 并保留 `Host` 头），如果代理无法传递真实地址，则设置 `TIMEVAULT_SSO_REQUIRE_SAME_IP=false`。两者都没有时，每次单点登录都会以"无法把这次登录与当前浏览器对应起来"结束。
4. **文件夹大小。** 在 Everything 中开启 工具 > 选项 > 索引 > "索引文件夹大小"，并保持设置 `TIMEVAULT_EVERYTHING_URL`。
5. **调度器的 Everything 探测默认关闭。** 当 Everything 监控着这些文件夹所在的卷并开启"索引最近更改日期"之后，设置 `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE=true` 即可开启（见 [runbook.md](./runbook.md#调度器设置)）。
6. **API 调用方。** 监控文件夹有 `max_versions` 和 `last_run_ms`，恢复点有 `partial`，审计事件有 `duration_ms`，`GET /api/v1/settings` 有 `sso_host`，`PATCH /api/v1/sources/{id}` 接受 `max_versions` 和 `confirm_prune`，并新增两个 409 错误码（见 [api.md](./api.md)）。

### 已知限制

- 被删除的版本永远消失。失去版本的恢复点不再显示其版本已被删除的文件，所以从它恢复可能不完整。
- 被删除版本占用的空间只在没有备份运行的时候才会释放，并且服务重启后会忘记有一次清扫待办：下一次删除旧版本时会再次清扫全部内容。
- 文件夹的大小来自 Everything 的索引，可能比磁盘稍微滞后。时间机器和版本页面不显示大小。
- 单点登录检查比较的是地址。设置 `TIMEVAULT_SSO_REQUIRE_SAME_IP=false` 后，可能有人把登录强加给打开了带有别人令牌的链接的人（见 [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md)）。
- 自动备份的间隔状态保存在内存中：重启后，第一个备份可能立即启动。
- 开启探测后，Everything 尚未看到的变化会在下一次没有被跳过的扫描时被发现，最晚在 `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY` 之后。

## 0.1.3

可以排序、筛选和分页的列表，类型列，监控文件夹的显示文件夹，以及除管理员之外任何人都看不到服务器上的真实文件夹。原因见 [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md) 和 [ADR 0008](./adr/0008-folder-lists-are-cut-on-the-server.md)。

### 新增

- **类型列和类型筛选**，用于文件浏览、时间机器和搜索结果。类型是大写的扩展名（`PDF`、`XLSX`）、"文件夹"、表示联接点和符号链接的"链接"，以及没有扩展名的文件的"文件"。筛选框列出该文件夹中有的类型及数量（`PDF (40)`），并整体提供"文件夹"和"文件"两项。
- **按名称、类型、大小或修改时间排序，两个方向都可以，并且作用于整个文件夹。** 以前只有已加载的行在浏览器中排序。文件夹始终排在最前面。
- **用分页代替"加载更多"。** 默认每页 100 项（可选 50、100、200 或 500；搜索结果可选 50、100 或 200），列表上方和下方有上一页/下一页链接和页码，并显示数量（"显示第 101–200 项，共 10000 项"）。排序、筛选和每页数量都写在地址里，所以链接会保留它们；更改筛选或每页数量会回到第 1 页。在开发机器上，包含 10,000 个文件的文件夹约 0.1 秒打开，包含 50,000 个文件的约 0.5 秒。"全选"只选中当前页的项目。
- **直达页面顶部和底部的按钮**，在足够长、需要它们的页面上显示。
- **显示文件夹。** 监控文件夹可以有一个显示文件夹，即人们打开它时使用的网络路径，例如 `D:\TimeVault\monitor` 对应 `\\sgmeslt595.co.murata.local\TimeVault\monitor`。管理员在设置页填写（新字段"显示文件夹"；真实文件夹现在叫"服务器上的文件夹"），或在 `POST` 和 `PATCH /api/v1/sources` 中使用 `display_path`。它可以随时更改，文件夹已有备份时也一样。
- **`TIMEVAULT_SERVER_HOST`**，即人们在网络路径中输入的本服务器名称。设置后，每个监控文件夹都**必须**有显示文件夹，并且它的主机必须是这一台（不区分大小写；保存时使用设置中的写法）。其他服务器上的文件夹会被拒绝，因为本服务器的搜索和恢复只能处理它自己的文件夹。没有设置时，显示文件夹是可选的，只检查语法。
- **`TIMEVAULT_RESTORE_DISPLAY_ROOT`**，即人们通过网络访问恢复文件夹的路径（`\\sgmeslt595.co.murata.local\TimeVault\restore`）。它必须位于 `TIMEVAULT_SERVER_HOST` 上。
- **带"复制"按钮的"位置"行**，显示在文件浏览、时间机器和文件版本页面上：当前屏幕内容的显示文件夹。管理员在那里还能看到真实文件夹。
- 迁移 `0003_display_path.sql`（`sources.display_path`），`GET /api/v1/settings` 中的 `server_host` 和 `restore_display_root`，以及 API 的列表参数（见 [api.md](./api.md)）。

### 变更

- **只有管理员能看到服务器磁盘上的文件夹。** 其他人无论是否登录，看到的都是显示文件夹：在文件浏览、时间机器、版本、备份、设置、恢复预览和恢复任务中。备份文件夹（仓库）不会向他们点名；设置页和状态页只说明它用了多少。引用真实文件夹的文本会为他们改写：任务错误和日志、恢复条目消息、文件未能备份的原因、审计详情和组件详情。有显示文件夹的文件夹变成它的显示路径，没有的变成 `[<监控文件夹名称>]`，备份文件夹变成 `[backup folder]`，恢复文件夹变成它的显示根路径或 `[restore folder]`。
- `GET /api/v1/sources` 只向管理员返回 `root_path`（以前每个已登录账号都能得到），向所有人返回 `display_path`。`GET /api/v1/settings` 只向管理员返回 `repository`、`restore_root` 和 `env_file`。恢复预览的 `destination` 是显示路径；管理员还会得到 `destination_real`。
- **一次恢复最多可以选择 500 个路径**（以前是 50），这样在 500 项一页的页面上"全选"也能用。
- 非管理员账号看到的设置页不再显示仓库文件夹、真实的恢复文件夹和设置文件。
- 文件浏览中的已删除项目面板只出现在文件夹的第一页，并且遵循类型筛选。
- 预览页和任务页把很长的恢复选择折叠成"已选择 N 项"。
- 设置了 `TIMEVAULT_SERVER_HOST` 时，`scripts\dev-demo.ps1` 会给演示文件夹一个虚构的显示文件夹。`scripts\dotenv.ps1` 和 `.env` 模板认识这两个新设置。

### 升级说明

1. **数据库。** 应用迁移 `0003`（`TIMEVAULT_AUTO_MIGRATE=true`，或运行 `timevault-server migrate`）。它只增加一个可为空的列；上一个版本在迁移后的数据库上仍然可以工作。
2. **设置。** 在 `.env` 中添加 `TIMEVAULT_SERVER_HOST` 和 `TIMEVAULT_RESTORE_DISPLAY_ROOT`（见 [configs/timevault.example.env](../configs/timevault.example.env)）。如果恢复显示根路径不是网络路径或位于另一台主机上，服务会拒绝启动。
3. **已有的监控文件夹没有显示文件夹。** 设置了 `TIMEVAULT_SERVER_HOST` 之后，管理员必须先在设置页给它们每个填上一个，才能再次编辑（设置页会把它们标为"还没有显示文件夹"）。在此期间浏览、备份、自动计划和恢复都照常工作。非管理员账号看不到这类文件夹的位置，引用它的文本中会显示 `[<名称>]`。
4. **API 调用方。** 以前以普通账号读取 `root_path` 的调用方，现在要改用 `display_path`，或使用管理员账号。`GET /api/v1/files` 和 `GET /api/v1/history/tree` 在没有给出新参数时行为不变；`files` 现在还会返回 `unfiltered`、`types` 以及每个条目的 `type`，它的 `total` 统计的是符合筛选条件的项目数。
5. **搜索（Everything）。** 服务本身没有变化。要为你已经在运行的 Everything 启用搜索，见 [runbook.md](./runbook.md#everything-搜索)。

### 已知限制

- 服务检查显示文件夹的主机，但不检查共享是否真的指向该监控文件夹。共享名写错时，人们看到的是一个不存在的路径。
- 隐藏真实文件夹是基于文本处理的：以不寻常的方式书写的文件夹（8.3 短名称、同一磁盘的另一个盘符别名）无法被识别。
- 已删除项目面板最多显示 500 项，并且只在第一页显示。
- 列表的每一页都会读取整个文件夹，所以包含几十万个条目的文件夹需要几秒钟才能打开。

## 0.1.2

开发环境只用一个设置文件；环境变量覆盖了它时会给出警告；设置页的说明更清楚。

### 变更

- **仓库文件夹中的 `.env` 是唯一的设置文件。** 开发脚本以前把设置放在另外四个文件里：`data\local.env`（`TIMEVAULT_GO_ROOT`）、`data\pgdev\db.env`（数据库 URL）、`data\dev\server.env`（备份和恢复文件夹、会话密钥、本地管理员）和 `data\everything\everything.env`（搜索）。现在它们读写的都是 `.env`。脚本第一次运行时，会把这些文件里的设置移入 `.env`（`.env` 里已有的键保留它的值）并删除旧文件，但只有在确认每一项设置都已在 `.env` 中之后才会删除；含有非 `KEY=VALUE` 行的文件会被保留，并给出警告。
- `scripts\dev-run.ps1` 在启动服务之前，会移除终端里遗留的 `TIMEVAULT_*` 变量，并说明是哪些，因为它们会覆盖 `.env`。`scripts\dev-env.ps1` 对你的会话做同样的事，并设置 `TIMEVAULT_ENV_FILE`，只加载测试和工具才读取的内容（`TIMEVAULT_TEST_*`、`TIMEVAULT_GO_ROOT`、`TIMEVAULT_PG_BIN`、`TIMEVAULT_EVERYTHING_EXE`）；服务自己的设置不再被复制到环境变量中。`dev-run.ps1` 结束时不再把 `TIMEVAULT_LISTEN_ADDR` 或 `TIMEVAULT_ENV_FILE` 留在终端里。
- `scripts\dev-demo.ps1` 从 `.env` 中的 `TIMEVAULT_LISTEN_ADDR` 取得服务器地址（以前是固定的默认值）。
- 设置页解释了备份文件夹和恢复文件夹，并说明 `TIMEVAULT_*` 环境变量会覆盖文件。`.env` 和 `configs/timevault.example.env` 里的注释也做了同样的说明。

### 新增

- **服务会点名被环境变量覆盖的设置。** 当某个 `TIMEVAULT_*` 环境变量的值与 `.env` 文件中同名键的值不同时，启动日志中会有一条列出这些名字（绝不列出值）的警告，`check-redis-users` 也会打印它们。

### 修复

- **`.env` 里写的是 8660，服务却监听在旧端口 8470。** 0.1.0 的 `scripts\dev-run.ps1` 在运行它的 PowerShell 会话中设置了 `TIMEVAULT_LISTEN_ADDR=127.0.0.1:8470` 并把它留在那里；环境变量会悄无声息地覆盖 `.env`。现在脚本不再留下变量，会移除这类遗留变量，服务也会报告这种覆盖。

### 升级说明

1. 运行一次 `scripts\dev-run.ps1`（或任何其他 `scripts\dev-*.ps1`）。它会把 `data\local.env`、`data\pgdev\db.env`、`data\dev\server.env` 和 `data\everything\everything.env` 中的设置移入 `.env`，并删除这些文件。
2. 关闭运行过旧版 `dev-run.ps1` 的终端，或在其中运行 `Remove-Item Env:TIMEVAULT_LISTEN_ADDR`。脚本也会移除这类变量。
3. 你自己写的、读取过这四个旧文件之一的东西（例如加载 `data\pgdev\db.env` 的快捷方式或脚本），现在要改为读取 `.env`。

## 0.1.1

开放浏览、通过 Redis 的公司账号登录、用于管理监控文件夹的设置页、自动备份，以及新的默认端口。原因见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md) 和 [ADR 0006](./adr/0006-settings-in-env-and-database-and-scheduled-backups.md)。

### 新增

- **开放浏览。** 任何能访问该地址的人，无需账号就可以使用文件浏览、时间机器、文件版本、备份页和备份任务。所有会改变数据的操作都需要账号，审计日志、状态页、设置页和恢复任务也是如此。访问者看不到文件夹在服务器磁盘上的路径、任务是谁发起的、为什么失败以及任务日志。见[谁可以调用什么](./api.md#谁可以调用什么)。
- **通过 Redis 的公司账号登录。** 账号以只读方式读取自另一个（dash）应用的 `app_user_*` 哈希。用户可以用工号、姓名或邮箱登录。`Type` 为 `admin` 的是管理员，其他类型是普通用户。已停用的账号不能登录，已打开的会话在一分钟内失效。Redis 无应答时，浏览功能照常可用。新增设置：`TIMEVAULT_REDIS_URL`、`TIMEVAULT_REDIS_KEY_PREFIX`（默认 `app_user_`）、`TIMEVAULT_REDIS_TIMEOUT`（默认 3 秒）。
- **两种角色。** 每个已登录的账号都可以备份、校验、取消任务、恢复，以及打开审计日志、状态页和设置页。只有 `admin` 账号可以添加或修改监控文件夹。本地管理员（`TIMEVAULT_ADMIN_PASSWORD_HASH`）是可选的，并视为 `admin`。
- **设置页**（`/settings`）。显示监控文件夹及其计划、备份文件夹和恢复文件夹（含仓库剩余空间），以及不含机密信息的当前运行配置。管理员在这里添加和编辑监控文件夹（名称、文件夹、排除规则、开关、自动计划）；API 提供 `POST /api/v1/sources` 和 `PATCH /api/v1/sources/{id}`，`GET /api/v1/settings` 返回当前运行配置。
- **自动备份。** 监控文件夹可以设置 1 到 43200 分钟的计划。没有发现任何变化的一次运行，不会留下恢复点、任务或审计记录。
- **`.env` 文件。** 部署相关的设置（数据库、Redis、监听地址、备份和恢复文件夹、会话密钥、搜索）从环境变量和 `.env` 文件读取；环境变量优先。[configs/timevault.example.env](../configs/timevault.example.env) 是模板。
- **`timevault-server check-redis-users`** 报告 Redis 用户目录中有多少可用，不会打印姓名、邮箱、键或密码。
- 迁移 `0002_schedule.sql`（`sources.schedule_minutes`、`sources.last_run_at`）、审计动作 `source.update`，以及错误 `source_busy` 和 `source_disabled`（HTTP `409`）。

### 变更

- **默认端口是 `8660`**（原来是 `8470`）。
- **必须有一种登录方式。** `TIMEVAULT_REDIS_URL` 和 `TIMEVAULT_ADMIN_PASSWORD_HASH` 至少要设置一个（除非 `TIMEVAULT_AUTH_DISABLED=true`，且仅限回环地址）。以前必须有本地管理员。
- **界面中把"备份源"称为"监控文件夹"。** 已经有恢复点的监控文件夹不能再修改其路径，监控文件夹只能停用，绝不删除。
- **会话 Cookie 换了新格式。** 0.1.0 的 Cookie 会被拒绝，所以每个人要重新登录一次。
- **登录限制。** 尝试在开始时就计数：10 分钟内每个客户端地址 5 次尝试、每个账号名 10 次失败。因为 Redis 无应答而失败的登录，不会算在该账号头上。
- `POST /backups/sources` 被 `POST /settings/sources`（仅限管理员）取代，`POST /api/v1/sources` 现在需要管理员。对已停用的文件夹发起备份会返回 `409 source_disabled`。在监控文件夹的备份运行期间更改其文件夹会返回 `409 source_busy`。
- **脚本。** `scripts\dev-run.ps1` 可以配合你已有的 PostgreSQL 服务器使用（只有数据库设置指向 55432 端口的私有集群时才会启动它），会创建带注释的 `.env`，`-Port` 变为可选。`scripts\dev-db.ps1` 拒绝覆盖或删除指向其他服务器的 `db.env`。`scripts\dev-demo.ps1` 使用 8660 端口。
- **新的 Go 模块。** `github.com/redis/go-redis/v9`（v9.22.0）用于读取 Redis 用户目录，`github.com/alicebob/miniredis/v2`（v2.40.0，仅用于测试）。见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)。

### 修复

- 在页面内无刷新地跳转之后，语言链接和登录链接会回到最初加载的页面，而不是当前屏幕上的页面。

### 升级说明

1. **端口。** 默认监听地址现在是 `127.0.0.1:8660`。更新书签和反向代理，或设置 `TIMEVAULT_LISTEN_ADDR` 以保留旧端口。
2. **`.env`。** 现在会读取工作目录中的 `.env`、`timevault-server.exe` 旁边的 `.env`，或 `TIMEVAULT_ENV_FILE` 指定的文件。环境变量优先于文件。
3. **数据库。** 新版本开始处理请求之前，先应用迁移 `0002`：设置 `TIMEVAULT_AUTO_MIGRATE=true`，或运行 `timevault-server migrate`。开发环境的 `scripts\dev-run.ps1` 已经做了这件事。该迁移只增加两列；已有的数据和监控文件夹保持不变，每个已有的监控文件夹在你为它设置计划之前仍然是仅手动。
4. **Redis。** 要让公司账号登录，请把 `TIMEVAULT_REDIS_URL=redis://:<password>@127.0.0.1:6379/0`（换成你自己的地址和密码）写入 `.env`。然后在同一个文件夹中，针对真实的 Redis 运行一次 `timevault-server check-redis-users`，并处理它报告的问题。没有 Redis 地址时，只有本地管理员可以登录。
5. **本地管理员。** `TIMEVAULT_ADMIN_PASSWORD_HASH` 和 `TIMEVAULT_ADMIN_USER` 继续有效。管理员现在是可选的。
6. **会话。** 每个人都要重新登录一次。
7. **API。** 调用 `POST /api/v1/sources` 需要管理员账号。`GET /api/v1/jobs/{id}` 对没有账号的调用者隐藏 `requested_by`、`error` 和 `events`。
8. **测试。** `go test` 需要 `TIMEVAULT_TEST_DATABASE_URL`。如果 `data\pgdev\db.env` 指向你自己的 PostgreSQL 服务器，请先在那里创建 `timevault_test` 数据库；55432 端口的私有集群不再是必需的。
9. **网络。** 没有 HTTPS 在前面时，不要把服务开放给其他电脑（见 [security.md](./security.md)）：公司账号的密码会被提交到本服务，而且在 Redis 中是明文保存的。

### 已知限制

- Redis 用户目录是针对进程内的模拟服务和 Python 生成的 pickle 数据测试的，没有针对公司自己的 Redis 测试过。请对它运行一次 `check-redis-users`。
- 匿名访问者可以看到每个监控文件夹及其历史的名称、大小、修改时间和结构，但看不到内容。
- 放在反向代理之后时，所有访问者在登录限制中共用同一个地址，因为不信任 `X-Forwarded-For`。十次错误尝试就能把一个账号名锁定十分钟，本地管理员也不例外。
- 匹配到超过 200 个 Redis 键的登录会因为有歧义而被拒绝。同一工号的陈旧重复记录不会被消解。
- 没有保留策略，所以发现变化的自动运行会永久增加恢复点。无法读取的、设置了计划的文件夹，每个间隔会产生一个失败的任务和一条审计记录。
- 手动备份请求如果发现已有排队或运行中的自动备份，会并入该备份。如果那次运行没有发现变化，它的任务会被删除，此后 `GET /api/v1/jobs/{id}` 返回 `404`。
- 两个管理员同时编辑同一个监控文件夹：以最后一次保存为准。

完整的列表见 [security.md](./security.md) 和 [mvp-status.md](./mvp-status.md)。

## 0.1.0（MVP）

在本地 NTFS 文件夹上的垂直切片：浏览、备份、保留历史、校验和恢复；一个管理员，只能手动启动，端口为 `8470`。做了什么、没做什么，见 [mvp-status.md](./mvp-status.md)。
