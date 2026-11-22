# English

## Overview

What changed in each version of TimeVault, and what to do when upgrading. The newest comes first.

## 1.0.1

Two changes of how TimeVault is built and run, and nothing new in the pages that people use: TimeVault builds its own **NTFS index** instead of asking Everything ([ADR 0018](./adr/0018-native-ntfs-index-replaces-everything.md)), and the operating settings move from `.env` into the **database**, where only the local administrator edits them on a **Site settings** page ([ADR 0017](./adr/0017-site-settings-in-the-database.md)). The limits of [mvp-status.md](./mvp-status.md) stand otherwise (backups read the live disk, no VSS, no TLS of its own). **This is not a drop-in upgrade:** the settings that were in `.env` are not carried over, and the service has to run with administrator rights for search. Read the upgrade notes before a server is updated. Migration `0006_site_settings` creates one table.

### Added

- **The NTFS index.** `internal/ntfsindex` builds, for every drive letter that has monitored folders, an in-memory tree of the files and folders below them from the file records of the NTFS volume (`FSCTL_ENUM_USN_DATA` for the folders, `FSCTL_QUERY_FILE_LAYOUT` for names, sizes and times; names are decoded only for what is inside the monitored folders), and keeps it current from the change journal (`FSCTL_READ_USN_JOURNAL`, read every 10 seconds and before a question when it was not read in the last 2 seconds). It answers a search below a folder (a name that contains every term, ignoring case; folders first, then by name; pages of at most 200), the folders directly inside a folder with the size of everything below each, and a probe for the scheduler (how many entries are below a folder, and whether anything below it changed since a time, which a time from before the index began to watch counts as). `search.NTFS` makes it the `Provider`, `FolderLister` and `ChangeProber` of the service. A journal that was replaced or overwritten, a folder that was moved into a monitored folder from outside, and a change of the monitored folders of a volume make the volume be built again (the second at most once a minute); a volume that could not be indexed is tried again after 30 seconds. Measured on the development computer: 2.0 million entries in 13 seconds, about 230 bytes of memory per entry, a search below a tree of 1.75 million entries in about 0.13 seconds. See [Search index](./runbook.md#search-index).
- **Site settings.** A page, `/settings/site` (**Settings → Site settings**), the endpoints `GET` and `PATCH /api/v1/site-settings` and `POST /api/v1/site-settings/restart`, and the command `timevault-server settings list|get|set|reset`. 26 settings in seven groups (address and display, sign-in, performance, scheduler, search index, automatic updates, logging) are stored in the table `site_settings` and read and changed only by the local administrator: anybody else who is signed in is told that there is no such page (`404`) and the attempt is audited. A value that equals its default is not stored. The values are checked together with the settings of `.env` that they depend on before they are saved. A saved setting is used after a restart: the page says which settings are not in use yet and offers **Restart now** (the restart of an automatic update; refused while a job runs). A change is audited as `site.update` and a restart as `site.restart`, with the names of the settings and never their values, and only the local administrator sees those events. See [Site settings](./runbook.md#site-settings).
- **Why the search is not available.** The Status page and `GET /api/v1/health` say why: the service has no administrator rights, the index of a volume is being built, a volume has no change journal (with the command that creates one), a volume is not NTFS, or another failure that the log names. `search_index` can switch the index off.
- **Migration `0006_site_settings`:** the table `site_settings` (`key`, `value`, `updated_at`, `updated_by`).
- **Tests.** `internal/ntfsindex` is tested with a model of a volume (building, searching, sizes, the probe and the journal: create, write, rename, move, delete, a journal that is applied twice, a lost journal, a folder moved in) and, on Windows from an elevated shell, against a real volume (`TestWindowsVolumeMatchesAWalkAndFollowsTheJournal`); `internal/search` tests the provider; `internal/config`, `internal/sitesettings`, `internal/auth` and `internal/server` test the Site settings (the registry, the lookup that ignores the environment, the checks, who may open the page, saving, the API, the restart, the translations); `cmd/timevault-server` tests the command line.

### Changed

- **Search and folder sizes come from the index, not from Everything.** Search is plain text (no operators; `*` and `?` separate terms), the results come folders first and then by name, and the same order on every page. The size of a folder is the sum of the lengths of the files below it. The sizes of folders are cached for 10 seconds as before.
- **The scheduler's change probe asks the index.** `scheduler_probe` (it was `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE`) is off by default. A folder is also scanned when the index has not been watching the volume since 5 minutes before the last scan, so a start or a rebuild costs one more scan of each folder before skipping resumes (more for an interval under 5 minutes).
- **`.env` holds only what the service needs before it reaches its database, and the secrets:** the database URL, `TIMEVAULT_AUTO_MIGRATE`, the listen address, the backup and restore folders, `TIMEVAULT_AUTH_DISABLED`, the local administrator, the session key and the Redis of the user directory. Every other setting is a Site setting (the table below). A Site setting that is still in `.env` or in the environment is ignored, and the start-up log names it. `configs/timevault.example.env` and the development scripts follow (`dev-run.ps1` says that the index needs an elevated shell).
- **The service starts in two steps:** the bootstrap settings, a small database pool, the migrations, the Site settings, the validation of everything together and then the real pool. Stored settings that are not valid together stop it with a message that names the problems.
- **The Settings page** shows the version (and, for the local administrator, a link to the Site settings page) where it showed the whole running configuration; the configuration moved to the Site settings page. `GET /api/v1/settings` gives everybody the version, `server_host`, `restore_display_root` and `update`, and the rest to the local administrator (`search` is now `search_index`).
- **`check-config` checks the bootstrap settings only,** so that the check that a service runs before it replaces itself does not depend on the database.
- **The service runs with administrator rights** for the index. Without them it still runs: search is unavailable, folder sizes show "-" and the scheduler scans as before.
- **The Status page no longer lists USN as disabled** (the change journal is used now); VSS is still listed as disabled.
- The version number is 1.0.1 (the footer of every page, the Settings page, `GET /api/v1/settings`, `timevault-server version`). `golang.org/x/sys` became a direct dependency of `go.mod` (it was indirect).

### Removed

- The Everything adapter, its settings `TIMEVAULT_EVERYTHING_URL`, `TIMEVAULT_EVERYTHING_USERNAME`, `TIMEVAULT_EVERYTHING_PASSWORD` and `TIMEVAULT_EVERYTHING_TIMEOUT`, the setting `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE`, `scripts\dev-everything.ps1`, the settings `TIMEVAULT_EVERYTHING_EXE` and `TIMEVAULT_TEST_EVERYTHING_*` of the scripts and live tests, and the live tests against Everything. Nothing of Everything is used or needed any more.

### Upgrade notes

1. **Write down the Site settings of `.env` before you update.** The new program ignores them from its first start, and nothing is copied. These are the old names and the new keys:

   | `.env` of 1.0.0 | Site setting of 1.0.1 |
   |---|---|
   | `TIMEVAULT_SERVER_HOST`, `TIMEVAULT_RESTORE_DISPLAY_ROOT`, `TIMEVAULT_AVATAR_URL`, `TIMEVAULT_BASE_PATH`, `TIMEVAULT_TRUSTED_PROXIES` | `server_host`, `restore_display_root`, `avatar_url`, `base_path`, `trusted_proxies` |
   | `TIMEVAULT_SESSION_TTL`, `TIMEVAULT_COOKIE_SECURE`, `TIMEVAULT_SSO_HOST`, `TIMEVAULT_SSO_REQUIRE_SAME_IP` | `session_ttl`, `cookie_secure`, `sso_host`, `sso_require_same_ip` |
   | `TIMEVAULT_JOB_WORKERS`, `TIMEVAULT_HASH_WORKERS`, `TIMEVAULT_PROCESS_PRIORITY` | `job_workers`, `hash_workers`, `process_priority` |
   | `TIMEVAULT_SCHEDULER_PARALLEL`, `_GAP`, `_REST`, `_FULL_SCAN_EVERY` | `scheduler_parallel`, `scheduler_gap`, `scheduler_rest`, `scheduler_full_scan_every` |
   | `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` | `scheduler_probe` (needs the index; see note 4) |
   | `TIMEVAULT_EVERYTHING_URL`, `_USERNAME`, `_PASSWORD`, `_TIMEOUT` | gone; the new `search_index` is `auto` or `off` |
   | `TIMEVAULT_UPDATE_URL`, `TIMEVAULT_UPDATE_INTERVAL`, `TIMEVAULT_UPDATE_RESTART` | `update_url`, `update_interval`, `update_restart` |
   | `TIMEVAULT_LOG_LEVEL`, `TIMEVAULT_LOG_FORMAT` | `log_level`, `log_format` |
   | `TIMEVAULT_WEB_LOG`, `TIMEVAULT_WEB_LOG_KEY`, `TIMEVAULT_WEB_LOG_HOST` | `web_log`, `web_log_key`, `web_log_host` |

   These stay in `.env`: `TIMEVAULT_DATABASE_URL`, `TIMEVAULT_AUTO_MIGRATE`, `TIMEVAULT_LISTEN_ADDR`, `TIMEVAULT_ALLOW_NON_LOOPBACK`, `TIMEVAULT_REPOSITORY_PATH`, `TIMEVAULT_RESTORE_ROOT`, `TIMEVAULT_AUTH_DISABLED`, `TIMEVAULT_ADMIN_USER`, `TIMEVAULT_ADMIN_PASSWORD_HASH`, `TIMEVAULT_SESSION_KEY`, `TIMEVAULT_REDIS_URL`, `TIMEVAULT_REDIS_KEY_PREFIX`, `TIMEVAULT_REDIS_TIMEOUT`.
2. **After the update, enter the values** on **Settings → Site settings** as the local administrator (`TIMEVAULT_ADMIN_USER` with the password whose hash is in `TIMEVAULT_ADMIN_PASSWORD_HASH`; a server without a local administrator can only be changed with `timevault-server settings`), then press **Restart now** or restart the service. Delete the old lines from `.env`: the start-up log names every one that is still there.
3. **Two settings matter at once.** A server that updates itself loses `update_url`, so **the update that brings 1.0.1 is the last one it installs by itself until `update_url` is entered again** (and `update_interval` and `update_restart` return to their defaults, 10 minutes and `supervise`). A site that is served under a path behind a reverse proxy (`base_path`, for example `/tv`) has broken links until `base_path` is entered again; open the Site settings page on the server's own address (`http://127.0.0.1:8660/settings/site`, which needs no prefix) or run `timevault-server settings set base_path /tv` and restart. `trusted_proxies`, `sso_host` and `cookie_secure` are lost the same way.
4. **Run the service with administrator rights for search,** for the sizes of folders and for skipping scans of unchanged folders: from an elevated PowerShell, or, not tried, as a scheduled task that runs with the highest privileges or as the account SYSTEM. Without them the Status page says "The service has no administrator rights, so it cannot read the volumes to index them", search is unavailable, folder sizes show "-", and every due folder is scanned as before. The monitored folders must be on local NTFS drive letters; a folder on a share is not indexed. `scheduler_probe` is off by default: switch it on (and restart) when the index runs. Read [security.md](./security.md): the whole service, its web server included, runs elevated.
5. **Migration `0006`** is applied at start when `TIMEVAULT_AUTO_MIGRATE=true` (a service that updates itself has it) or by `timevault-server migrate`. It only adds a table. Going back to 1.0.0 on the migrated database should work (the older program looks only for the migrations it knows, and ignores the table; this was not tried), but then its `.env` has to hold the settings again: keep a copy of the old `.env`.
6. **Everything is no longer needed.** Stop and uninstall the Everything instance if nothing else uses it.
7. **To release:** the version in `cmd\timevault-server\main.go` is already 1.0.1, so run `.\scripts\release.ps1` and copy `timevault.exe`, then `timevault.json`, to the release folder (the servers that update themselves find it; see note 3 about what they lose).

## 1.0.0

The first official release. It is still the vertical slice that [mvp-status.md](./mvp-status.md) describes, and its limits stand (the live disk, no VSS or NTFS USN journal, no TLS of its own). Three things are new: the console log names the user and the address of every request; the service writes a **web log** to the Redis list that the company's other applications share, which is the first time TimeVault writes to Redis ([ADR 0016](./adr/0016-web-log-in-the-shared-redis-list.md)); and every page but the sign-in page has a **Documentation** button (the user guide) and a **Guide** button (a step-by-step tour of the page). No migration and no setting is required. Nobody has published 1.0.0 yet: `.\scripts\release.ps1` builds the release, and publishing it to the folder that the servers read is the administrator's decision.

### Added

- **The web log in Redis.** With `TIMEVAULT_REDIS_URL` set, the service appends lines to the list `web_log` of that Redis (the same database), the list that the company's other applications write their web log to. A line is `ip | YYYY-MM-DD HH:MM:SS.ffffff | message | status | level | host`, for example `10.0.0.5 | 2026-11-18 14:05:12.250431 | TimeVault: /explorer user=E123456 0.112(s) | 200 | info | sgmeslt595`, and an element of the list is `zlib.compress(pickle.dumps(line, protocol=4))`, written the way Python writes it, so what reads the lines of the other applications reads these too. TimeVault never adds a field: the account is in the message as `user=<employee ID>` (`guest` for a visitor who has not signed in). The address is that of the browser (`127.0.0.1` for what the service does on its own), the time is the local time of the server, and the last field is `TIMEVAULT_WEB_LOG_HOST` or, by default, the computer name in lower case. What is written:
  - **A page that somebody opened:** `TimeVault: <route> user=<employee ID or guest> <seconds>(s)`: the route that matched (`/explorer`, `/jobs/12`; never the address as it was typed: no query, no path, no search text, and `(not found)` for an address that matched nothing), the user and how long the page took to build. `info`, or `warning` for a `4xx`. Not written: static files, `/healthz`, API calls, redirects, and the reloads that the page script makes by itself while a job that the page shows runs or when it has finished (the request header `X-TV-Auto: 1`).
  - **A request that failed on the server** (any `5xx`, the API too, and a panic): `TimeVault: error <route> user=<employee ID> ref=<reference>`, `error`. `ref` is the reference that the error page shows and that the console log holds as `id=` together with the error text; the error text itself is never written to Redis.
  - **Every audit event,** once it is stored in the database: what people do, and what the scheduler and the updater do. For example `TimeVault: backup.request source=3 user=E123456`, `TimeVault: backup.finish source=3 point=17 user=scheduler 12.400(s)` and `TimeVault: access.denied method=POST route=/settings/sources user=E1001`. `ok` is `info` (status 200), `denied` is `warning` (403) and `failed` is `error` (500). The name that was typed at a failed sign-in, which may be a password, is never copied (`user=-`).
  - **The scheduler and the service:** `TimeVault: scheduler.queued source=<id> user=scheduler`, `TimeVault: scheduler.error [source=<id>] user=scheduler` (a warning, at most once per folder and minute, like the warning in the console), `TimeVault: started version=1.0.0` once everything is built, and `TimeVault: stopped` when the service stops.
  - **Only with `TIMEVAULT_LOG_LEVEL=debug`:** the page line of API calls (the polling of a job) and of the automatic reloads, and `scheduler.skipped` (the search engine saw no change). The other log levels change nothing in the web log.
- **The web log is best effort and never gets in the way.** `Write` only puts a line into a queue of 1,024, and a worker sends up to 100 lines with one `RPUSH`. A flood is cut at the door (page lines: 10 per second on average, a burst of 100; event lines: 20 per second, a burst of 200), a full queue drops the line, and after a failed `RPUSH` the worker leaves Redis alone for 30 seconds, then longer, up to 5 minutes, and drops what comes meanwhile; it says so with one warning, with the password of the URL masked. At shutdown the queue is sent first, for at most 3 seconds. TimeVault never trims or expires the list: it is shared, and the other applications manage it. Nothing that a visitor typed, no path, no file or folder name and no error text is written: only routes, numbers, employee IDs and addresses. The details are in [Web log](./runbook.md#web-log).
- **Three settings:** `TIMEVAULT_WEB_LOG` (`auto`, the default, is on exactly when `TIMEVAULT_REDIS_URL` is set; `on` needs it; `off`), `TIMEVAULT_WEB_LOG_KEY` (default `web_log`; it must not start like the keys of the accounts or of the single sign-on results) and `TIMEVAULT_WEB_LOG_HOST`. The start-up log has `web_log`, `web_log_key` and `web_log_host`, and, when the web log is on, a second line, `web log is on`, with the list, the host name and the Redis as `host:port/db`, never the password. The Settings page has a **Web log** row in the **Service** card, and `GET /api/v1/settings` has `web_log` (`enabled`, `key`, `host`).
- **Two round buttons: Documentation and Guide.** Next to the arrows at the bottom right, which still appear only on a long page, every page except the sign-in page has two buttons that are always there. **Documentation** (a book) opens the user guide in a new tab. **Guide** (a compass) starts a step-by-step tour of the page you are on: it dims the page, lights up one part at a time and shows a bubble with a title, a text, "Step n of N", **Back**, **Next** or **Done**, and a close button. The parts of the page itself come first, then the header (the navigation, the theme, the language, the account or the sign-in) and the round buttons. The right and left arrow keys go on and back, **Esc** closes the guide, and **Enter** presses the button that has the focus; clicking the dimmed page does nothing, and nothing is clicked or changed for the visitor. On a narrow screen (640 pixels or less) the bubble is docked at the bottom (at the top for the round buttons). The guide ends when another page is shown or the back button is used, and it goes on when the same page refreshes itself, as the job page does while its job runs. A part that is not shown has no step. It is `web/assets/js/tour.js` (no library, no inline script or style); the texts are in English and Chinese (`internal/server/i18n_tour.go`).
- **`GET /help`, the user guide.** A page of the application, open to everybody, in English and Chinese (the language of the EN / 中文 switch): what TimeVault is for and how each page is used. It is `web/templates/help.html`.

### Changed

- **The `request` line of the console log has the address and the user:** `ip=<address of the browser> user=<employee ID, or guest> method=… path=… status=… ms=…`. The address is the connection, or the browser address that a proxy of `TIMEVAULT_TRUSTED_PROXIES` reports. The user is read from the session cookie only, without asking the user directory; a visitor who has not signed in is `guest`, and the request that signs somebody in (`POST /login`, `POST /auth/sso/poll`) is logged as the account that signed in. Static files and `/healthz` are still not logged.
- **The lines `request failed` and `panic while handling a request`** carry the same `ip`, `user`, `method` and `path`, and `id`, the reference that the error page shows. A request that panicked now also gets its normal `request` line, with `status=500` (the recoverer now sits inside the request log).
- **The lines `job finished` and `job finished with an error`** carry `by=<who requested the job>` (`scheduler` for an automatic backup).
- **`TIMEVAULT_BASE_PATH` may not start with `/help`:** it is a page of the application now, and the service refuses to start with such a prefix.
- The version number is 1.0.0 (the footer of every page, the Settings page, `GET /api/v1/settings`, `timevault-server version`); `go build` is unchanged.
- **In the code:** `redisuser.Connect` and `redisuser.Conn` hand a Redis client with the same settings to the writer of the web log (`internal/weblog`); `internal/redisuser` itself still only reads. `audit.Logger.SetMirror` hands every event, after it is stored, to the web log. `service.Config.WebLog` and `server.Options.WebLog` take a `weblog.Sink`. `Page.Tour` writes the steps of the guide into the templates.

### Upgrade notes

1. **Nothing is required:** no migration, no new required setting, and the service runs as before.
2. **The service now writes to Redis.** With `TIMEVAULT_REDIS_URL` set, the web log is **on by default**: a service that is upgraded from 0.1.x starts to append to the list `web_log` in that Redis, also when it updated itself. To switch it off put `TIMEVAULT_WEB_LOG=off` into the `.env` (older versions ignore a setting they do not know, so it can be added before the release is published); `TIMEVAULT_WEB_LOG_KEY` and `TIMEVAULT_WEB_LOG_HOST` change the list and the host name in the lines. The Redis account of `TIMEVAULT_REDIS_URL` has to be allowed to `RPUSH` to the list; if it is not, the service logs the warning `web log: the lines cannot be written to Redis` and drops the lines, and nothing else stops. After the first start look at the **Web log** row of the Settings page and at the start-up line `web log is on`. A `TIMEVAULT_REDIS_KEY_PREFIX` that `web_log` starts with makes the default key invalid (the service refuses to start): set another `TIMEVAULT_WEB_LOG_KEY`, or `off`.
3. **The console lines have new fields.** After `msg=request` come `ip=…` and `user=…`, before `method`; `request failed` and `panic while handling a request` have them too, and the job lines have `by=`. Anything that parses these lines by position has to be adapted; anything that reads them by name keeps working.
4. **To release:** the version in `cmd\timevault-server\main.go` is already 1.0.0, so run `.\scripts\release.ps1` and copy `timevault.exe`, then `timevault.json`, to the release folder that the servers read. A service that runs 0.1.8 or later and has `TIMEVAULT_UPDATE_URL` finds it within its interval (**Check now** on the Settings page looks at once), waits for its running jobs and restarts into 1.0.0: a version that differs is an update. Decide about the web log (note 2) before you publish.
5. The `.env` template (`configs/timevault.example.env`) has the three new settings.

### Known limits

- **The web log is best effort.** A full queue, the limits and a Redis that does not answer drop lines, and a dropped line is not written later. An audit event reaches the web log only after it is stored in the database, so an event that could not be stored is not in it. It does not replace the audit trail in PostgreSQL.
- **TimeVault never trims or expires the shared list,** and each line is about 100 to 150 bytes in Redis. The volume is bounded (at most 10 page lines and 20 event lines per second on average) but not zero, anybody who can reach TimeVault can cause page lines, and the Redis has no `maxmemory` limit. Cutting the list is for whoever manages that Redis.
- **The lines hold employee IDs and client addresses,** and everybody who can read the list reads them. No path, no file or folder name, no typed text and no error text is written.
- The texts of the guide and the user guide exist in English and Chinese only, like the rest of the interface.
- Checked with automated tests, and the guide in a real browser against the test harness (all pages, English and Chinese, light and dark theme, desktop and 390 pixels wide); not on the production server: the web log was not written to the production Redis, the compiled program was not run against the production database and Redis, and that the log page of the other application shows these lines was not checked (see [mvp-status.md](./mvp-status.md)). The known limits of the earlier versions stand.

## 0.1.10

Only the version number changes: in everything else the program is the one of 0.1.9. The version exists to try the automatic update from one release to the next on a server (see [Releasing and automatic updates](./runbook.md#releasing-and-automatic-updates)).

### Changed

- The version number is 0.1.10; `go build` is unchanged.

### Upgrade notes

1. **Nothing has to be done:** no migration, no new setting, and the service runs as before.
2. **To try the update,** make the release with `.\scripts\release.ps1` and copy `timevault.exe`, then `timevault.json`, to the release folder that the servers read (`static\cd` of the other application when `TIMEVAULT_UPDATE_URL` is `http://<server host>/api/static/cd/timevault.json`). A service that runs 0.1.9 and has its address set finds the release within its interval (10 minutes by default; **Check now** on the Settings page looks at once), waits for its running jobs and restarts into 0.1.10. The program it replaced stays next to it as `timevault.old-0.1.9.exe`.
3. **To run the test again,** publish the 0.1.9 release (its `timevault.exe`, then its `timevault.json`) in the same way: a version that differs is an update, in either direction. The script only builds the version that the source has, so keep a copy of the two files of each release.

### Known limits

- Checked with compiled programs against a scratch database and a stand-in for the static files, not on the production server (see [mvp-status.md](./mvp-status.md)). The known limits of 0.1.8 and 0.1.9 stand.

## 0.1.9

The pictures of the accounts show now, and the release has a place of its own. The cause of the missing pictures was found with the real application: it sends the `.webp` pictures as `text/plain`, and 0.1.8 only accepted `image/*`. The decisions and the alternatives are in [ADR 0015](./adr/0015-account-pictures-are-read-by-the-browser-from-their-host.md).

### Fixed

- **The pictures of the accounts were never shown (0.1.8).** The application that keeps them (the static files of the other company applications) sends a picture with `Content-Type: text/plain`, which an image does not mind but the script of 0.1.8 did not accept, and it answers a missing picture with `202` and a placeholder picture of 480×320 pixels. The script now loads each picture as an image: an error, or a picture of exactly that size, leaves the default picture. A missing picture is remembered for ten minutes (it was the whole browser session, which kept a host that had been down from being tried again).

### Changed

- **The pictures are read from `TIMEVAULT_SERVER_HOST`.** `TIMEVAULT_AVATAR_URL` is now an `http(s)` address, or a path on the host the site is opened from (the meaning it had in 0.1.8), or `off`. Not set, it is `http://<TIMEVAULT_SERVER_HOST>/api/static/hr/{id}.webp`, and there are no pictures while the server host is not set either. The browser reads them, not the service, so they show whatever address the site is opened by.
- **The content security policy** of every page lists the host of an absolute picture address in `img-src`, and nowhere else. A host name with an underscore (`prod_eng.co.example.local`) cannot be written in a policy, so the wildcard of its domain is listed instead (`http://*.co.example.local`); when no domain of two labels is left, the start-up log warns that the pictures only show when the site is opened by that same address.
- The version number is 0.1.9; `go build` is unchanged.

### Documented

- **Where the release goes.** With the static files of the other company applications, the manifest is `http://<server host>/api/static/cd/timevault.json`, and a `package_url` that is a file name leads to `http://<server host>/api/static/cd/<name>` (the service has always looked it up next to the manifest; a test now covers this layout, and a `202` for a file that is not there). The address stays an explicit `TIMEVAULT_UPDATE_URL` in the `.env`: it is not derived from `TIMEVAULT_SERVER_HOST`, so updates are only ever on because the `.env` says so.
- `configs/timevault.example.env` no longer says that a scheduled task that "repeats on failure" restarts the program after an update in the mode `exit`: the Task Scheduler setting "If the task fails, restart" did not (it was tried); a trigger that repeats every minute with "Do not start a new instance" does.

### Upgrade notes

1. **Pictures:** with `TIMEVAULT_SERVER_HOST` set, nothing else is needed. If the pictures are on the host of the site but `TIMEVAULT_SERVER_HOST` is not set, write `TIMEVAULT_AVATAR_URL=http://<host>/api/static/hr/{id}.webp`. A `.env` that sets the 0.1.8 default itself (`TIMEVAULT_AVATAR_URL=/api/static/hr/{id}.webp`) keeps working as it did, from the host the site is opened from.
2. **Automatic updates:** nothing changes. Put `TIMEVAULT_UPDATE_URL=http://<server host>/api/static/cd/timevault.json` (with `TIMEVAULT_AUTO_MIGRATE=true`) into the `.env`, and copy `timevault.exe`, then `timevault.json`, to the `static\cd` folder of the other application. A server that runs 0.1.8 with its address set updates itself to 0.1.9 from there.
3. No migration and no new required setting.

### Known limits

- The pictures are read in the clear over `http`, and the host of the pictures sees the address of every browser that shows one. A site served over `https` needs an `https` picture address.
- A person without a picture keeps the default picture only while the placeholder of the other application has the size it has today (480×320 pixels); if that application changes it, the placeholder is shown until the size in `web/assets/js/app.js` is changed.
- Checked with automated tests, with compiled programs against a scratch database, and in a real browser against the application that keeps the pictures on the computer of the build (see [mvp-status.md](./mvp-status.md)); not on the production server and not through the company's other nginx servers.

## 0.1.8

Pictures of the accounts, and a program that updates itself. A build now writes a **release folder** (`timevault.exe` and `timevault.json`); a service that is told where that folder is reads it, downloads a new version, waits for its running jobs to finish and restarts itself. The decisions and the alternatives are in [ADR 0014](./adr/0014-self-update-from-a-release-manifest.md).

### Added

- **Pictures of the accounts.** The header, the audit log (the actor), the job page and the lists of jobs (who asked), the summary of who has access to a folder and the people lists of the permission editor show the picture of an account next to its name. The picture is read from `/api/static/hr/<employee ID>.webp` on the same host (the pictures of the employees that the other company applications keep), set by `TIMEVAULT_AVATAR_URL`, where `{id}` is the employee ID and `off` turns the pictures off. Whoever has no picture, or whose picture cannot be read, keeps the default picture. The host of the pictures answers `202` with a placeholder for a missing one, so only a `200` with an image counts; the browser asks for each picture once, only when it is about to be seen, and remembers a missing one for the session. The local administrator and the names the service uses itself (`scheduler`, `updater`, `system`) have none. The content security policy is unchanged.
- **`scripts\release.ps1`** writes the release folder (default `<repository>\release`, git-ignored): `timevault.exe`, and last `timevault.json` with `latest_version`, `sha256`, `package_url` and `size`. It runs `go vet`, builds without `-X`, takes the version from the program itself, and warns when the version is the one of the release that is already there. `-BaseUrl` writes a full `package_url`; `-OutDir`, `-ExeName` and `-SkipChecks` are the other parameters.
- **Automatic update.** With `TIMEVAULT_UPDATE_URL` (an `http(s)` address, or a path of a folder or a share; empty means off), `TIMEVAULT_UPDATE_INTERVAL` (default `10m`, `1m` to `24h`) and `TIMEVAULT_UPDATE_RESTART` (`supervise` by default, or `exit`) the service reads the manifest about 10 seconds after its start and at every interval. When `latest_version` is not the running version it downloads the program next to itself (size and sha256 must match), runs it with `version` and with the new command `check-config`, stops taking jobs, waits until the running jobs are through (no time limit), swaps the file by renaming (the old program stays as `timevault.old-<version>.exe`, the newest three are kept), stops, and starts the new program. With `supervise` the process that was started stays as a small launcher that runs the new program as its child (a console window and a scheduled task keep seeing one program; Ctrl+C stops both; when the launcher is killed, for example by ending the task, the program stops too); with `exit` the process ends with the code 75 for a service manager that restarts it. The new program notes that it took over (log and audit).
- **`timevault-server check-config`** checks the settings (the environment and the `.env` file) as `serve` does and connects to nothing; exit code 1 and the reasons when they are not valid.
- **The Settings page has a card, "Program updates":** the state (up to date, checking, downloading, waiting for the running jobs, installing, problem), the version that runs, the released version, when the manifest was last read and how often. Administrators also see the address of the release and the last problem, and a **Check now** button (`POST /settings/update/check`, `POST /api/v1/update/check`).
- **A notice under the header** tells every signed-in account, while the service waits to restart, that a version will be installed as soon as the running jobs have finished and that new jobs wait until then; the script keeps it right while people move between pages.
- **Audit:** the action **Program update** (`app.update`, actor `updater`) with the stage (`staged`, `restart`, `done`, `cancelled`, `failed`) in the detail, with no path and no error text.
- **API:** `update` in `GET /api/v1/settings` (`enabled`, `state`, `latest_version`, `check_interval`, `waiting_jobs`, `checked_at`; for administrators also `manifest` and `error`), `POST /api/v1/update/check`, and the `X-TV-Notice` header on partial pages.
- **Job runner:** `Drain`, `Resume` and `Running`. While the runner is drained, queued jobs stay queued.

### Changed

- **A service with `TIMEVAULT_UPDATE_URL` must have `TIMEVAULT_AUTO_MIGRATE=true`** and refuses to start otherwise, because a new version may change the database and nobody runs `migrate` when the service updates itself.
- The summary of who has access on the Settings page and the people lists of the permission editor name accounts through the same partials as the other pages, so they show the pictures.
- The version number is 0.1.8; `go build` is unchanged.

### Upgrade notes

1. **Nothing has to be done** to keep the way a service runs: without `TIMEVAULT_UPDATE_URL` it never updates itself, and without a reachable picture host everybody has the default picture. No migration, no new required setting.
2. **To turn automatic updates on at a server** (once, by hand: older versions cannot update themselves): copy `timevault.exe` of 0.1.8 next to the `.env`, add `TIMEVAULT_AUTO_MIGRATE=true` and `TIMEVAULT_UPDATE_URL=<release folder>`, give the account of the service **Modify** on the folder of the program and **Read** on the release folder, start it. From then on a release is made with `scripts\release.ps1` and copied (program first, manifest last) to the release folder. See [Releasing and automatic updates](./runbook.md#releasing-and-automatic-updates).
3. **If a service has to go back,** put the older release in the release folder first (or empty `TIMEVAULT_UPDATE_URL`), or it updates itself to the newer one again. The previous program is `timevault.old-<version>.exe`.
4. **Pictures:** behind the company's nginx `/api/static/hr/<employee ID>.webp` is on the same host as `/tv/`, so they appear without any setting. Reaching the service directly on its own port gives the default picture. Set `TIMEVAULT_AVATAR_URL=off` to turn them off.
5. The `.env` template (`configs/timevault.example.env`) has the new settings.

### Known limits

- **No signature.** Whoever can write the release folder decides what runs on the server; the size and the sha256 only protect against a damaged or half-copied file ([security.md](./security.md)). Use a folder that only administrators can write, and `https` for an address.
- **No automatic way back.** If a new program passes the checks and then fails (a migration, a folder that is gone), the service stays down and the log says why. Going back is by hand, and a migration that was applied stays.
- **The wait for running jobs has no limit,** and while it lasts nothing starts, the scheduler's backups included.
- **The launcher of `supervise` mode keeps the code it was started with** and the old program file stays locked while it lives; a change to the launcher needs a restart of the launcher.
- Versions are compared for equality only, so a manifest with an older version is a downgrade.
- Checked with automated tests and with compiled programs against a scratch database (see [mvp-status.md](./mvp-status.md)); not on the production server, not through the company's nginx, and not with the real picture host.

## 0.1.7

What an account may do is now decided for each monitored folder: **Browse**, **Back up** (back up now, including the full re-read) and **Full control**, given to guests, to everybody who is signed in, or to single employee IDs, and set on the Settings page together with the folder. Administrators have full control of every folder. A folder that a visitor may not browse is not there for them at all. The decisions and the alternatives are in [ADR 0013](./adr/0013-per-folder-access-levels.md).

### Added

- **Three levels per folder.** Each includes the ones before it. Browse: see the folder, its history, jobs and audit events, and restore from it (which needs a signed-in account). Back up: also **Back up now**, **Full re-read**, **Verify** and stopping the jobs of the folder. Full control: also change the folder's name, display folder, exclude patterns, on or off, schedule, versions kept and who may do what with it; not the folder on the server's disk, and not adding folders (administrators only). Guests can only be given Browse.
- **Who may do what** on the Settings page: three lists per folder (Browse, Back up, Full control) with check boxes for **Guests (not signed in)**, **Everyone who is signed in** and the people of the user directory (by name and employee ID, with a box to find one). They are in the form that adds a folder (administrators) and in the form that edits it (administrators and whoever has Full control); the forms are built when asked for (`/settings?add=1`, `/settings?edit=<id>`).
- **Hidden folders.** A folder that a visitor has no level on is missing from the Explorer, Time Machine, Backups, Settings, Audit and job pages and from the API, and asking for it directly gives the same "not found" as for a folder that does not exist (a visitor who has not signed in is told that signing in may show it). A refusal for a folder that can be seen is a `403`, and the audit log shows it as **Access denied** with the folder and the level that was needed.
- **API:** `level` (and, for those with Full control, `access`) in `GET /api/v1/sources`; `access` in `POST /api/v1/sources` and `PATCH /api/v1/sources/{id}`; `confirm_lockout` and the error `lockout_not_confirmed`. A person in `access` may be written `user:<employee ID>`.
- **A warning before somebody gives up their own Full control.** When somebody who is not an administrator saves lists that no longer give them Full control, the form asks for a tick and the API answers `409 lockout_not_confirmed` until `confirm_lockout` is sent.
- **A revision of the lists** (`access_rev`) in the edit form, in `GET /api/v1/sources` (for those who have Full control) and in `PATCH /api/v1/sources/{id}`. A form that did not change the lists leaves them alone, so saving a schedule cannot bring back grants that somebody else took away while the form was open; a form or a request that changes them is refused (`409`, `access_changed`) when somebody else has changed them since, and the form comes back with the lists as they are now and with what else was typed.
- **Migration `0005_source_access`** (table `source_access`), and the list of the active accounts of the Redis user directory for the editor (`Directory.Accounts`, kept for a minute).

### Changed

- **Rights are per folder.** The rule that every signed-in account may back up, verify, cancel jobs and restore on every folder is gone; each folder says so. A restore needs Browse on the folder and a signed-in account.
- **What is shown is cut to the visible folders:** the Explorer and Time Machine selectors, the Backups, Settings, Audit and job pages and their API, the counts of queued and running jobs on the Status page, and the table that hides the server's real folders in text. Events that belong to no folder (sign-ins and the like) stay visible to every signed-in account, as before. The audit record of a refused request holds the route, not the path that was typed.
- **The pages that show a folder check a signed-in visitor with the user directory** (as the pages that change something already did), so an account that was deactivated or demoted is a guest at once.
- **A change of a folder** checks the grants again in its own transaction, after it has locked the row, so a Full control that was revoked meanwhile cannot undo its own revocation. Somebody who is not an administrator can never write the folder on the server's disk (`root_path` from them is `403`).
- **Restore keys.** Repeating a restore with the same `idempotency_key` returns the original job only to the account that started it for that folder; a key that somebody else used is `409`.
- **Audit.** The events about a folder (a refusal, a stopped job, the creation and the change of a folder) carry the folder; older `job.cancel` events are attributed through their job. Everybody but administrators sees the events of the folders they may browse and the events that belong to no folder.
- **The Settings page** shows the level on each folder, and the **Edit** button only where the visitor has Full control. Enter in the box that finds a person no longer saves the form.
- **Who may do what is for those who may change it**, also in the audit log: the lists in the details of the events that create a folder or change its lists (`access`) are `[hidden]` for those who only browse or back up the folder.
- **A recovery point that is not one of the folder** in a restore preview or start is answered `404`, like a number that does not exist; it used to be a `400` that told that the point exists in another folder.

### Upgrade notes

1. **Apply the migration:** `timevault-server migrate`, or start with `TIMEVAULT_AUTO_MIGRATE=true`. It gives every folder that exists what everybody could do until 0.1.6 (guests browse, everybody who is signed in may back up and restore), so nothing changes until an administrator narrows it with **Edit** on the Settings page. A folder that is added afterwards has no access until somebody is given it (only administrators see it).
2. **Do not start 0.1.6 against a database that migration `0005` has changed.** 0.1.6 does not know the table, so it would let every visitor see every folder and every signed-in account back up and restore them again. If you have to go back, put the database from before the upgrade back.
3. **Scripts that use the API:** `GET /api/v1/sources` and the other calls only know the folders the caller may browse, and a request for another folder is `404`; `PATCH` needs Full control and `POST` an administrator, as before. A script that signs in with an ordinary account keeps working on the folders that existed; for a folder that is added later the account needs a level on it. The employee IDs in `access` are stored and shown in upper case. A script that reads the lists and writes them back can send the `access_rev` it read together with `access`, and is told (`409`) when somebody changed them meanwhile.
4. The build command and `.env` do not change.

### Known limits

- Rights are for a monitored folder as a whole, not for a path inside it; NTFS permissions and Active Directory groups are not consulted.
- Not closed (see [security.md](./security.md) and [ADR 0013](./adr/0013-per-folder-access-levels.md)): a restore that is already queued or running goes on when the account that started it loses Browse; somebody with Full control who renames their folder to the name of a folder that they cannot see is told that the name is taken; while Redis cannot answer, a session is trusted as issued, so an account that was deactivated or demoted keeps its rights until Redis answers again.
- The editor lists every active account of the user directory in each of the three lists, so the form grows with the directory (checked with 303 accounts); the list is read from Redis at most once a minute.
- Checked with automated tests and with the compiled program in a browser against a scratch database and a fake Redis (see [mvp-status.md](./mvp-status.md)); not with the company's Redis and real accounts.

## 0.1.6

A single sign-on that is refused because of the addresses now says why, and the runbook describes two proxies in a row. It was found when the site was opened through a second server (`http://10.135.36.81/tv/`, whose nginx forwards to the nginx of `prod_eng`): every single sign-in ended with "The sign-in could not be matched to this browser". The reasons and the alternatives are in the update of [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md).

### Added

- **What a refused address check records.** The audit record of a single sign-on that was refused because of the addresses (`address_mismatch`, `browser_address_changed`) now has `connection` (the address that connected: the nearest proxy), `trusted_proxy` (whether that address is in `TIMEVAULT_TRUSTED_PROXIES`) and `forwarded` (the `X-Forwarded-For` header, cut to 256 characters, when there was one), next to `started_from` and `sso_address`. The service log has a warning with the same, and a hint on how to read it. A server's address in `started_from` means that a proxy in front hides the browser; `trusted_proxy` `false` with `forwarded` set means that the proxy is not listed.
- **Two proxies in a row** in [runbook.md](./runbook.md#two-proxies-in-a-row): what goes wrong, the nginx lines that fix it, a second way, and how to check.

### Changed

- The runbook, the security notes and [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md) no longer say that the proxy has to be the first one: the proxy next to TimeVault has to pass on the address of the browser, whether it is the first or the second.

### Upgrade notes

1. **The program behaves as before** except for the extra details of a refused sign-in.
2. **If another nginx sits in front of the one that serves TimeVault,** add `set_real_ip_from <address of the front nginx>; real_ip_header X-Forwarded-For; real_ip_recursive on;` to the `/tv/` block of the nginx next to TimeVault and reload it. `.env` stays as it is.

### Known limits

- Checked with the real nginx 1.24 on one computer, with the two `/tv/` blocks of the company's configuration and loopback addresses for the servers, and with automated tests of the new records; not on the company's servers.
- A browser that reaches the front server over IPv4 and the sign-on service over IPv6 (or the reverse) is still seen with two addresses; the new details show both.

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
5. **Search (Everything).** Nothing changed in the service. To turn it on for an Everything you already run, see the section of [runbook.md](./runbook.md) of that version (Everything was removed in 1.0.1; search is described in [Search index](./runbook.md#search-index) now).

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

## 1.0.1

TimeVault 的构建和运行方式有两处改变，而人们使用的页面里没有任何新东西：TimeVault 自己建立 **NTFS 索引**，而不再去问 Everything（[ADR 0018](./adr/0018-native-ntfs-index-replaces-everything.md)）；运行设置从 `.env` 移入**数据库**，只有本地管理员能在**站点设置**页上编辑它们（[ADR 0017](./adr/0017-site-settings-in-the-database.md)）。除此之外，[mvp-status.md](./mvp-status.md) 中的限制依然有效（备份读取在线磁盘、没有 VSS、自身没有 TLS）。**这不是可以直接替换的升级：** 原来在 `.env` 里的设置不会被带过来，而且要使用搜索，服务必须以管理员权限运行。更新服务器之前，请先阅读升级说明。迁移 `0006_site_settings` 会创建一张表。

### 新增

- **NTFS 索引。** `internal/ntfsindex` 为每个有监控文件夹的盘符，由 NTFS 卷的文件记录（文件夹用 `FSCTL_ENUM_USN_DATA`，名称、大小和时间用 `FSCTL_QUERY_FILE_LAYOUT`；只对监控文件夹之内的内容解码名称）建立其下文件和文件夹的内存树，并通过变更日志（`FSCTL_READ_USN_JOURNAL`，每 10 秒读取一次，并且在回答问题之前、如果过去 2 秒内没有读过，也会先读）保持它最新。它回答某个文件夹之下的搜索（名称包含每一个词，不区分大小写；先文件夹、再按名称；每页最多 200 条）、文件夹中直接包含的各个文件夹及其下所有内容的大小，以及供调度器使用的探测（某个文件夹之下有多少条目，以及其下自某个时间以来是否有变化；早于索引开始监视的时间也算作有变化）。`search.NTFS` 让它成为服务的 `Provider`、`FolderLister` 和 `ChangeProber`。日志被替换或被覆盖、有文件夹从外部被移入监控文件夹，以及某个卷上的监控文件夹发生变化，都会让该卷重新建立（第二种最多每分钟一次）；无法建立索引的卷会在 30 秒后重试。在开发电脑上测得：200 万个条目 13 秒建好，每个条目约 230 字节内存，在一棵有 175 万个条目的树之下搜索约 0.13 秒。见[搜索索引](./runbook.md#搜索索引)。
- **站点设置。** 一个页面 `/settings/site`（**设置 → 站点设置**）、接口 `GET` 和 `PATCH /api/v1/site-settings` 以及 `POST /api/v1/site-settings/restart`，以及命令 `timevault-server settings list|get|set|reset`。分七组（地址与显示、登录、性能、调度器、搜索索引、自动更新、日志）共 26 个设置保存在表 `site_settings` 中，只有本地管理员可以读取和修改：其他已登录的人会被告知没有这个页面（`404`），并且这次尝试会被审计。与默认值相同的值不会被保存。保存之前，各个值会连同它们所依赖的 `.env` 设置一起检查。已保存的设置在重启之后使用：页面会说明哪些设置还没有生效，并提供**立即重启**（自动更新所用的那个重启；有任务在运行时会拒绝）。修改作为 `site.update`、重启作为 `site.restart` 记入审计，只有设置的名称，绝不包含它们的值，并且只有本地管理员能看到这些事件。见[站点设置](./runbook.md#站点设置)。
- **说明搜索为什么不可用。** 状态页和 `GET /api/v1/health` 会说明原因：服务没有管理员权限、某个卷的索引正在建立、某个卷没有变更日志（并给出创建它的命令）、某个卷不是 NTFS，或日志中点名的其他失败。`search_index` 可以关闭索引。
- **迁移 `0006_site_settings`：** 表 `site_settings`（`key`、`value`、`updated_at`、`updated_by`）。
- **测试。** `internal/ntfsindex` 用卷的模型测试（建立、搜索、大小、探测，以及日志：创建、写入、重命名、移动、删除、被应用两次的日志、丢失的日志、被移入的文件夹），并且在 Windows 上从已提升权限的 shell 中对真实的卷测试（`TestWindowsVolumeMatchesAWalkAndFollowsTheJournal`）；`internal/search` 测试提供者；`internal/config`、`internal/sitesettings`、`internal/auth` 和 `internal/server` 测试站点设置（注册表、忽略环境变量的查找、各项检查、谁能打开页面、保存、API、重启、翻译）；`cmd/timevault-server` 测试命令行。

### 变更

- **搜索和文件夹大小来自索引，而不是 Everything。** 搜索是纯文本（没有操作符；`*` 和 `?` 用作词的分隔符），结果先列文件夹、再按名称排序，每一页的顺序都一样。文件夹的大小是其下所有文件长度之和。文件夹大小仍和以前一样缓存 10 秒。
- **调度器的变更探测询问索引。** `scheduler_probe`（原来是 `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE`）默认关闭。索引自上次扫描前 5 分钟以来没有一直在监视该卷时，文件夹同样会被扫描，所以启动或重新建立之后，每个文件夹要多扫描一次才会恢复跳过（间隔短于 5 分钟时更多）。
- **`.env` 只放服务在连上数据库之前就需要的内容，以及机密：** 数据库 URL、`TIMEVAULT_AUTO_MIGRATE`、监听地址、备份和恢复文件夹、`TIMEVAULT_AUTH_DISABLED`、本地管理员、会话密钥，以及用户目录的 Redis。其余每个设置都是站点设置（见下表）。仍写在 `.env` 或环境变量里的站点设置会被忽略，启动日志会点名它们。`configs/timevault.example.env` 和开发脚本随之更新（`dev-run.ps1` 会说明索引需要已提升权限的 shell）。
- **服务分两步启动：** 引导设置、一个小的数据库连接池、迁移、站点设置、把所有内容放在一起校验，然后才是真正的连接池。已保存的设置放在一起无效时，服务会停止并给出点名问题的消息。
- **设置页**原来显示整个运行配置，现在显示版本（对本地管理员还有一个指向站点设置页的链接）；配置移到了站点设置页。`GET /api/v1/settings` 给每个人 `version`、`server_host`、`restore_display_root` 和 `update`，其余的只给本地管理员（`search` 现在是 `search_index`）。
- **`check-config` 只检查引导设置，** 这样服务在替换自己之前运行的那项检查就不依赖数据库。
- **服务以管理员权限运行**，以便使用索引。没有这个权限时它仍能运行：搜索不可用，文件夹大小显示"-"，调度器照旧扫描。
- **状态页不再把 USN 列为已禁用**（现在使用变更日志了）；VSS 仍被列为已禁用。
- 版本号为 1.0.1（每个页面的页脚、设置页、`GET /api/v1/settings`、`timevault-server version`）。`golang.org/x/sys` 成为 `go.mod` 的直接依赖（原来是间接依赖）。

### 移除

- Everything 适配器、它的设置 `TIMEVAULT_EVERYTHING_URL`、`TIMEVAULT_EVERYTHING_USERNAME`、`TIMEVAULT_EVERYTHING_PASSWORD` 和 `TIMEVAULT_EVERYTHING_TIMEOUT`、设置 `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE`、`scripts\dev-everything.ps1`、脚本和在线测试所用的设置 `TIMEVAULT_EVERYTHING_EXE` 和 `TIMEVAULT_TEST_EVERYTHING_*`，以及针对 Everything 的在线测试。Everything 的任何东西都不再被使用，也不再需要。

### 升级说明

1. **更新之前，请先把 `.env` 里的站点设置记下来。** 新程序从第一次启动起就会忽略它们，并且不会复制任何东西。旧名称和新的键如下：

   | 1.0.0 的 `.env` | 1.0.1 的站点设置 |
   |---|---|
   | `TIMEVAULT_SERVER_HOST`、`TIMEVAULT_RESTORE_DISPLAY_ROOT`、`TIMEVAULT_AVATAR_URL`、`TIMEVAULT_BASE_PATH`、`TIMEVAULT_TRUSTED_PROXIES` | `server_host`、`restore_display_root`、`avatar_url`、`base_path`、`trusted_proxies` |
   | `TIMEVAULT_SESSION_TTL`、`TIMEVAULT_COOKIE_SECURE`、`TIMEVAULT_SSO_HOST`、`TIMEVAULT_SSO_REQUIRE_SAME_IP` | `session_ttl`、`cookie_secure`、`sso_host`、`sso_require_same_ip` |
   | `TIMEVAULT_JOB_WORKERS`、`TIMEVAULT_HASH_WORKERS`、`TIMEVAULT_PROCESS_PRIORITY` | `job_workers`、`hash_workers`、`process_priority` |
   | `TIMEVAULT_SCHEDULER_PARALLEL`、`_GAP`、`_REST`、`_FULL_SCAN_EVERY` | `scheduler_parallel`、`scheduler_gap`、`scheduler_rest`、`scheduler_full_scan_every` |
   | `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE` | `scheduler_probe`（需要索引；见第 4 条） |
   | `TIMEVAULT_EVERYTHING_URL`、`_USERNAME`、`_PASSWORD`、`_TIMEOUT` | 已删除；新的 `search_index` 为 `auto` 或 `off` |
   | `TIMEVAULT_UPDATE_URL`、`TIMEVAULT_UPDATE_INTERVAL`、`TIMEVAULT_UPDATE_RESTART` | `update_url`、`update_interval`、`update_restart` |
   | `TIMEVAULT_LOG_LEVEL`、`TIMEVAULT_LOG_FORMAT` | `log_level`、`log_format` |
   | `TIMEVAULT_WEB_LOG`、`TIMEVAULT_WEB_LOG_KEY`、`TIMEVAULT_WEB_LOG_HOST` | `web_log`、`web_log_key`、`web_log_host` |

   这些仍留在 `.env` 中：`TIMEVAULT_DATABASE_URL`、`TIMEVAULT_AUTO_MIGRATE`、`TIMEVAULT_LISTEN_ADDR`、`TIMEVAULT_ALLOW_NON_LOOPBACK`、`TIMEVAULT_REPOSITORY_PATH`、`TIMEVAULT_RESTORE_ROOT`、`TIMEVAULT_AUTH_DISABLED`、`TIMEVAULT_ADMIN_USER`、`TIMEVAULT_ADMIN_PASSWORD_HASH`、`TIMEVAULT_SESSION_KEY`、`TIMEVAULT_REDIS_URL`、`TIMEVAULT_REDIS_KEY_PREFIX`、`TIMEVAULT_REDIS_TIMEOUT`。
2. **更新之后，请填写这些值：** 以本地管理员身份（`TIMEVAULT_ADMIN_USER`，密码的哈希在 `TIMEVAULT_ADMIN_PASSWORD_HASH` 中；没有本地管理员的服务器只能用 `timevault-server settings` 修改）在**设置 → 站点设置**中填写，然后点击**立即重启**或重启服务。请删除 `.env` 中的旧行：启动日志会点名仍在那里的每一行。
3. **有两个设置马上就有影响。** 会自己更新的服务器会丢失 `update_url`，所以**带来 1.0.1 的这次更新，就是它在重新填写 `update_url` 之前自己安装的最后一次更新**（`update_interval` 和 `update_restart` 也会回到默认值，即 10 分钟和 `supervise`）。放在反向代理之后某个路径下提供服务的站点（`base_path`，例如 `/tv`），在重新填写 `base_path` 之前链接是坏的；请用服务器自己的地址（`http://127.0.0.1:8660/settings/site`，不需要前缀）打开站点设置页，或运行 `timevault-server settings set base_path /tv` 然后重启。`trusted_proxies`、`sso_host` 和 `cookie_secure` 也会以同样的方式丢失。
4. **要使用搜索、文件夹大小和跳过未变化文件夹的扫描，请让服务以管理员权限运行：** 从已提升权限的 PowerShell 启动，或者（没有试过）作为以最高权限运行的计划任务，或以 SYSTEM 账号运行。没有这个权限时，状态页会说"服务没有管理员权限，无法读取卷来建立索引。"，搜索不可用，文件夹大小显示"-"，每个到期的文件夹都照旧扫描。监控文件夹必须位于本地 NTFS 盘符上；位于共享上的文件夹不会被建立索引。`scheduler_probe` 默认关闭：索引运行之后请把它打开（并重启）。请阅读 [security.md](./security.md)：整个服务，包括它的 Web 服务器，都在提升权限下运行。
5. **迁移 `0006`** 在 `TIMEVAULT_AUTO_MIGRATE=true` 时于启动时应用（会自己更新的服务有它），或用 `timevault-server migrate` 应用。它只增加一张表。在已迁移的数据库上回到 1.0.0 应该可行（旧程序只查找它认识的迁移，会忽略这张表；没有试过），但那时它的 `.env` 必须重新写上这些设置：请保留一份旧的 `.env`。
6. **不再需要 Everything。** 如果没有别的东西在用它，请停止并卸载 Everything 实例。
7. **发布：** `cmd\timevault-server\main.go` 中的版本已经是 1.0.1，所以运行 `.\scripts\release.ps1`，把 `timevault.exe`，再把 `timevault.json`，复制到发布文件夹（会自己更新的服务器会发现它；它们会丢失什么，见第 3 条）。

## 1.0.0

第一个正式版本。它仍然是 [mvp-status.md](./mvp-status.md) 所描述的垂直切片，它的限制依然有效（读取在线磁盘、没有 VSS 或 NTFS USN journal、自身没有 TLS）。有三项新内容：控制台日志会写出每个请求的用户和地址；服务会把 **Web 日志**写入公司其他应用共用的 Redis 列表，这是 TimeVault 第一次写 Redis（[ADR 0016](./adr/0016-web-log-in-the-shared-redis-list.md)）；除登录页之外，每个页面都有**文档**按钮（用户手册）和**页面导览**按钮（逐步介绍本页）。不需要迁移，也没有必填的设置。还没有人发布 1.0.0：`.\scripts\release.ps1` 负责构建发布，是否把它发布到服务器读取的文件夹由管理员决定。

### 新增

- **Redis 中的 Web 日志。** 设置了 `TIMEVAULT_REDIS_URL` 时，服务会把行追加到该 Redis（同一个数据库）的列表 `web_log` 中，也就是公司其他应用写它们的 Web 日志的那个列表。一行是 `ip | YYYY-MM-DD HH:MM:SS.ffffff | message | status | level | host`，例如 `10.0.0.5 | 2026-11-18 14:05:12.250431 | TimeVault: /explorer user=E123456 0.112(s) | 200 | info | sgmeslt595`，列表中的一个元素是 `zlib.compress(pickle.dumps(line, protocol=4))`，按 Python 写出的方式写出，所以能读其他应用那些行的东西也能读这些行。TimeVault 从不增加字段：账号以 `user=<工号>` 写在 message 中（没有登录的访问者是 `guest`）。地址是浏览器的地址（服务自己做的事用 `127.0.0.1`），时间是服务器的本地时间，最后一个字段是 `TIMEVAULT_WEB_LOG_HOST`，默认是小写的计算机名。写入的内容：
  - **有人打开的页面：** `TimeVault: <route> user=<工号或 guest> <seconds>(s)`：匹配到的路由（`/explorer`、`/jobs/12`；绝不是输入时的地址：没有查询串、没有路径、没有搜索文本，没有匹配任何路由的地址写作 `(not found)`）、用户，以及构建这个页面花了多久。`info`，`4xx` 时是 `warning`。不写入：静态文件、`/healthz`、API 调用、重定向，以及页面脚本在页面所显示的任务运行期间或结束时自己发起的重新加载（请求头 `X-TV-Auto: 1`）。
  - **在服务器上失败的请求**（任何 `5xx`，包括 API 的，以及 panic）：`TimeVault: error <route> user=<工号> ref=<参考编号>`，`error`。`ref` 是错误页面显示的参考编号，控制台日志把它记作 `id=`，并连同错误文本一起保存；错误文本本身绝不写入 Redis。
  - **每一个审计事件**，在它被存入数据库之后：人们做的事，以及调度器和更新器做的事。例如 `TimeVault: backup.request source=3 user=E123456`、`TimeVault: backup.finish source=3 point=17 user=scheduler 12.400(s)` 和 `TimeVault: access.denied method=POST route=/settings/sources user=E1001`。`ok` 是 `info`（状态码 200），`denied` 是 `warning`（403），`failed` 是 `error`（500）。登录失败时输入的名称（它可能是密码）绝不复制（`user=-`）。
  - **调度器和服务：** `TimeVault: scheduler.queued source=<id> user=scheduler`、`TimeVault: scheduler.error [source=<id>] user=scheduler`（一条警告，每个文件夹每分钟最多一次，与控制台中的警告一样）、一切构建完成后的 `TimeVault: started version=1.0.0`，以及服务停止时的 `TimeVault: stopped`。
  - **只在 `TIMEVAULT_LOG_LEVEL=debug` 时：** API 调用（任务的轮询）和自动重新加载的页面行，以及 `scheduler.skipped`（搜索引擎看到没有变化）。其他日志级别不会改变 Web 日志的任何内容。
- **Web 日志是尽力而为的，并且从不妨碍服务。** `Write` 只把一行放进容量为 1,024 的队列，一个 worker 用一次 `RPUSH` 发送最多 100 行。大量涌入的行在入口处被截断（页面行：平均每秒 10 行，突发 100 行；事件行：每秒 20 行，突发 200 行），队列满了就丢弃这一行，`RPUSH` 失败之后，worker 会先暂停访问 Redis 30 秒，然后更久，最长 5 分钟，期间到来的行都被丢弃；它会用一条警告说明这一点，其中 URL 的密码被遮盖。关闭时先发送队列，最多等待 3 秒。TimeVault 从不裁剪列表，也不让它过期：它是共用的，由其他应用管理。访问者输入的任何内容、路径、文件或文件夹的名称和错误文本都不会写入：只有路由、数字、工号和地址。详情见 [Web 日志](./runbook.md#web-日志)。
- **三个设置：** `TIMEVAULT_WEB_LOG`（默认的 `auto` 恰好在设置了 `TIMEVAULT_REDIS_URL` 时开启；`on` 需要它；`off`）、`TIMEVAULT_WEB_LOG_KEY`（默认 `web_log`；不能以账号或单点登录结果的键的写法开头）和 `TIMEVAULT_WEB_LOG_HOST`。启动日志中有 `web_log`、`web_log_key` 和 `web_log_host`，Web 日志开启时还有第二行 `web log is on`，其中是列表、主机名，以及显示为 `host:port/db` 的 Redis，绝不显示密码。设置页的**服务**卡片中有一行 **Web 日志**，`GET /api/v1/settings` 中有 `web_log`（`enabled`、`key`、`host`）。
- **两个圆形按钮：文档和页面导览。** 在右下角的箭头旁边（箭头仍然只在较长的页面上出现），除登录页之外的每个页面都有两个始终存在的按钮。**文档**（书本图标）在新标签页中打开用户手册。**页面导览**（指南针图标）开始逐步介绍你当前所在的页面：它让页面变暗，一次点亮一个部分，并显示一个气泡，其中有标题、文字、"第 n 步，共 N 步"、**上一步**、**下一步**或**完成**，以及一个关闭按钮。页面自己的部分排在最前面，然后是页眉（导航、主题、语言、账号或登录）和圆形按钮。左右方向键用于前进和后退，**Esc** 关闭导览，**Enter** 按下获得焦点的按钮；点击变暗的页面没有任何作用，也不会替访问者点击或更改任何东西。在窄屏幕（640 像素或更窄）上，气泡停靠在底部（针对圆形按钮时在顶部）。显示另一个页面或使用后退按钮时，导览结束；同一个页面自己刷新时（任务页在任务运行期间会这样），导览继续。没有显示出来的部分没有对应的步骤。它是 `web/assets/js/tour.js`（没有第三方库，没有内联脚本或样式）；文字有英文和中文两种（`internal/server/i18n_tour.go`）。
- **`GET /help`，用户手册。** 应用自己的一个页面，对所有人开放，有英文和中文两种（取决于 EN / 中文 切换）：说明 TimeVault 是做什么的，以及每个页面怎么用。它是 `web/templates/help.html`。

### 变更

- **控制台日志的 `request` 行有了地址和用户：** `ip=<浏览器的地址> user=<工号，或 guest> method=… path=… status=… ms=…`。地址是连接的地址，或者是 `TIMEVAULT_TRUSTED_PROXIES` 中的代理所报告的浏览器地址。用户只从会话 Cookie 读取，不询问用户目录；没有登录的访问者是 `guest`，让某人登录的那个请求（`POST /login`、`POST /auth/sso/poll`）会按登录成功的账号记录。静态文件和 `/healthz` 仍然不记录。
- **`request failed` 和 `panic while handling a request` 这两种行**带有同样的 `ip`、`user`、`method` 和 `path`，以及 `id`，即错误页面显示的参考编号。发生 panic 的请求现在也会有它正常的 `request` 行，其中 `status=500`（恢复处理器现在位于请求日志之内）。
- **`job finished` 和 `job finished with an error` 这两种行**带有 `by=<谁请求了这个任务>`（自动备份是 `scheduler`）。
- **`TIMEVAULT_BASE_PATH` 不能以 `/help` 开头：** 它现在是应用自己的页面，服务遇到这样的前缀会拒绝启动。
- 版本号为 1.0.0（每个页面的页脚、设置页、`GET /api/v1/settings`、`timevault-server version`）；`go build` 的命令不变。
- **代码方面：** `redisuser.Connect` 和 `redisuser.Conn` 把一个设置相同的 Redis 客户端交给 Web 日志的写入方（`internal/weblog`）；`internal/redisuser` 本身仍然只读。`audit.Logger.SetMirror` 把每个事件在被存储之后交给 Web 日志。`service.Config.WebLog` 和 `server.Options.WebLog` 接受一个 `weblog.Sink`。`Page.Tour` 把导览的各个步骤写进模板。

### 升级说明

1. **不需要做任何事：** 没有迁移，没有新增的必填设置，服务的运行方式和以前一样。
2. **服务现在会写 Redis。** 设置了 `TIMEVAULT_REDIS_URL` 时，Web 日志**默认开启**：从 0.1.x 升级的服务会开始向该 Redis 中的列表 `web_log` 追加内容，自己更新的服务也一样。要关闭它，请在 `.env` 中写入 `TIMEVAULT_WEB_LOG=off`（较旧的版本会忽略它不认识的设置，所以可以在发布之前先加上）；`TIMEVAULT_WEB_LOG_KEY` 和 `TIMEVAULT_WEB_LOG_HOST` 用来更改列表和行中的主机名。`TIMEVAULT_REDIS_URL` 中的 Redis 账号必须被允许对该列表执行 `RPUSH`；不被允许时，服务会记录警告 `web log: the lines cannot be written to Redis` 并丢弃这些行，其他一切照常。首次启动之后，请查看设置页的 **Web 日志**一行和启动日志中的 `web log is on` 一行。如果 `web_log` 是以 `TIMEVAULT_REDIS_KEY_PREFIX` 开头的，默认的键就是无效的（服务会拒绝启动）：请设置另一个 `TIMEVAULT_WEB_LOG_KEY`，或者 `off`。
3. **控制台日志的各行有了新字段。** `msg=request` 之后是 `ip=…` 和 `user=…`，然后才是 `method`；`request failed` 和 `panic while handling a request` 也有它们，任务各行有 `by=`。按位置解析这些行的任何东西都要调整；按名称读取的则继续可用。
4. **发布：** `cmd\timevault-server\main.go` 中的版本已经是 1.0.0，所以运行 `.\scripts\release.ps1`，并把 `timevault.exe`，再把 `timevault.json`，复制到服务器读取的发布文件夹。运行 0.1.8 或更高版本并设置了 `TIMEVAULT_UPDATE_URL` 的服务，会在它的间隔内发现这个发布（设置页的**立即检查**会马上查看），等正在运行的任务结束，然后重启成 1.0.0：版本不同就是更新。发布之前请先决定如何处理 Web 日志（说明 2）。
5. `.env` 模板（`configs/timevault.example.env`）包含这三个新设置。

### 已知限制

- **Web 日志是尽力而为的。** 队列已满、限制，以及不应答的 Redis 都会丢弃行，被丢弃的行以后不会再写。审计事件只有在被存入数据库之后才会进入 Web 日志，所以没能存储的事件不在其中。它不能替代 PostgreSQL 中的审计记录。
- **TimeVault 从不裁剪共用的列表，也不让它过期**，每一行在 Redis 中约 100 到 150 字节。写入量是有界的（平均每秒最多 10 行页面行和 20 行事件行），但不为零；任何能访问 TimeVault 的人都能制造页面行；而且这个 Redis 没有 `maxmemory` 限制。要裁剪这个列表，由管理那个 Redis 的人决定。
- **这些行里有工号和客户端地址**，所有能读取这个列表的人都能读到。不会写入路径、文件或文件夹的名称、输入的文本和错误文本。
- 页面导览和用户手册的文字只有英文和中文两种，与界面的其余部分一样。
- 已用自动化测试检查过，页面导览还在真实浏览器中针对测试环境检查过（所有页面，英文和中文，浅色和深色主题，桌面宽度和 390 像素宽）；没有在生产服务器上检查：Web 日志没有写入过生产环境的 Redis，编译后的程序没有针对生产环境的数据库和 Redis 运行过，另一个应用的日志页面是否会显示这些行也没有检查过（见 [mvp-status.md](./mvp-status.md)）。更早版本的已知限制依然有效。

## 0.1.10

只改了版本号：在其他所有方面，程序和 0.1.9 一样。这个版本的用途是在服务器上试一试从一个发布自动更新到下一个发布（见[发布与自动更新](./runbook.md#发布与自动更新)）。

### 变更

- 版本号为 0.1.10；`go build` 的命令不变。

### 升级说明

1. **不需要做任何事：** 没有迁移，没有新增设置，服务的运行方式和以前一样。
2. **试一试更新：** 用 `.\scripts\release.ps1` 制作发布，并把 `timevault.exe`，再把 `timevault.json`，复制到服务器读取的发布文件夹（当 `TIMEVAULT_UPDATE_URL` 是 `http://<服务器主机>/api/static/cd/timevault.json` 时，就是另一个应用的 `static\cd`）。运行 0.1.9 并且设置了地址的服务，会在它的间隔内发现这个发布（默认 10 分钟；设置页的**立即检查**会马上查看），等正在运行的任务结束，然后重启成 0.1.10。被替换的程序会留在旁边，名为 `timevault.old-0.1.9.exe`。
3. **要再试一次，** 用同样的方式发布 0.1.9 的发布（先 `timevault.exe`，再 `timevault.json`）：版本不同就是更新，不分方向。脚本只会构建源码里的版本，所以请保留每个发布的两个文件的副本。

### 已知限制

- 已用编译后的程序，针对临时数据库和一个静态文件的替身检查过，没有在生产服务器上检查（见 [mvp-status.md](./mvp-status.md)）。0.1.8 和 0.1.9 的已知限制依然有效。

## 0.1.9

账号头像现在能显示了，发布也有了自己的位置。头像缺失的原因是用真实的应用找到的：它把 `.webp` 头像以 `text/plain` 发送，而 0.1.8 只接受 `image/*`。决定和备选方案见 [ADR 0015](./adr/0015-account-pictures-are-read-by-the-browser-from-their-host.md)。

### 修复

- **账号头像从未显示（0.1.8）。** 存放头像的应用（其他公司应用的静态文件）以 `Content-Type: text/plain` 发送头像，图片并不在意这一点，但 0.1.8 的脚本不接受；对于缺失的头像，它回答 `202` 并附带一张 480×320 像素的占位图。现在脚本把每张头像作为图片加载：出错，或者图片恰好是这个尺寸，都保留默认头像。缺失的头像会被记住十分钟（以前是整个浏览器会话，这会让曾经不可用的主机不再被尝试）。

### 变更

- **头像从 `TIMEVAULT_SERVER_HOST` 读取。** `TIMEVAULT_AVATAR_URL` 现在可以是 `http(s)` 地址、站点被打开时所用主机上的路径（0.1.8 中它的含义），或 `off`。未设置时，它是 `http://<TIMEVAULT_SERVER_HOST>/api/static/hr/{id}.webp`；服务器主机也没有设置时，没有头像。由浏览器读取，而不是服务，所以无论用什么地址打开站点都能显示。
- **内容安全策略。** 每个页面的策略都把绝对头像地址的主机列入 `img-src`，不用于其他任何地方。带下划线的主机名（`prod_eng.co.example.local`）无法写进策略，所以改为列出其所在域的通配符（`http://*.co.example.local`）；如果没有剩下两个标签的域，启动日志会警告：头像只有在用同一个地址打开站点时才会显示。
- 版本号为 0.1.9；`go build` 的命令不变。

### 文档

- **发布放在哪里。** 使用其他公司应用的静态文件时，清单是 `http://<服务器主机>/api/static/cd/timevault.json`，是文件名的 `package_url` 指向 `http://<服务器主机>/api/static/cd/<名称>`（服务一直都是在清单旁边查找它；现在有测试覆盖这种布局，以及文件不存在时的 `202`）。地址仍然是 `.env` 中显式写出的 `TIMEVAULT_UPDATE_URL`：它不会从 `TIMEVAULT_SERVER_HOST` 推导，所以更新只会因为 `.env` 这样写才开启。
- `configs/timevault.example.env` 不再说"失败时重复运行"的计划任务能在 `exit` 模式下于更新之后重新启动程序：任务计划程序的"如果任务失败，重新启动"设置做不到（已试过）；触发器每分钟重复一次并选择"不要启动新实例"的任务可以。

### 升级说明

1. **头像：** 设置了 `TIMEVAULT_SERVER_HOST` 时不需要做别的事。如果头像在站点所在的主机上，但没有设置 `TIMEVAULT_SERVER_HOST`，请写 `TIMEVAULT_AVATAR_URL=http://<主机>/api/static/hr/{id}.webp`。自己设置了 0.1.8 默认值（`TIMEVAULT_AVATAR_URL=/api/static/hr/{id}.webp`）的 `.env` 照常工作，仍然从站点被打开时所用的主机读取。
2. **自动更新：** 没有变化。把 `TIMEVAULT_UPDATE_URL=http://<服务器主机>/api/static/cd/timevault.json`（以及 `TIMEVAULT_AUTO_MIGRATE=true`）写入 `.env`，并把 `timevault.exe`，再把 `timevault.json`，复制到另一个应用的 `static\cd` 文件夹。运行 0.1.8 并且设置了地址的服务器，会从那里自己更新到 0.1.9。
3. 没有迁移，也没有新增的必填设置。

### 已知限制

- 头像通过 `http` 明文读取，存放头像的主机能看到每个显示头像的浏览器的地址。通过 `https` 提供的站点需要 `https` 头像地址。
- 没有头像的人，只有在另一个应用的占位图保持现在的尺寸（480×320 像素）时才保留默认头像；如果该应用改了占位图，在修改 `web/assets/js/app.js` 中的尺寸之前，会显示占位图。
- 已用自动化测试、针对临时数据库的编译后程序，以及在构建所用的电脑上针对存放头像的应用的真实浏览器检查过（见 [mvp-status.md](./mvp-status.md)）；没有在生产服务器上检查，也没有通过公司的其他 nginx 服务器检查。

## 0.1.8

账号头像，以及会自己更新的程序。现在每次构建都会生成一个**发布文件夹**（`timevault.exe` 和 `timevault.json`）；被告知该文件夹位置的服务会读取它，下载新版本，等正在运行的任务结束后自己重启。决定和备选方案见 [ADR 0014](./adr/0014-self-update-from-a-release-manifest.md)。

### 新增

- **账号头像。** 页眉、审计日志（操作者）、任务页和任务列表（谁发起的）、文件夹访问权限的摘要，以及权限编辑器的人员列表，都会在账号名称旁边显示该账号的头像。头像从同一主机上的 `/api/static/hr/<工号>.webp` 读取（公司其他应用保存的员工照片），由 `TIMEVAULT_AVATAR_URL` 设置，其中 `{id}` 是工号，`off` 表示关闭头像。没有头像、或者头像读不到的账号保留默认头像。存放头像的主机对缺失的头像回答带占位图的 `202`，所以只有 `200` 且是图片的应答才算数；浏览器对每张头像只请求一次，并且只在它即将被看到时才请求，缺失的头像在会话期间会被记住。本地管理员和服务自己使用的名称（`scheduler`、`updater`、`system`）没有头像。内容安全策略不变。
- **`scripts\release.ps1`** 生成发布文件夹（默认是 `<repository>\release`，已被 git 忽略）：`timevault.exe`，最后是带有 `latest_version`、`sha256`、`package_url` 和 `size` 的 `timevault.json`。它运行 `go vet`，不带 `-X` 地构建，从程序本身取得版本号，并在版本号与那里已有的发布相同时发出警告。`-BaseUrl` 写出完整的 `package_url`；其他参数是 `-OutDir`、`-ExeName` 和 `-SkipChecks`。
- **自动更新。** 设置 `TIMEVAULT_UPDATE_URL`（`http(s)` 地址，或文件夹/共享的路径；为空表示关闭）、`TIMEVAULT_UPDATE_INTERVAL`（默认 `10m`，范围 `1m` 到 `24h`）和 `TIMEVAULT_UPDATE_RESTART`（默认 `supervise`，或 `exit`）之后，服务在启动后约 10 秒以及每个间隔读取清单。当 `latest_version` 不是正在运行的版本时，它会把程序下载到自己旁边（大小和 sha256 必须一致），用 `version` 和新增的 `check-config` 命令运行它，停止取用任务，等正在运行的任务全部结束（没有时间上限），通过重命名交换文件（旧程序保留为 `timevault.old-<版本>.exe`，保留最新的三个），停止，然后启动新程序。使用 `supervise` 时，被启动的进程作为一个小型启动器保留，并把新程序作为子进程运行（控制台窗口和计划任务看到的始终是一个程序；Ctrl+C 会同时停止两者；启动器被强制终止时，例如结束任务，程序也会随之停止）；使用 `exit` 时，进程以退出码 75 结束，由服务管理器重新启动它。新程序会说明它已接手（日志和审计）。
- **`timevault-server check-config`** 像 `serve` 一样检查设置（环境变量和 `.env` 文件），不连接任何东西；设置无效时退出码为 1 并给出原因。
- **设置页新增一张"程序更新"卡片：** 状态（已是最新、正在检查、正在下载、等待正在运行的任务、正在安装、出现问题）、正在运行的版本、已发布的版本、清单最近一次读取的时间以及读取的频率。管理员还能看到发布地址和最近的问题，以及**立即检查**按钮（`POST /settings/update/check`、`POST /api/v1/update/check`）。
- **页眉下面的提示：** 服务等待重启期间，每个已登录的账号都会被告知：有一个版本将在正在运行的任务结束后安装，新任务要等到那时再开始；人们在页面之间切换时，脚本会让它保持正确。
- **审计：** 动作**程序更新**（`app.update`，操作者 `updater`），详情中有阶段（`staged`、`restart`、`done`、`cancelled`、`failed`），没有路径，也没有错误文本。
- **API：** `GET /api/v1/settings` 中的 `update`（`enabled`、`state`、`latest_version`、`check_interval`、`waiting_jobs`、`checked_at`；管理员还有 `manifest` 和 `error`）、`POST /api/v1/update/check`，以及局部页面上的 `X-TV-Notice` 响应头。
- **任务运行器：** `Drain`、`Resume` 和 `Running`。运行器处于排空状态时，排队的任务保持排队。

### 变更

- **设置了 `TIMEVAULT_UPDATE_URL` 的服务必须同时设置 `TIMEVAULT_AUTO_MIGRATE=true`**，否则拒绝启动，因为新版本可能修改数据库，而服务自己更新时没有人会运行 `migrate`。
- 设置页的访问权限摘要和权限编辑器的人员列表，现在与其他页面使用同样的局部模板来显示账号，所以也会显示头像。
- 版本号为 0.1.8；`go build` 的命令不变。

### 升级说明

1. **不需要做任何事**就能保持服务原来的运行方式：没有 `TIMEVAULT_UPDATE_URL` 时它从不更新自己，访问不到头像主机时每个人显示的都是默认头像。没有迁移，也没有新的必填设置。
2. **在服务器上启用自动更新**（只需手工做一次：更早的版本不能自己更新）：把 0.1.8 的 `timevault.exe` 复制到 `.env` 旁边，加入 `TIMEVAULT_AUTO_MIGRATE=true` 和 `TIMEVAULT_UPDATE_URL=<发布文件夹>`，给运行服务的账号对程序所在文件夹的**修改**权限和对发布文件夹的**读取**权限，然后启动。此后用 `scripts\release.ps1` 制作发布，并（先程序、后清单）复制到发布文件夹。见[发布与自动更新](./runbook.md#发布与自动更新)。
3. **如果服务需要回退，** 请先把较旧的发布放进发布文件夹（或者清空 `TIMEVAULT_UPDATE_URL`），否则它会再次更新到较新的版本。上一个程序是 `timevault.old-<版本>.exe`。
4. **头像：** 在公司的 nginx 之后，`/api/static/hr/<工号>.webp` 与 `/tv/` 在同一个主机上，所以不需要任何设置头像就会出现。直接通过服务自己的端口访问时显示默认头像。设置 `TIMEVAULT_AVATAR_URL=off` 可以关闭头像。
5. `.env` 模板（`configs/timevault.example.env`）包含新的设置。

### 已知限制

- **没有签名。** 谁能写入发布文件夹，谁就决定了服务器上运行什么；大小和 sha256 只能防止文件损坏或只复制了一半（[security.md](./security.md)）。请使用只有管理员能写入的文件夹，地址使用 `https`。
- **没有自动回退。** 如果新程序通过了检查之后又失败（迁移失败、文件夹不见了），服务会一直停止，原因在日志中。回退要手工完成，已经应用的迁移会保留。
- **等待正在运行的任务没有时间上限，** 等待期间任何任务都不会开始，包括调度器的备份。
- **`supervise` 模式的启动器保留它被启动时的代码，** 并且在它活着的时候旧程序文件会一直被锁定；要改动启动器，需要重启启动器。
- 版本只比较是否相等，所以带有较旧版本的清单就是降级。
- 已用自动化测试和编译后的程序对着临时数据库检查过（见 [mvp-status.md](./mvp-status.md)）；没有在正式服务器上检查，没有经过公司的 nginx，也没有使用真实的头像主机。

## 0.1.7

账号能做什么，现在按每个监控文件夹分别决定：**浏览**、**备份**（立即备份，包括完整重读）和**完全控制**，可以授予访客、所有已登录的人或单个工号，并在设置页设置文件夹时一并设定。管理员对每个文件夹都拥有完全控制权。访问者无权浏览的文件夹，对他们来说根本不存在。决定和备选方案见 [ADR 0013](./adr/0013-per-folder-access-levels.md)。

### 新增

- **每个文件夹三个级别。** 每个级别都包含前面的级别。浏览：查看文件夹、它的历史、任务和审计事件，并从中恢复（这需要已登录的账号）。备份：另外还有**立即备份**、**完整重读**、**校验**，以及停止该文件夹的任务。完全控制：另外还可以修改文件夹的名称、显示文件夹、排除规则、启停、计划、保留的版本数，以及谁能对它做什么；不能修改服务器磁盘上的文件夹，也不能添加文件夹（仅限管理员）。访客只能被授予"浏览"。
- 设置页上的**谁能做什么**：每个文件夹有三个列表（浏览、备份、完全控制），用复选框选择**访客（未登录）**、**所有已登录的人**和用户目录中的人员（按姓名和工号列出，有一个用来查找的输入框）。它们出现在添加文件夹的表单（管理员）和编辑文件夹的表单（管理员和拥有完全控制权的人）中；这些表单在被请求时才构建（`/settings?add=1`、`/settings?edit=<id>`）。
- **隐藏的文件夹。** 访问者在其上没有任何级别的文件夹，不会出现在文件浏览、时间机器、备份、设置、审计和任务页面以及 API 中，直接请求它会得到与请求不存在的文件夹相同的"未找到"（未登录的访问者会被告知登录后也许能看到）。对能看到的文件夹的拒绝是 `403`，审计日志把它显示为**访问被拒绝**，并带上该文件夹和所需的级别。
- **API：** `GET /api/v1/sources` 中的 `level`（对拥有完全控制权的人还有 `access`）；`POST /api/v1/sources` 和 `PATCH /api/v1/sources/{id}` 中的 `access`；`confirm_lockout` 和错误 `lockout_not_confirmed`。`access` 中的人员可以写成 `user:<工号>`。
- **在有人放弃自己的完全控制权之前给出警告。** 不是管理员的人保存的列表如果不再让他们拥有完全控制权，表单会要求勾选确认，API 则在发送 `confirm_lockout` 之前返回 `409 lockout_not_confirmed`。
- **列表的修订号**（`access_rev`）出现在编辑表单、`GET /api/v1/sources`（给拥有完全控制权的人）和 `PATCH /api/v1/sources/{id}` 中。没有修改列表的表单不会去动它们，所以保存计划时，不会把别人在表单打开期间取消的授权又带回来；修改了列表的表单或请求，如果此后别人已经修改了它们，就会被拒绝（`409`，`access_changed`），表单会带着列表现在的样子和其他已输入的内容重新出现。
- **迁移 `0005_source_access`**（表 `source_access`），以及为编辑器提供的 Redis 用户目录有效账号列表（`Directory.Accounts`，缓存一分钟）。

### 变更

- **权限按文件夹分别决定。** "每个已登录的账号都可以对每个文件夹备份、校验、取消任务和恢复"这条规则不再存在；由每个文件夹自己说了算。恢复需要对该文件夹的"浏览"权限，并且需要已登录的账号。
- **展示的内容被裁剪到可见的文件夹：** 文件浏览和时间机器的选择框、备份、设置、审计和任务页面及其 API、状态页上排队和运行中任务的数量，以及在文本中隐藏服务器真实文件夹的映射表。不属于任何文件夹的事件（登录等）仍然像以前一样对每个已登录的账号可见。被拒绝请求的审计记录保存的是路由，而不是输入的路径。
- **显示文件夹的页面会向用户目录核实已登录的访问者**（和那些会改变内容的页面以前就做的一样），所以被停用或被降级的账号会立即变成访客。
- **对文件夹的修改**会在锁定该行之后，在自己的事务中再次检查授权，所以期间被撤销的"完全控制"不能撤销自己的撤销。不是管理员的人永远不能写入服务器磁盘上的文件夹（他们提交 `root_path` 会得到 `403`）。
- **恢复的幂等键。** 用相同的 `idempotency_key` 重复恢复，只会把原来的任务返回给针对该文件夹启动它的那个账号；别人用过的键返回 `409`。
- **审计。** 与某个文件夹有关的事件（拒绝、任务被停止、文件夹的创建和修改）带有该文件夹；旧的 `job.cancel` 事件通过它的任务归属到文件夹。除管理员外，其他人看到他们可以浏览的文件夹的事件，以及不属于任何文件夹的事件。
- **设置页**显示每个文件夹上的级别，并且只在访问者拥有完全控制权的地方显示**编辑**按钮。在"查找人员"的输入框中按回车不再保存表单。
- **谁能做什么只给能修改它的人看**，审计日志中也是如此：创建文件夹或修改其列表的事件详情中的列表（`access`），对只能浏览或备份该文件夹的人是 `[hidden]`。
- **不是该文件夹的恢复点**出现在恢复预览或启动请求中时，会返回 `404`，与不存在的编号一样；以前是一个 `400`，它会透露该恢复点存在于另一个文件夹。

### 升级说明

1. **应用迁移：** `timevault-server migrate`，或者用 `TIMEVAULT_AUTO_MIGRATE=true` 启动。它会给每个已有的文件夹设置到 0.1.6 为止所有人都能做的事（访客可以浏览，所有已登录的人可以备份和恢复），所以在管理员用设置页的**编辑**收紧之前没有任何变化。之后添加的文件夹在有人被授权之前没有任何访问权限（只有管理员能看到）。
2. **不要让 0.1.6 去启动一个已被迁移 `0005` 改过的数据库。** 0.1.6 不认识这张表，所以它会再次让每个访问者看到每个文件夹，让每个已登录账号对它们备份和恢复。如果必须回退，请把升级之前的数据库放回去。
3. **使用 API 的脚本：** `GET /api/v1/sources` 和其他调用只认识调用者可以浏览的文件夹，请求其他文件夹会得到 `404`；`PATCH` 需要完全控制权、`POST` 需要管理员，和以前一样。用普通账号登录的脚本在已有的文件夹上继续可用；对于之后添加的文件夹，账号需要在其上有一个级别。`access` 中的工号以大写形式保存和显示。读取列表再写回去的脚本，可以把它读到的 `access_rev` 与 `access` 一起发送，这样在期间有人修改了列表时会得到 `409`。
4. 构建命令和 `.env` 都不变。

### 已知限制

- 权限针对整个监控文件夹，而不是其中的某个路径；不会查询 NTFS 权限和 Active Directory 组。
- 尚未解决的（见 [security.md](./security.md) 和 [ADR 0013](./adr/0013-per-folder-access-levels.md)）：启动恢复的账号失去"浏览"权限后，已排队或正在运行的恢复仍会继续；拥有完全控制权的人把自己的文件夹重命名为一个他们看不到的文件夹的名称时，会被告知该名称已被占用；Redis 无法应答期间，会话按签发时的状态被信任，所以被停用或被降级的账号会一直保留它的权限，直到 Redis 重新应答。
- 编辑器在三个列表中的每一个里都列出用户目录的每个有效账号，所以表单会随目录变大（检查过 303 个账号）；这份列表最多每分钟从 Redis 读取一次。
- 已通过自动化测试检查，并用编译后的程序在浏览器中针对临时数据库和模拟的 Redis 检查过（见 [mvp-status.md](./mvp-status.md)）；没有用公司的 Redis 和真实账号检查。

## 0.1.6

因地址而被拒绝的单点登录现在会说明原因，runbook 也说明了两层代理的配置。它是在通过第二台服务器打开站点时发现的（`http://10.135.36.81/tv/`，它的 nginx 转发给 `prod_eng` 的 nginx）：每次单点登录都以"无法把这次登录与当前浏览器对应起来"结束。原因和备选方案见 [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md) 中的更新。

### 新增

- **被拒绝的地址检查会记录什么。** 因地址而被拒绝的单点登录（`address_mismatch`、`browser_address_changed`）的审计记录，现在除了 `started_from` 和 `sso_address`，还有 `connection`（连接过来的地址：最近的代理）、`trusted_proxy`（该地址是否在 `TIMEVAULT_TRUSTED_PROXIES` 中）和 `forwarded`（`X-Forwarded-For` 头，截断为 256 个字符，有该头时才有）。服务日志里有一条包含同样内容的警告，以及如何解读它的提示。`started_from` 里是某台服务器的地址，说明前面有代理把浏览器隐藏了；`trusted_proxy` 为 `false` 且有 `forwarded`，说明该代理没有被列入。
- [runbook.md](./runbook.md#两层代理) 中的**两层代理**：会出什么问题、修复它的 nginx 配置行、另一种做法，以及如何检查。

### 变更

- runbook、安全说明和 [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md) 不再说代理必须是第一层：紧挨着 TimeVault 的代理必须把浏览器的地址传下去，无论它是第一层还是第二层。

### 升级说明

1. **程序的行为与以前一样**，只是被拒绝的登录多了一些详细信息。
2. **如果提供 TimeVault 的 nginx 前面还有另一个 nginx，** 请在紧挨着 TimeVault 的 nginx 的 `/tv/` 配置块里加上 `set_real_ip_from <前面 nginx 的地址>; real_ip_header X-Forwarded-For; real_ip_recursive on;` 并重新加载它。`.env` 保持不变。

### 已知限制

- 用真实的 nginx 1.24 在一台电脑上检查过，使用公司配置中的两个 `/tv/` 配置块，并用回环地址代表服务器，另外还有针对新记录的自动化测试；没有在公司的服务器上检查。
- 如果浏览器通过 IPv4 访问前面的服务器、通过 IPv6 访问单点登录服务（或者相反），仍会被看成两个地址；新的详细信息会显示这两个地址。

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
5. **搜索（Everything）。** 服务本身没有变化。要为你已经在运行的 Everything 启用搜索，见该版本的 [runbook.md](./runbook.md)（Everything 在 1.0.1 中已被移除，搜索现在在[搜索索引](./runbook.md#搜索索引)中说明）。

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
