# English

## Overview

TimeVault is one Go service, `timevault-server` (`cmd/timevault-server`), that hosts the web UI, the JSON API and the background workers. It keeps history in PostgreSQL and file content in a separate, content-addressed repository folder. It reads source folders; it never changes them. Anyone can browse; company accounts from a Redis user directory sign in to back up, restore or change settings (version 0.1.6; see [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)).

This document describes what is implemented. Items that plan.md describes but TimeVault does not do are listed in [mvp-status.md](./mvp-status.md).

## Components

| Package | Responsibility |
|---|---|
| `internal/config` | Reads and validates the `TIMEVAULT_*` settings from the environment and the `.env` file (the environment wins), names the settings of the file that the environment overrides (the start-up log warns about them), and describes the running configuration for the Settings page without secrets. |
| `internal/db`, `migrations/` | pgx connection pool, the embedded, checksummed SQL migrations (`0001_init`, `0002_schedule`, `0003_display_path`, `0004_v014`), and the service-wide instance lock. |
| `internal/pathutil` | Windows-compatible name validation, source-relative path cleaning, case-insensitive containment, resolution of a path's real location (junctions and links included), and the parsing of network paths (`ParseUNC`, `\\host\share\folder`). |
| `internal/platform` | Windows file attributes, disk space, lock detection, real-path resolution, folder flushing and the process priority behind build tags. |
| `internal/repository` | Content-addressed object store (whole-file SHA-256), atomic writes, verification, deletion of an object. |
| `internal/catalog` | Monitored folders (sources) with their schedule, display folder and version limit, recovery points, per-path version history and its queries, the deletion of old versions (`PruneVersions`, `PreviewPrune`) and the list of objects that no version refers to. |
| `internal/backup` | Scanner, change detection, content capture, recovery-point state machine, verification, skipping of unchanged automatic runs, validation of monitored folders and their display folders, the deletion of old versions (`Prune`) and of repository content that nobody refers to (`SweepObjects`) behind a gate that every backup holds. |
| `internal/restore` | Dry-run planner, confirmation tokens, staged and verified restore. |
| `internal/jobs` | Durable job queue in PostgreSQL with workers, progress and a per-job log. A job that has to wait for another one of its folder (one backup per folder at a time; a cleanup of old versions takes turns with the other jobs of its folder) stays queued and holds no worker. |
| `internal/listing` | The engine of the folder lists: the type of an item, filtering, sorting and paging in memory, and the count of the types of a folder. It knows nothing about where the items come from. |
| `internal/search` | Search provider interface, Everything HTTP JSON adapter (search, the folders inside a folder with their sizes, and the change probe for the scheduler), mock and disabled providers. |
| `internal/auth` | Identities, roles and permissions; PBKDF2 hash of the optional local administrator; signed session cookies; the `Directory` interface (and `SSODirectory` for single sign-on) and re-checking of signed-in accounts; CSRF checks; sign-in limits. |
| `internal/redisuser` | The user directory of another application, read-only from Redis: a restricted pickle decoder, the sign-in adapter (`auth.Directory`), the reader of the results of the company sign-on service (`sso_login_<token>`) and the `check-redis-users` report. |
| `internal/audit` | Append-only audit trail, with the duration of the operations that have one. |
| `internal/service` | Application layer shared by the UI and the API (live browsing with the folder sizes from Everything, history browsing, jobs, restore, monitored folders and their display-folder policy, the scheduler with its pacing and the Everything probe, the version limit and the cleanup that applies it, health, and `PathMap`, which hides the server's real folders in text). |
| `internal/server`, `web/` | HTTP routing and access control (`open`, `openChecked`, `need`), middleware, server-rendered pages (Go templates), JSON API, embedded assets, EN/中文 messages, the list controls (sort links, type filter, pager), the single sign-on flows (`sso.go`), the path prefix (`basepath.go`) and `visibility.go`, which decides what each kind of visitor sees of the server's folders. |

## Data flow

**Backup.** A backup job takes the per-source advisory lock, discards anything an unfinished earlier build left behind, creates a `building` recovery point, walks the source folder by folder, copies new or changed files into the repository, writes the changes for each folder in one transaction, validates that every object it newly recorded still exists, flushes the repository folders to disk, and finally marks the point `committed` in a single update. Once that update is issued it is not abandoned: a cancellation that arrives later cannot turn a committed point into a failed job. Readers only ever query committed points, so a point that is being built, failed or was cancelled is never visible.

**Single instance.** Only one service may run against a database (schema). At start, before it recovers anything, the service takes a PostgreSQL advisory lock on a dedicated connection; a second service stops with "another TimeVault service is already running against this database". Without this, the second service's start-up recovery would mark the first one's running jobs as failed.

**Browse.** The Explorer lists the folder as it is on disk (through `os.Root`, so a crafted path cannot leave the source) and marks each item *protected*, *changed* or *new* by comparing it with the newest committed point. Items that existed at that point but are gone from disk are listed as *deleted since the last backup*. The Time Machine page lists the same folder as it was at any committed point. Both read **all** the children of the folder (a directory read, or one SQL query without `LIMIT`) and hand them to `internal/listing`, which filters by type, sorts, cuts the requested page and counts the types of the whole folder; the pages and the API use the same call (`Service.ListLive`, `Service.ListHistory`), so they cannot disagree. See [ADR 0008](./adr/0008-folder-lists-are-cut-on-the-server.md). The sort, the type filter and the page are query parameters, so every view has an address that can be shared.

**Display folders.** People reach the folders over the network, so every monitored folder has a display path (`sources.display_path`) and the restore folder a display root (`TIMEVAULT_RESTORE_DISPLAY_ROOT`). `Service.AddSource` and `UpdateSource` apply the policy before the engine looks at the disk: with `TIMEVAULT_SERVER_HOST` set the display path is required and its host must be that one, otherwise it is optional and only its syntax is checked (`backup.SourceError` carries the message key and arguments, so the Settings page can answer in the visitor's language). On every request that has something to show, `internal/server/visibility.go` builds a `viewer` for the account: administrators get texts as they are, everybody else gets them through `service.PathMap` (`Mask`, `MaskValue`), which replaces every real folder of the sources, the repository and the restore folder. See [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md).

**Search.** The search box sends the text to a `search.Provider`. The Everything adapter sanitizes the text, confines the query to the source root, calls the loopback-only HTTP JSON endpoint with a bounded page size and re-checks every result against the root. When the engine is down the page shows a degraded notice; browsing and history keep working.

**Restore.** A restore is previewed first (nothing is written), then confirmed with a token that is bound to the user and to the exact plan, then run as a background job into a folder below the configured restore root. See [Restore flow](#restore-flow).

**Sign-in and access.** Every route is registered with one of three wrappers in `internal/server`: `open` (anyone; reads only the cookie, so browsing never depends on Redis), `openChecked` (anyone, but a visitor with a cookie is first checked with the user directory because the page shows an account more than a visitor) and `need(permission, handler)` (a signed-in, active account with the permission; checks the CSRF token on state-changing methods; audits a refusal as `access.denied`). The permissions are `browse` (everyone), `account` (any signed-in account, for signing out), `backup`, `restore`, `audit` (the audit log, the status and settings pages) for every signed-in account, and `manage` for `admin` accounts only. A sign-in goes through `auth.Manager.Login`: the name of the optional local administrator is checked against its own hash and never looked up elsewhere; any other name is passed to the `auth.Directory`, which `internal/redisuser` implements by finding the `app_user_*` hash (`SCAN`, then `HGETALL`), decoding its fields without executing anything, and comparing the password. The session cookie carries the account, name, role and Redis key; `Manager.Verify` asks the directory again at most once a minute per account and record (`Refresh`), so a deactivation or role change takes effect within a minute, trusts the session as issued for 15 seconds at a time while the directory does not answer, and never takes a request that the visitor abandoned for an outage. Sign-in attempts are counted when they start, per client address and per account name.

**Automatic backups.** `service.RunScheduler` runs in its own goroutine, started after the job runner. Every 15 seconds (and once at start) it asks the catalog for the sources that are due (`enabled`, `schedule_minutes` set, and `last_run_at` empty or at least that many minutes ago) and goes through them in order of need (`rankDue`: no committed point first, then the most overdue in proportion to its interval, then the quickest last run, then the id). A source that already has a queued or running backup is skipped. With the Everything probe on, a due source that Everything says did not change is done at once, without a scan, and its interval restarts (`probeUnchanged`: Everything knows the folder, answers within 3 seconds, holds as many entries below it as were recorded before the last complete scan, saw nothing created, modified, renamed, moved or changed in its attributes since 5 minutes before that scan, the newest point has no item that was not captured, and that scan is younger than `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY`; a tick asks Everything at most 25 times, and an Everything that does not answer is left alone for a minute, `probeBackedOff`). Changing the exclude patterns of a source clears its last scan (`catalog.UpdateSource`), so its next run scans. The first source that has to be scanned is queued as a backup job whose requester is `scheduler`, if the pace allows (`mayStartScan`: fewer than `TIMEVAULT_SCHEDULER_PARALLEL` automatic backups are queued or running, and the gap since the last start and the rest since the last end have passed; the pace is kept in memory), and then the tick ends. See [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md). The backup runs with `SkipUnchanged`: after the scan, when no path was added, modified or deleted and the number of items that could not be read equals that of the newest committed point, the engine discards the recovery point it had begun and returns `ErrUnchanged`; the job runner then deletes the job (`jobs.ErrForget`) and no audit event is written. Whatever the outcome of a run, `last_run_at` and `last_run_ms` are set when it ends, and so are `last_walk_at` and `last_walk_items` after a complete scan (`recordRun`), so the next run is due one interval later. The engine takes the source's advisory lock first and reads the source only then; changing the folder of a source takes the same lock and is refused (`ErrBusy`) while a backup of it runs.

**Single sign-on.** `internal/server/sso.go` keeps the sign-ins in progress in memory (a token, the client address, the page to go to afterwards, the time). `POST /auth/sso/start` issues a token (15 random bytes, 24 characters) and returns the address of the company sign-on service with it; the browser opens that address in a window. `POST /auth/sso/poll` looks the flow up, compares the client address with the one that started it (with `TIMEVAULT_SSO_REQUIRE_SAME_IP`), reads `sso_login_<token>` through `auth.SSODirectory.SSOResult` (a `GET` by `internal/redisuser`, decoded by the same restricted pickle decoder as the accounts), maps the employee ID to an active account with `Identify` (the newest record, which must be activated) and starts the same session as a password sign-in (`Manager.StartSession`). The flow is then removed, so a result is used once; the key in Redis is never deleted and expires on its own. The client address comes from `Server.clientIP`, which believes `X-Forwarded-For` only from `TIMEVAULT_TRUSTED_PROXIES`. When the check refuses a sign-in over the addresses, `Server.addressDetail` adds how the request reached the server (`connection`, `trusted_proxy`, `forwarded`) to the audit record, and the same goes to the log. See [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md).

**Path prefix.** With `TIMEVAULT_BASE_PATH` (for example `/tv`) the site can be served under a path of a host name that other applications share. Inside, the routes, the redirect targets and the template paths stay written without the prefix; `internal/server/basepath.go` takes it off on the way in and puts it on on the way out. `Server.withBase` is the outermost handler: a path that starts with the prefix loses it (`http.StripPrefix`), the bare prefix is sent to the same address with a slash, and any other request is served as it is, so a proxy may keep or remove the prefix. It wraps the response in `prefixWriter`, which puts the prefix on every `Location` header that names a path of the application: the redirects of the pages, the `Location` of the API and the redirects of the `ServeMux`. The templates write every address with the `url` function (`Server.href`), the address a link carries for the page to come back to (`next`, `back`) is written with `route`, the script reads the prefix from `<meta name="tv-base">`, and the cookies take their path from `Server.cookiePath` and `auth.Options.CookiePath`. An address that comes back from the browser goes through `Server.local` and `Server.safeNext`, which take the prefix off and check the result again. Two tests keep the pieces honest: no template may write an address that starts with a slash, and no code may set a cookie for the whole host. See [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md).

**Folder sizes.** When the live listing holds folders, `Service.ListLive` asks `fillFolderSizes` for their sizes: one `Everything.Subfolders` call (`infolder:"<dir>" folder:`, paged, at most 20,000 folders) whose answer is cached for 10 seconds. A question waits at most 2 seconds; when Everything does not answer, the sizes stay unknown (shown as `-`) and it is not asked again for 15 seconds. The sizes take part in sorting by size. The Time Machine and the versions page read the catalog and do not use them.

**Version limit.** After a backup committed a recovery point, and when the folder has a limit, `pruneAfterBackup` calls `Engine.Prune`: with the writer lock of the source, `catalog.PruneVersions` deletes (in batches of 500 files, one transaction each) the versions of the files that backup changed that fall outside the newest N captured versions of the file, and flags the recovery points that lost versions (`recovery_points.partial`) in the same transaction. When versions were deleted, `Engine.SweepObjects` then deletes the objects that no version refers to (the file first, then its record), within 2 minutes; it only runs while no backup holds the shared side of a gate in the backup engine (it takes the exclusive side with a try-lock and never makes a backup wait), and a sweep that could not run or finish is repeated by the scheduler's next tick. This cleanup inside a backup never waits: while a restore or verification of the folder is queued or running, or when it fails, a `prune` job for the whole folder is queued instead (`Service.StartPrune`, at most one active per folder), which survives a restart. Setting or lowering a limit queues the same job. The job queue lets the cleanup of a folder take turns with the other jobs of that folder (`jobs.Runner.claim`): it is not claimed while a backup, a restore or a verification of the folder runs, and none of them is claimed while it runs, so a job that has to wait stays queued and holds no worker. Lowering a limit first needs `confirm_prune` when it would delete versions (`PruneNotConfirmedError`; the preview uses the same SQL as the deletion), and a limit cannot change while a cleanup of the folder runs. While the versions of a folder are being deleted (`Service.pruning`, or a running `prune` job), `StartRestore` and `StartVerify` refuse that folder (`ErrPruneRunning`), and a restore or verification that was queued before waits for the deletion to end (`waitWhilePruning`). Verification of a partial point skips the comparison with the manifest. See [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md).

## Data model (PostgreSQL)

History uses validity ranges so a recovery point only stores what changed:

| Table | Meaning |
|---|---|
| `sources` | Monitored folders: name, folder (`root_path`), `display_path` (NULL = none; the network path people know it by, at most 520 characters), exclude patterns, `enabled`, `schedule_minutes` (NULL = manual only, otherwise 1 to 43200), `last_run_at` (when the last backup run ended), `last_run_ms` (how long it took), `last_walk_at` and `last_walk_items` (when the last complete scan started and how many entries Everything held below the folder then), and `max_versions` (NULL = keep every version, otherwise 1 to 10000). |
| `recovery_points` | One row per backup attempt: sequence number `seq` per source, `state`, `consistency`, counters, manifest digest, `verification`, and `partial` (older versions of some files were deleted by the version limit). |
| `nodes` | One row per path slot, identified by (`parent_id`, exact `name`); `name_key` is the lower-cased name used for sorting and case-insensitive lookup. The root of a source has `parent_id` NULL. |
| `node_versions` | Each state of a slot: folder or file, size, modification time, attributes, content hash, `state` (`ok` or `skipped` with a reason), valid for the sequence numbers `first_point`..`last_point` (`last_point` NULL while open). |
| `objects` | Content objects that exist in the repository (hash, size). |
| `jobs`, `job_events`, `restore_items` | Background jobs (kinds `backup`, `verify`, `restore` and `prune`), their log lines and per-item restore results. |
| `audit_events` | Append-only (a trigger rejects UPDATE and DELETE), with `duration_ms` for the operations that have a duration. |

A file that is unchanged between two points costs no new row. A folder that disappears closes the versions of its whole subtree with one recursive statement. A path that is deleted and later comes back gets a new version on the same node. Rename and move are recorded as delete plus add (bytes are still stored once, because content is addressed by hash).

A build writes its rows under its own sequence number and closes old versions at the previous committed sequence number. Because readers only use committed sequence numbers, none of this is visible until the point is committed. After a crash or cancellation the next build (or the start-up recovery) deletes every version with `first_point` greater than the newest committed sequence and re-opens every version closed at it.

## Recovery point lifecycle

`building` → `committed`, or `failed` / `aborted`. After commit `verification` is `validated` (the commit-time checks passed: every object this build newly recorded exists with the right size), then `verified` after a deep integrity check, or `failed` when a check finds a problem; a point with `verification = failed` is not restorable. A normal backup does not re-check the content of files it carries over unchanged; an integrity check (Verify) finds repository objects that went missing or were damaged since, and a **Full re-read** backup writes missing content again. Job phases shown in the UI: `snapshot_preparing` → `scanning` (enumerating and copying are interleaved per folder) → `manifest_validating` → `committing` → `committed`; after a backup with a version limit also `pruning` (deleting old versions) and `sweeping` (freeing space), ending again at `committed`.

The **manifest digest** is a SHA-256 over one canonical line per path (path, kind, size, modification time, attributes, content hash) in a fixed order. The backup computes it while it scans; verification recomputes it from the catalog. If the two differ, the catalog was changed after commit.

## Repository

```text
<repository>/
  timevault-repository.json      format version and repository id
  objects/ab/cd/<sha256>         immutable content, read-only attribute set
  tmp/                           objects being written; stale files are cleaned
```

An object is streamed to `tmp/`, hashed while it is written, flushed, and only then renamed to its final name, so `objects/` never holds a partial object. Before a recovery point is committed the backup flushes the folders that received new objects (`Barrier`), so a power failure cannot leave the catalog pointing at an object whose directory entry was lost; this is best effort and has not been tested against a real power loss. The repository refuses a non-empty folder that is not a TimeVault repository. Writers of identical content agree on who created the object (striped lock per hash prefix).

## Consistency on a live disk

The MVP reads the live file system (`consistency = live_scan`); it does not use a VSS snapshot. To avoid storing torn copies:

- A file's size and modification time are read from the open handle before and after the copy. If they differ the file is read again (up to three attempts), then recorded as *not captured* with the reason.
- A file locked by another process (sharing violation) or unreadable is recorded as *not captured*; if an earlier good version exists it is kept and a warning is logged.
- A folder that cannot be listed keeps its previous state; its contents are never treated as deleted.
- Junctions, symbolic links and other reparse points are not followed; they are recorded as *not captured*. A file or folder that is replaced by a link after its folder was listed is detected and not followed either, so another file's content is never recorded under the wrong path.
- Names that Windows cannot represent safely (reserved device names, trailing dots or spaces, invalid characters) are skipped and counted.
- Paths deeper than 256 folders are recorded as *not captured*, because browsing and restoring accept at most that depth.
- Overlap checks (a source against the repository, the restore folder and other sources) compare where the folders really are, resolved through junctions and links, both when a source is added and when each backup starts.

The UI labels every point *Live scan* so nobody mistakes it for a snapshot.

## Restore flow

1. **Preview** (`Preview`): validates the request, resolves each selected path at the chosen point, classifies every item against the destination (create, overwrite, rename, skip; an existing file counts as identical only when its size, modification time and SHA-256 all match the catalog, so existing files are read while planning) and checks that every content object exists with the right size. Nothing is written. The result carries a digest of the plan.
2. **Confirm**: the token is an HMAC over (user, plan digest, expiry of 15 minutes). The service re-plans and compares digests, so a changed destination or a different request makes the preview stale. Replacing existing files additionally needs an explicit confirmation.
3. **Execute** (background job): plans again and refuses to write anything if items are blocked (missing content, not captured, unsafe name) unless *allow partial* was chosen. Every file is streamed from the repository into a temporary file in the destination folder, its SHA-256 and size are checked, the modification time and attributes are applied, and only then it is renamed into place. All paths go through `os.Root` on the destination folder. Per-item outcomes are stored for the job page.

Restores only write below `TIMEVAULT_RESTORE_ROOT`, into a folder named by the requester (created when it does not exist; when it already exists, for example for a repeated restore, the conflict policy decides what happens to files that are already there). The destination folder must be a plain folder: if it already exists as a junction or symbolic link the restore is refused, and the restore root is compared with the repository and every source by its real location. Nothing in a restore deletes or overwrites source data or repository content.

---

# 中文

## 概述

TimeVault 是一个 Go 服务 `timevault-server`（`cmd/timevault-server`），同时承载网页界面、JSON API 和后台任务。历史记录保存在 PostgreSQL 中，文件内容保存在独立的、按内容寻址的仓库文件夹中。它只读取备份源文件夹，绝不修改它们。任何人都可以浏览；来自 Redis 用户目录的公司账号登录后才能备份、恢复或修改设置（0.1.6 版；见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)）。

本文档描述已经实现的内容。plan.md 中提到但 TimeVault 尚未实现的部分，见 [mvp-status.md](./mvp-status.md)。

## 组件

| 包 | 职责 |
|---|---|
| `internal/config` | 从环境变量和 `.env` 文件读取并校验 `TIMEVAULT_*` 设置（环境变量优先），点名文件中被环境变量覆盖的设置（启动日志会就此给出警告），并为设置页描述当前运行的配置（不含机密信息）。 |
| `internal/db`、`migrations/` | pgx 连接池、内嵌且带校验和的 SQL 迁移（`0001_init`、`0002_schedule`、`0003_display_path`、`0004_v014`），以及服务级的实例锁。 |
| `internal/pathutil` | 兼容 Windows 的名称校验、相对路径清理、不区分大小写的包含判断、解析路径真实位置（包括联接点和链接），以及网络路径的解析（`ParseUNC`，`\\host\share\folder`）。 |
| `internal/platform` | 通过 build tag 隔离的 Windows 文件属性、磁盘空间、文件锁检测、真实路径解析、文件夹刷新和进程优先级。 |
| `internal/repository` | 内容寻址对象存储（整文件 SHA-256），原子写入，校验，删除对象。 |
| `internal/catalog` | 监控文件夹（备份源）及其计划、显示文件夹和版本数上限、恢复点、按路径的版本历史及其查询、旧版本的删除（`PruneVersions`、`PreviewPrune`），以及没有任何版本引用的对象列表。 |
| `internal/backup` | 扫描器、变更检测、内容采集、恢复点状态机、完整性校验、对"无变化的自动备份"的跳过、监控文件夹及其显示文件夹的校验，以及旧版本的删除（`Prune`）和无人引用的仓库内容的删除（`SweepObjects`），它们位于每个备份都会持有的一道闸门之后。 |
| `internal/restore` | 试运行（dry-run）计划、确认令牌、暂存并校验的恢复。 |
| `internal/jobs` | 存放在 PostgreSQL 中的持久化任务队列，含 worker、进度和任务日志。需要等待同一文件夹中另一个任务的任务（每个文件夹同一时间一个备份；旧版本的清理与该文件夹的其他任务轮流执行）保持排队状态，不占用任何 worker。 |
| `internal/listing` | 文件夹列表的引擎：项目的类型、在内存中筛选、排序和分页，以及统计文件夹中各类型的数量。它不关心项目从哪里来。 |
| `internal/search` | 搜索提供者接口、Everything HTTP JSON 适配器（搜索、文件夹内各文件夹及其大小，以及供调度器使用的变化探测）、mock 与禁用提供者。 |
| `internal/auth` | 身份、角色和权限；可选的本地管理员的 PBKDF2 哈希；签名会话 Cookie；`Directory` 接口（以及用于单点登录的 `SSODirectory`）和对已登录账号的重新检查；CSRF 校验；登录限制。 |
| `internal/redisuser` | 另一个应用的用户目录，只读地从 Redis 读取：受限的 pickle 解码器、登录适配器（`auth.Directory`）、公司单点登录服务结果（`sso_login_<token>`）的读取器，以及 `check-redis-users` 报告。 |
| `internal/audit` | 只追加的审计记录，对有耗时的操作记录耗时。 |
| `internal/service` | 界面与 API 共用的应用层（带 Everything 文件夹大小的实时浏览、历史浏览、任务、恢复、监控文件夹及其显示文件夹策略、带间隔控制和 Everything 探测的调度器、版本数上限及应用它的清理、健康状态，以及在文本中隐藏服务器真实文件夹的 `PathMap`）。 |
| `internal/server`、`web/` | HTTP 路由与访问控制（`open`、`openChecked`、`need`）、中间件、服务端渲染页面（Go 模板）、JSON API、内嵌静态资源、EN/中文文案、列表控件（排序链接、类型筛选、分页）、单点登录流程（`sso.go`）、路径前缀（`basepath.go`）以及 `visibility.go`（决定每一类访问者能看到服务器上的哪些文件夹）。 |

## 数据流

**备份。** 备份任务先取得该备份源的 advisory 锁，清除之前未完成的构建留下的内容，创建状态为 `building` 的恢复点，逐个文件夹遍历备份源，把新增或变化的文件复制进仓库，每个文件夹的变更在一个事务中写入，校验自己新记录的每个对象仍然存在，把仓库文件夹刷新到磁盘，最后用一条更新语句把恢复点标记为 `committed`。这条更新一旦发出就不会被放弃：之后才到达的取消操作，不会把已提交的恢复点变成失败的任务。读取方只会查询已提交的恢复点，所以构建中、失败或被取消的恢复点永远不可见。

**单实例。** 同一个数据库（schema）只能运行一个服务。启动时，在做任何恢复之前，服务会在一个专用连接上获取 PostgreSQL advisory 锁；第二个服务会以"another TimeVault service is already running against this database"退出。否则，第二个服务启动时的恢复会把第一个服务正在运行的任务标记为失败。

**浏览。** 文件浏览页通过 `os.Root` 列出磁盘上当前的文件夹（精心构造的路径无法跳出备份源），并与最新已提交的恢复点对比，把每一项标记为*已保护*、*已变更*或*新增*。在该恢复点存在、但磁盘上已经没有的项目，会列为*自上次备份以来已删除*。时间机器页面则列出同一文件夹在任意已提交恢复点时的样子。两者都会读取文件夹的**全部**子项（一次目录读取，或一条没有 `LIMIT` 的 SQL 查询），再交给 `internal/listing`，由它按类型筛选、排序、截取请求的那一页，并统计整个文件夹中各类型的数量；页面和 API 使用同一个调用（`Service.ListLive`、`Service.ListHistory`），所以它们不会不一致。见 [ADR 0008](./adr/0008-folder-lists-are-cut-on-the-server.md)。排序、类型筛选和页码都是查询参数，所以每个视图都有可以分享的地址。

**显示文件夹。** 人们通过网络访问文件夹，所以每个监控文件夹都有一个显示路径（`sources.display_path`），恢复文件夹有一个显示根路径（`TIMEVAULT_RESTORE_DISPLAY_ROOT`）。`Service.AddSource` 和 `UpdateSource` 在引擎查看磁盘之前先应用这套策略：设置了 `TIMEVAULT_SERVER_HOST` 时显示路径是必填的，并且它的主机必须是该主机，否则它是可选的，只检查语法（`backup.SourceError` 携带消息键和参数，所以设置页可以用访问者的语言回答）。每个有内容要显示的请求，`internal/server/visibility.go` 都会为该账号构建一个 `viewer`：管理员得到原样的文本，其他人得到经过 `service.PathMap`（`Mask`、`MaskValue`）处理的文本，它会替换备份源、仓库和恢复文件夹的每一个真实文件夹。见 [ADR 0007](./adr/0007-display-folders-and-hiding-server-paths.md)。

**搜索。** 搜索框把文本交给 `search.Provider`。Everything 适配器会清理文本、把查询限定在备份源根目录内、以受限的页大小调用仅限本机回环的 HTTP JSON 接口，并对每个结果再次检查是否位于根目录内。搜索引擎不可用时，页面显示降级提示；浏览和历史功能不受影响。

**恢复。** 恢复先预览（不写入任何内容），再用与用户和该计划绑定的令牌确认，然后作为后台任务写入所配置恢复根目录下的文件夹。见[恢复流程](#恢复流程)。

**登录与访问控制。** `internal/server` 中的每条路由都用三种包装之一注册：`open`（任何人；只读取 Cookie，所以浏览从不依赖 Redis）、`openChecked`（任何人，但带有 Cookie 的访问者会先向用户目录核实，因为页面对账号展示的内容比对访问者更多）和 `need(权限, 处理函数)`（必须是具有该权限的、已登录且有效的账号；对会改变状态的方法校验 CSRF 令牌；被拒绝时以 `access.denied` 记入审计）。权限有：`browse`（所有人）、`account`（任何已登录的账号，用于退出）、`backup`、`restore`、`audit`（审计日志、状态页和设置页），这三项对每个已登录账号开放，以及仅限 `admin` 账号的 `manage`。登录由 `auth.Manager.Login` 处理：可选的本地管理员的名字只与它自己的哈希比对，绝不去别处查找；其他名字交给 `auth.Directory`，由 `internal/redisuser` 实现，即找到 `app_user_*` 哈希（先 `SCAN`，再 `HGETALL`），在不执行任何内容的前提下解码其字段，再比对密码。会话 Cookie 携带账号、姓名、角色和 Redis 键；`Manager.Verify` 对每个账号和记录最多每分钟向目录再核实一次（`Refresh`），所以停用账号或更改角色在一分钟内生效；目录无应答时，每次按签发时的状态信任会话 15 秒；访问者已放弃的请求不会被当作故障而计入。登录尝试在开始时就按客户端地址和账号名分别计数。

**自动备份。** `service.RunScheduler` 在自己的 goroutine 中运行，在任务运行器之后启动。它每 15 秒（启动时也运行一次）向 catalog 查询到期的备份源（`enabled`、设置了 `schedule_minutes`，且 `last_run_at` 为空或距今至少这么多分钟），并按需要程度依次处理它们（`rankDue`：没有已提交恢复点的先来，然后是相对于自己的间隔超期最多的，再然后是上次运行最快的，最后是 id）。已经有排队或运行中备份的备份源会被跳过。开启 Everything 探测后，如果 Everything 说某个到期的备份源没有变化，就立即处理完毕、不做扫描，并让它的间隔重新开始（`probeUnchanged`：Everything 认识该文件夹、3 秒内应答、其下的条目数量与上次完整扫描之前记录的数量相同、自上次扫描前 5 分钟以来没有看到任何创建、修改、重命名、移动或属性变化、最新的恢复点没有未捕获的项目，并且那次扫描比 `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY` 更新；一个周期最多询问 Everything 25 次，没有应答的 Everything 在一分钟内不会再被询问，`probeBackedOff`）。更改备份源的排除规则会清除它上次的扫描记录（`catalog.UpdateSource`），所以它的下一次运行会进行扫描。第一个必须扫描的备份源会在间隔状态允许时被排入一个请求者为 `scheduler` 的备份任务（`mayStartScan`：排队或运行中的自动备份少于 `TIMEVAULT_SCHEDULER_PARALLEL` 个，并且自上次启动以来的间隔和自上次结束以来的休息都已过去；间隔状态保存在内存中），然后这个周期结束。见 [ADR 0011](./adr/0011-staggered-scheduler-and-change-probe.md)。备份以 `SkipUnchanged` 运行：扫描之后，如果没有任何路径被新增、修改或删除，且无法读取的项目数量与最新已提交的恢复点相同，引擎会丢弃它已开始创建的恢复点并返回 `ErrUnchanged`；任务运行器随后删除该任务（`jobs.ErrForget`），也不写入审计事件。无论一次运行的结果如何，`last_run_at` 和 `last_run_ms` 都在其结束时设置，完整扫描之后还会设置 `last_walk_at` 和 `last_walk_items`（`recordRun`），所以下一次在一个间隔之后到期。引擎先取得备份源的 advisory 锁，然后才读取备份源；更改备份源的文件夹要取得同一把锁，并且在它的备份运行期间会被拒绝（`ErrBusy`）。

**单点登录。** `internal/server/sso.go` 在内存中保存进行中的登录（令牌、客户端地址、登录后要去的页面、时间）。`POST /auth/sso/start` 签发一个令牌（15 个随机字节，24 个字符），并连同它返回公司单点登录服务的地址；浏览器在一个窗口中打开该地址。`POST /auth/sso/poll` 查找该流程，把客户端地址与发起它的地址比较（设置了 `TIMEVAULT_SSO_REQUIRE_SAME_IP` 时），通过 `auth.SSODirectory.SSOResult` 读取 `sso_login_<token>`（由 `internal/redisuser` 用 `GET` 读取，并用与账号相同的受限 pickle 解码器解码），用 `Identify` 把工号对应到有效账号（最新的记录，且必须已激活），然后开始与密码登录相同的会话（`Manager.StartSession`）。随后该流程被移除，所以一个结果只能使用一次；Redis 中的键从不被删除，而是自己过期。客户端地址来自 `Server.clientIP`，它只信任来自 `TIMEVAULT_TRUSTED_PROXIES` 的 `X-Forwarded-For`。当检查因为地址而拒绝一次登录时，`Server.addressDetail` 会把请求是怎样到达服务器的（`connection`、`trusted_proxy`、`forwarded`）加入审计记录，同样的内容也会写入日志。见 [ADR 0009](./adr/0009-single-sign-on-and-trusted-proxies.md)。

**路径前缀。** 设置 `TIMEVAULT_BASE_PATH`（例如 `/tv`）后，站点可以放在多个应用共用的主机名的某个路径下提供服务。内部的路由、重定向目标和模板路径仍然不带前缀地书写；`internal/server/basepath.go` 在进入时去掉前缀，在出去时加上前缀。`Server.withBase` 是最外层的处理器：路径以前缀开头的请求会去掉前缀（`http.StripPrefix`），只有前缀本身的请求会被送到带斜杠的同一地址，其他请求原样处理，所以代理可以保留也可以去掉前缀。它用 `prefixWriter` 包装响应，给每个指向应用路径的 `Location` 头加上前缀：页面的重定向、API 的 `Location` 以及 `ServeMux` 自己的重定向。模板用 `url` 函数（`Server.href`）写每个地址，链接中携带的、用于回到某个页面的地址（`next`、`back`）用 `route` 写，脚本从 `<meta name="tv-base">` 读取前缀，Cookie 的路径来自 `Server.cookiePath` 和 `auth.Options.CookiePath`。从浏览器返回的地址要经过 `Server.local` 和 `Server.safeNext`，它们会去掉前缀并把结果再检查一遍。两个测试保证各部分不出错：任何模板都不能写以斜杠开头的地址，任何代码都不能设置作用于整个主机的 Cookie。见 [ADR 0012](./adr/0012-base-path-behind-a-reverse-proxy.md)。

**文件夹大小。** 当实时列表中有文件夹时，`Service.ListLive` 会请 `fillFolderSizes` 提供它们的大小：一次 `Everything.Subfolders` 调用（`infolder:"<dir>" folder:`，分页，最多 20,000 个文件夹），答案缓存 10 秒。一次询问最多等待 2 秒；Everything 没有应答时，大小保持未知（显示为 `-`），并且 15 秒内不再询问。大小参与按大小排序。时间机器和版本页面读取的是目录库，不使用它们。

**版本数上限。** 一次备份提交了恢复点之后，如果文件夹有上限，`pruneAfterBackup` 会调用 `Engine.Prune`：持有备份源的写入锁，由 `catalog.PruneVersions` 删除（每个事务 500 个文件）这次备份改动过的文件中落在该文件最新 N 个已捕获版本之外的版本，并在同一个事务中把失去版本的恢复点标记出来（`recovery_points.partial`）。删除了版本之后，`Engine.SweepObjects` 会删除没有任何版本引用的对象（先删文件，再删它的记录），时间限制为 2 分钟；它只在没有备份持有备份引擎中那道闸门的共享侧时运行（它以尝试加锁的方式取得独占侧，绝不让备份等待），没有能运行或没有能完成的清扫会由调度器的下一个周期重复。备份内部的这一步清理从不等待：当该文件夹的恢复或校验排队或正在运行，或者它失败时，会改为排入一个针对整个文件夹的 `prune` 任务（`Service.StartPrune`，每个文件夹最多一个活动任务），它在重启后依然存在。设置或调低上限会排入同样的任务。任务队列让一个文件夹的清理与该文件夹的其他任务轮流执行（`jobs.Runner.claim`）：该文件夹的备份、恢复或校验正在运行时不会领取它的清理，清理运行时也不会领取它们中的任何一个，所以需要等待的任务保持排队状态，不占用任何 worker。调低上限在会删除版本时，首先需要 `confirm_prune`（`PruneNotConfirmedError`；预览与删除使用同一段 SQL），并且文件夹的清理正在运行时不能更改上限。在删除某个文件夹的版本期间（`Service.pruning`，或一个正在运行的 `prune` 任务），`StartRestore` 和 `StartVerify` 会拒绝该文件夹（`ErrPruneRunning`），而在此之前已排队的恢复或校验会等到删除结束（`waitWhilePruning`）。校验不完整的恢复点时会跳过与清单的比较。见 [ADR 0010](./adr/0010-version-limit-and-garbage-collection.md)。

## 数据模型（PostgreSQL）

历史使用"有效期区间"模型，因此恢复点只保存发生变化的部分：

| 表 | 含义 |
|---|---|
| `sources` | 监控文件夹：名称、文件夹（`root_path`）、`display_path`（NULL 表示没有；人们所知的网络路径，最多 520 个字符）、排除规则、`enabled`、`schedule_minutes`（NULL 表示仅手动，否则为 1 到 43200）、`last_run_at`（上一次备份运行结束的时间）、`last_run_ms`（它花了多久）、`last_walk_at` 和 `last_walk_items`（上一次完整扫描开始的时间，以及当时 Everything 在该文件夹之下记录的条目数量），以及 `max_versions`（NULL 表示保留每个版本，否则为 1 到 10000）。 |
| `recovery_points` | 每次备份尝试一行：每个备份源内的序号 `seq`、`state`、`consistency`、计数器、清单摘要、`verification`，以及 `partial`（版本数上限删除了某些文件的较旧版本）。 |
| `nodes` | 每个路径位置一行，由（`parent_id`，精确 `name`）标识；`name_key` 是小写化的名称，用于排序和不区分大小写的查找。备份源的根节点 `parent_id` 为 NULL。 |
| `node_versions` | 路径位置经历的每个状态：文件夹或文件、大小、修改时间、属性、内容哈希、`state`（`ok`，或带原因的 `skipped`），在序号 `first_point`..`last_point` 之间有效（仍然有效时 `last_point` 为 NULL）。 |
| `objects` | 仓库中存在的内容对象（哈希、大小）。 |
| `jobs`、`job_events`、`restore_items` | 后台任务（类型 `backup`、`verify`、`restore` 和 `prune`）、任务日志行，以及恢复的逐项结果。 |
| `audit_events` | 只追加（触发器会拒绝 UPDATE 和 DELETE），对有耗时的操作带有 `duration_ms`。 |

两个恢复点之间没有变化的文件不产生任何新行。文件夹消失时，用一条递归语句关闭其整棵子树的版本。被删除后又重新出现的路径，会在同一个节点上产生新版本。重命名和移动会被记录为"删除 + 新增"（由于内容按哈希寻址，字节仍然只存一份）。

构建过程使用自己的序号写入新行，并把旧版本在"上一个已提交序号"处关闭。读取方只使用已提交的序号，所以在恢复点提交之前，这些改动都不可见。崩溃或取消之后，下一次构建（或启动时的恢复）会删除所有 `first_point` 大于最新已提交序号的版本，并重新打开所有在该序号处被关闭的版本。

## 恢复点生命周期

`building` → `committed`，或 `failed` / `aborted`。提交后 `verification` 为 `validated`（提交时的检查已通过：本次构建新记录的每个对象都存在且大小正确），完整性深度校验通过后为 `verified`，发现问题则为 `failed`；`verification = failed` 的恢复点不可恢复。普通备份不会重新检查它原样沿用的未变化文件的内容；完整性校验（Verify）可以发现之后丢失或损坏的仓库对象，而**完整重读**备份会把缺失的内容重新写入。界面显示的任务阶段：`snapshot_preparing` → `scanning`（枚举与复制按文件夹交替进行）→ `manifest_validating` → `committing` → `committed`；有版本数上限的备份之后还有 `pruning`（删除旧版本）和 `sweeping`（释放空间），并重新以 `committed` 结束。

**清单摘要**是对每个路径一行规范文本（路径、类型、大小、修改时间、属性、内容哈希）按固定顺序计算的 SHA-256。备份扫描时计算它；校验时从目录库重新计算。两者不一致说明提交之后目录库被改动过。

## 仓库

```text
<repository>/
  timevault-repository.json      格式版本与仓库 id
  objects/ab/cd/<sha256>         不可变内容，已设置只读属性
  tmp/                           正在写入的对象；过期文件会被清理
```

对象先以流方式写入 `tmp/`，边写边计算哈希，刷新到磁盘后才重命名为最终名称，因此 `objects/` 中永远不会出现不完整的对象。在恢复点提交之前，备份会先刷新接收了新对象的文件夹（`Barrier`），这样断电就不会让目录库指向一个目录项已丢失的对象；这是尽力而为的做法，尚未在真实断电下测试过。仓库会拒绝一个非空且不是 TimeVault 仓库的文件夹。写入相同内容的多个写入者通过按哈希前缀分段的锁，对"谁创建了对象"达成一致。

## 在线磁盘上的一致性

MVP 读取的是在线文件系统（`consistency = live_scan`），不使用 VSS 快照。为避免保存"撕裂"的副本：

- 在复制前后，从已打开的句柄读取文件大小和修改时间。若两者不同，则重新读取（最多三次），之后记录为*未能备份*并写明原因。
- 被其他进程锁定（共享冲突）或无法读取的文件会记录为*未能备份*；如果之前存在可用版本，则保留旧版本并记录警告。
- 无法列出的文件夹保持其之前的状态，其内容绝不会被当作已删除。
- 联接点（junction）、符号链接和其他重解析点不会被跟随，而是记录为*未能备份*。如果文件或文件夹在其所在文件夹被列出之后被替换成链接，也会被检测到并且不会被跟随，因此不会把另一个文件的内容记录在错误的路径下。
- Windows 无法安全表示的名称（保留设备名、结尾为点或空格、非法字符）会被跳过并计数。
- 深度超过 256 层文件夹的路径会被记录为*未能备份*，因为浏览和恢复最多接受这个深度。
- 重叠检查（备份源与仓库、恢复目录以及其他备份源之间）比较的是文件夹的真实位置，即解析了联接点和链接之后的位置；添加备份源时和每次备份开始时都会检查。

界面把每个恢复点都标注为*实时扫描*，避免被误认为快照。

## 恢复流程

1. **预览**（`Preview`）：校验请求，在所选恢复点解析每个所选路径，把每一项与目标位置对比并分类（创建、替换、改名、跳过；已有文件只有在大小、修改时间和 SHA-256 都与目录库一致时才算"完全一致"，因此规划时会读取已有文件），并检查每个内容对象存在且大小正确。不会写入任何内容。结果带有该计划的摘要。
2. **确认**：令牌是对（用户、计划摘要、15 分钟有效期）的 HMAC。服务会重新生成计划并比较摘要，所以目标位置变化或请求不同都会使预览失效。替换已有文件还需要额外的明确确认。
3. **执行**（后台任务）：再次生成计划，如果存在被阻止的项目（内容缺失、未备份成功、名称不安全），除非选择了*允许部分恢复*，否则不会写入任何内容。每个文件从仓库流式写入目标文件夹中的临时文件，校验 SHA-256 和大小，应用修改时间和属性，最后才重命名到位。所有路径都通过针对目标文件夹的 `os.Root` 访问。逐项结果会保存下来，供任务页面查看。

恢复只会写入 `TIMEVAULT_RESTORE_ROOT` 之下、由请求者命名的文件夹（不存在时创建；已存在时，例如重复恢复，由冲突策略决定如何处理已有的文件）。目标文件夹必须是普通文件夹：如果它已经作为联接点或符号链接存在，恢复会被拒绝；恢复根目录与仓库以及每个备份源比较的是它们的真实位置。恢复操作不会删除或覆盖源数据和仓库内容。
