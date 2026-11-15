# English

## Overview

TimeVault is one Go service, `timevault-server` (`cmd/timevault-server`), that hosts the web UI, the JSON API and the background workers. It keeps history in PostgreSQL and file content in a separate, content-addressed repository folder. It reads source folders; it never changes them. Anyone can browse; company accounts from a Redis user directory sign in to back up, restore or change settings (version 0.1.1; see [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)).

This document describes what is implemented. Items that plan.md describes but TimeVault does not do are listed in [mvp-status.md](./mvp-status.md).

## Components

| Package | Responsibility |
|---|---|
| `internal/config` | Reads and validates the `TIMEVAULT_*` settings from the environment and the `.env` file (the environment wins), and describes the running configuration for the Settings page without secrets. |
| `internal/db`, `migrations/` | pgx connection pool, the embedded, checksummed SQL migrations (`0001_init`, `0002_schedule`), and the service-wide instance lock. |
| `internal/pathutil` | Windows-compatible name validation, source-relative path cleaning, case-insensitive containment, and resolution of a path's real location (junctions and links included). |
| `internal/platform` | Windows file attributes, disk space, lock detection, real-path resolution and folder flushing behind build tags. |
| `internal/repository` | Content-addressed object store (whole-file SHA-256), atomic writes, verification. |
| `internal/catalog` | Monitored folders (sources) with their schedule, recovery points, per-path version history and its queries. |
| `internal/backup` | Scanner, change detection, content capture, recovery-point state machine, verification, skipping of unchanged automatic runs. |
| `internal/restore` | Dry-run planner, confirmation tokens, staged and verified restore. |
| `internal/jobs` | Durable job queue in PostgreSQL with workers, progress and a per-job log. |
| `internal/search` | Search provider interface, Everything HTTP JSON adapter, mock and disabled providers. |
| `internal/auth` | Identities, roles and permissions; PBKDF2 hash of the optional local administrator; signed session cookies; the `Directory` interface and re-checking of signed-in accounts; CSRF checks; sign-in limits. |
| `internal/redisuser` | The user directory of another application, read-only from Redis: a restricted pickle decoder, the sign-in adapter (`auth.Directory`) and the `check-redis-users` report. |
| `internal/audit` | Append-only audit trail. |
| `internal/service` | Application layer shared by the UI and the API (live browsing, history browsing, jobs, restore, monitored folders, the scheduler, health). |
| `internal/server`, `web/` | HTTP routing and access control (`open`, `openChecked`, `need`), middleware, server-rendered pages (Go templates), JSON API, embedded assets, EN/中文 messages. |

## Data flow

**Backup.** A backup job takes the per-source advisory lock, discards anything an unfinished earlier build left behind, creates a `building` recovery point, walks the source folder by folder, copies new or changed files into the repository, writes the changes for each folder in one transaction, validates that every object it newly recorded still exists, flushes the repository folders to disk, and finally marks the point `committed` in a single update. Once that update is issued it is not abandoned: a cancellation that arrives later cannot turn a committed point into a failed job. Readers only ever query committed points, so a point that is being built, failed or was cancelled is never visible.

**Single instance.** Only one service may run against a database (schema). At start, before it recovers anything, the service takes a PostgreSQL advisory lock on a dedicated connection; a second service stops with "another TimeVault service is already running against this database". Without this, the second service's start-up recovery would mark the first one's running jobs as failed.

**Browse.** The Explorer lists the folder as it is on disk (through `os.Root`, so a crafted path cannot leave the source) and marks each item *protected*, *changed* or *new* by comparing it with the newest committed point. Items that existed at that point but are gone from disk are listed as *deleted since the last backup*. The Time Machine page lists the same folder as it was at any committed point.

**Search.** The search box sends the text to a `search.Provider`. The Everything adapter sanitizes the text, confines the query to the source root, calls the loopback-only HTTP JSON endpoint with a bounded page size and re-checks every result against the root. When the engine is down the page shows a degraded notice; browsing and history keep working.

**Restore.** A restore is previewed first (nothing is written), then confirmed with a token that is bound to the user and to the exact plan, then run as a background job into a folder below the configured restore root. See [Restore flow](#restore-flow).

**Sign-in and access.** Every route is registered with one of three wrappers in `internal/server`: `open` (anyone; reads only the cookie, so browsing never depends on Redis), `openChecked` (anyone, but a visitor with a cookie is first checked with the user directory because the page shows an account more than a visitor) and `need(permission, handler)` (a signed-in, active account with the permission; checks the CSRF token on state-changing methods; audits a refusal as `access.denied`). The permissions are `browse` (everyone), `account` (any signed-in account, for signing out), `backup`, `restore`, `audit` (the audit log, the status and settings pages) for every signed-in account, and `manage` for `admin` accounts only. A sign-in goes through `auth.Manager.Login`: the name of the optional local administrator is checked against its own hash and never looked up elsewhere; any other name is passed to the `auth.Directory`, which `internal/redisuser` implements by finding the `app_user_*` hash (`SCAN`, then `HGETALL`), decoding its fields without executing anything, and comparing the password. The session cookie carries the account, name, role and Redis key; `Manager.Verify` asks the directory again at most once a minute per account and record (`Refresh`), so a deactivation or role change takes effect within a minute, trusts the session as issued for 15 seconds at a time while the directory does not answer, and never takes a request that the visitor abandoned for an outage. Sign-in attempts are counted when they start, per client address and per account name.

**Automatic backups.** `service.RunScheduler` runs in its own goroutine, started after the job runner. Every 15 seconds (and once at start) it asks the catalog for the sources that are due (`enabled`, `schedule_minutes` set, and `last_run_at` empty or at least that many minutes ago), skips a source that already has a queued or running backup, and queues a backup job whose requester is `scheduler`. The backup runs with `SkipUnchanged`: after the scan, when no path was added, modified or deleted and the number of items that could not be read equals that of the newest committed point, the engine discards the recovery point it had begun and returns `ErrUnchanged`; the job runner then deletes the job (`jobs.ErrForget`) and no audit event is written. Whatever the outcome of a run, `last_run_at` is set when it ends, so the next one is due one interval later. The engine takes the source's advisory lock first and reads the source only then; changing the folder of a source takes the same lock and is refused (`ErrBusy`) while a backup of it runs.

## Data model (PostgreSQL)

History uses validity ranges so a recovery point only stores what changed:

| Table | Meaning |
|---|---|
| `sources` | Monitored folders: name, folder, exclude patterns, `enabled`, `schedule_minutes` (NULL = manual only, otherwise 1 to 43200) and `last_run_at` (when the last backup run ended). |
| `recovery_points` | One row per backup attempt: sequence number `seq` per source, `state`, `consistency`, counters, manifest digest, `verification`. |
| `nodes` | One row per path slot, identified by (`parent_id`, exact `name`); `name_key` is the lower-cased name used for sorting and case-insensitive lookup. The root of a source has `parent_id` NULL. |
| `node_versions` | Each state of a slot: folder or file, size, modification time, attributes, content hash, `state` (`ok` or `skipped` with a reason), valid for the sequence numbers `first_point`..`last_point` (`last_point` NULL while open). |
| `objects` | Content objects that exist in the repository (hash, size). |
| `jobs`, `job_events`, `restore_items` | Background jobs, their log lines and per-item restore results. |
| `audit_events` | Append-only (a trigger rejects UPDATE and DELETE). |

A file that is unchanged between two points costs no new row. A folder that disappears closes the versions of its whole subtree with one recursive statement. A path that is deleted and later comes back gets a new version on the same node. Rename and move are recorded as delete plus add (bytes are still stored once, because content is addressed by hash).

A build writes its rows under its own sequence number and closes old versions at the previous committed sequence number. Because readers only use committed sequence numbers, none of this is visible until the point is committed. After a crash or cancellation the next build (or the start-up recovery) deletes every version with `first_point` greater than the newest committed sequence and re-opens every version closed at it.

## Recovery point lifecycle

`building` → `committed`, or `failed` / `aborted`. After commit `verification` is `validated` (the commit-time checks passed: every object this build newly recorded exists with the right size), then `verified` after a deep integrity check, or `failed` when a check finds a problem; a point with `verification = failed` is not restorable. A normal backup does not re-check the content of files it carries over unchanged; an integrity check (Verify) finds repository objects that went missing or were damaged since, and a **Full re-read** backup writes missing content again. Job phases shown in the UI: `snapshot_preparing` → `scanning` (enumerating and copying are interleaved per folder) → `manifest_validating` → `committing` → `committed`.

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

TimeVault 是一个 Go 服务 `timevault-server`（`cmd/timevault-server`），同时承载网页界面、JSON API 和后台任务。历史记录保存在 PostgreSQL 中，文件内容保存在独立的、按内容寻址的仓库文件夹中。它只读取备份源文件夹，绝不修改它们。任何人都可以浏览；来自 Redis 用户目录的公司账号登录后才能备份、恢复或修改设置（0.1.1 版；见 [ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)）。

本文档描述已经实现的内容。plan.md 中提到但 TimeVault 尚未实现的部分，见 [mvp-status.md](./mvp-status.md)。

## 组件

| 包 | 职责 |
|---|---|
| `internal/config` | 从环境变量和 `.env` 文件读取并校验 `TIMEVAULT_*` 设置（环境变量优先），并为设置页描述当前运行的配置（不含机密信息）。 |
| `internal/db`、`migrations/` | pgx 连接池、内嵌且带校验和的 SQL 迁移（`0001_init`、`0002_schedule`），以及服务级的实例锁。 |
| `internal/pathutil` | 兼容 Windows 的名称校验、相对路径清理、不区分大小写的包含判断，以及解析路径真实位置（包括联接点和链接）。 |
| `internal/platform` | 通过 build tag 隔离的 Windows 文件属性、磁盘空间、文件锁检测、真实路径解析和文件夹刷新。 |
| `internal/repository` | 内容寻址对象存储（整文件 SHA-256），原子写入，校验。 |
| `internal/catalog` | 监控文件夹（备份源）及其计划、恢复点、按路径的版本历史及其查询。 |
| `internal/backup` | 扫描器、变更检测、内容采集、恢复点状态机、完整性校验，以及对"无变化的自动备份"的跳过。 |
| `internal/restore` | 试运行（dry-run）计划、确认令牌、暂存并校验的恢复。 |
| `internal/jobs` | 存放在 PostgreSQL 中的持久化任务队列，含 worker、进度和任务日志。 |
| `internal/search` | 搜索提供者接口、Everything HTTP JSON 适配器、mock 与禁用提供者。 |
| `internal/auth` | 身份、角色和权限；可选的本地管理员的 PBKDF2 哈希；签名会话 Cookie；`Directory` 接口以及对已登录账号的重新检查；CSRF 校验；登录限制。 |
| `internal/redisuser` | 另一个应用的用户目录，只读地从 Redis 读取：受限的 pickle 解码器、登录适配器（`auth.Directory`）和 `check-redis-users` 报告。 |
| `internal/audit` | 只追加的审计记录。 |
| `internal/service` | 界面与 API 共用的应用层（实时浏览、历史浏览、任务、恢复、监控文件夹、调度器、健康状态）。 |
| `internal/server`、`web/` | HTTP 路由与访问控制（`open`、`openChecked`、`need`）、中间件、服务端渲染页面（Go 模板）、JSON API、内嵌静态资源、EN/中文文案。 |

## 数据流

**备份。** 备份任务先取得该备份源的 advisory 锁，清除之前未完成的构建留下的内容，创建状态为 `building` 的恢复点，逐个文件夹遍历备份源，把新增或变化的文件复制进仓库，每个文件夹的变更在一个事务中写入，校验自己新记录的每个对象仍然存在，把仓库文件夹刷新到磁盘，最后用一条更新语句把恢复点标记为 `committed`。这条更新一旦发出就不会被放弃：之后才到达的取消操作，不会把已提交的恢复点变成失败的任务。读取方只会查询已提交的恢复点，所以构建中、失败或被取消的恢复点永远不可见。

**单实例。** 同一个数据库（schema）只能运行一个服务。启动时，在做任何恢复之前，服务会在一个专用连接上获取 PostgreSQL advisory 锁；第二个服务会以"another TimeVault service is already running against this database"退出。否则，第二个服务启动时的恢复会把第一个服务正在运行的任务标记为失败。

**浏览。** 文件浏览页通过 `os.Root` 列出磁盘上当前的文件夹（精心构造的路径无法跳出备份源），并与最新已提交的恢复点对比，把每一项标记为*已保护*、*已变更*或*新增*。在该恢复点存在、但磁盘上已经没有的项目，会列为*自上次备份以来已删除*。时间机器页面则列出同一文件夹在任意已提交恢复点时的样子。

**搜索。** 搜索框把文本交给 `search.Provider`。Everything 适配器会清理文本、把查询限定在备份源根目录内、以受限的页大小调用仅限本机回环的 HTTP JSON 接口，并对每个结果再次检查是否位于根目录内。搜索引擎不可用时，页面显示降级提示；浏览和历史功能不受影响。

**恢复。** 恢复先预览（不写入任何内容），再用与用户和该计划绑定的令牌确认，然后作为后台任务写入所配置恢复根目录下的文件夹。见[恢复流程](#恢复流程)。

**登录与访问控制。** `internal/server` 中的每条路由都用三种包装之一注册：`open`（任何人；只读取 Cookie，所以浏览从不依赖 Redis）、`openChecked`（任何人，但带有 Cookie 的访问者会先向用户目录核实，因为页面对账号展示的内容比对访问者更多）和 `need(权限, 处理函数)`（必须是具有该权限的、已登录且有效的账号；对会改变状态的方法校验 CSRF 令牌；被拒绝时以 `access.denied` 记入审计）。权限有：`browse`（所有人）、`account`（任何已登录的账号，用于退出）、`backup`、`restore`、`audit`（审计日志、状态页和设置页），这三项对每个已登录账号开放，以及仅限 `admin` 账号的 `manage`。登录由 `auth.Manager.Login` 处理：可选的本地管理员的名字只与它自己的哈希比对，绝不去别处查找；其他名字交给 `auth.Directory`，由 `internal/redisuser` 实现，即找到 `app_user_*` 哈希（先 `SCAN`，再 `HGETALL`），在不执行任何内容的前提下解码其字段，再比对密码。会话 Cookie 携带账号、姓名、角色和 Redis 键；`Manager.Verify` 对每个账号和记录最多每分钟向目录再核实一次（`Refresh`），所以停用账号或更改角色在一分钟内生效；目录无应答时，每次按签发时的状态信任会话 15 秒；访问者已放弃的请求不会被当作故障而计入。登录尝试在开始时就按客户端地址和账号名分别计数。

**自动备份。** `service.RunScheduler` 在自己的 goroutine 中运行，在任务运行器之后启动。它每 15 秒（启动时也运行一次）向 catalog 查询到期的备份源（`enabled`、设置了 `schedule_minutes`，且 `last_run_at` 为空或距今至少这么多分钟），跳过已经有排队或运行中备份的备份源，并排入一个请求者为 `scheduler` 的备份任务。备份以 `SkipUnchanged` 运行：扫描之后，如果没有任何路径被新增、修改或删除，且无法读取的项目数量与最新已提交的恢复点相同，引擎会丢弃它已开始创建的恢复点并返回 `ErrUnchanged`；任务运行器随后删除该任务（`jobs.ErrForget`），也不写入审计事件。无论一次运行的结果如何，`last_run_at` 都在其结束时设置，所以下一次在一个间隔之后到期。引擎先取得备份源的 advisory 锁，然后才读取备份源；更改备份源的文件夹要取得同一把锁，并且在它的备份运行期间会被拒绝（`ErrBusy`）。

## 数据模型（PostgreSQL）

历史使用"有效期区间"模型，因此恢复点只保存发生变化的部分：

| 表 | 含义 |
|---|---|
| `sources` | 监控文件夹：名称、文件夹、排除规则、`enabled`、`schedule_minutes`（NULL 表示仅手动，否则为 1 到 43200）和 `last_run_at`（上一次备份运行结束的时间）。 |
| `recovery_points` | 每次备份尝试一行：每个备份源内的序号 `seq`、`state`、`consistency`、计数器、清单摘要、`verification`。 |
| `nodes` | 每个路径位置一行，由（`parent_id`，精确 `name`）标识；`name_key` 是小写化的名称，用于排序和不区分大小写的查找。备份源的根节点 `parent_id` 为 NULL。 |
| `node_versions` | 路径位置经历的每个状态：文件夹或文件、大小、修改时间、属性、内容哈希、`state`（`ok`，或带原因的 `skipped`），在序号 `first_point`..`last_point` 之间有效（仍然有效时 `last_point` 为 NULL）。 |
| `objects` | 仓库中存在的内容对象（哈希、大小）。 |
| `jobs`、`job_events`、`restore_items` | 后台任务、任务日志行，以及恢复的逐项结果。 |
| `audit_events` | 只追加（触发器会拒绝 UPDATE 和 DELETE）。 |

两个恢复点之间没有变化的文件不产生任何新行。文件夹消失时，用一条递归语句关闭其整棵子树的版本。被删除后又重新出现的路径，会在同一个节点上产生新版本。重命名和移动会被记录为"删除 + 新增"（由于内容按哈希寻址，字节仍然只存一份）。

构建过程使用自己的序号写入新行，并把旧版本在"上一个已提交序号"处关闭。读取方只使用已提交的序号，所以在恢复点提交之前，这些改动都不可见。崩溃或取消之后，下一次构建（或启动时的恢复）会删除所有 `first_point` 大于最新已提交序号的版本，并重新打开所有在该序号处被关闭的版本。

## 恢复点生命周期

`building` → `committed`，或 `failed` / `aborted`。提交后 `verification` 为 `validated`（提交时的检查已通过：本次构建新记录的每个对象都存在且大小正确），完整性深度校验通过后为 `verified`，发现问题则为 `failed`；`verification = failed` 的恢复点不可恢复。普通备份不会重新检查它原样沿用的未变化文件的内容；完整性校验（Verify）可以发现之后丢失或损坏的仓库对象，而**完整重读**备份会把缺失的内容重新写入。界面显示的任务阶段：`snapshot_preparing` → `scanning`（枚举与复制按文件夹交替进行）→ `manifest_validating` → `committing` → `committed`。

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
