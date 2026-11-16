# English

## Overview

This is the honest status of TimeVault against [plan.md](./plan.md). It is a **vertical slice**: one working path through search/browse, backup, history and restore on a local NTFS folder. It is not a production backup system, and nothing here has been run at the 5-million-file scale. Version 0.1.1 added open browsing, company sign-in with two roles, a Settings page for the monitored folders and automatic backups; version 0.1.3 added sorted, filtered and paged folder lists with a Type column, display folders for the monitored folders, and the rule that only administrators see the server's real folders; version 0.1.4 added single sign-on, folder sizes from Everything, the duration of every operation in the audit log, a limit of versions per file with rolling deletion, and automatic backups that are spread out; version 0.1.5 lets the site be served under a path prefix such as `/tv` behind a reverse proxy. The statements below are updated for them, and [changelog.md](./changelog.md) lists what changed.

Status words: **Done** (implemented and tested), **Partial** (works with stated limits), **Not done** (planned, not implemented), **Not verified** (no evidence yet).

## Scope against plan.md §3

| Plan item | Status | Notes |
|---|---|---|
| Multi-root source registration, exclusions, source and repository health | Done | Administrators add and edit monitored folders on the Settings page or with `POST` and `PATCH /api/v1/sources` (0.1.1); exclusion globs; repository free space and write test on the status page. |
| Current folder tree, breadcrumbs, search, sort, pagination | Done | Live folder from disk with protected / changed / new markers; Everything-backed search. Since 0.1.3 the lists have a Type column, sort the whole folder by name, type, size or modified time, filter by type and come in pages of 100 (50 to 500), with buttons that jump to the top and the bottom. Since 0.1.4 the size of a folder comes from the index of Everything (the Time Machine pages show none). |
| Historical browse with timeline and recovery-point selector | Done | Time Machine page; only committed points appear. |
| File version panel | Done | Capture time, validity, size, modification time, SHA-256, content availability. "Optional change reason" is not implemented. |
| Detect new / changed / renamed / moved / deleted, tombstones | Partial | New, changed and deleted are tracked; rename and move are recorded as delete plus add. Deleted paths stay visible at older points. |
| Initial full backup and incremental backups, progress, retries, verification | Partial | Full and incremental by walking the source and comparing size and modification time; progress, cancel, integrity verification (shallow and deep). Since 0.1.1 a monitored folder can have an automatic schedule (1 to 43200 minutes); a run that finds nothing changed leaves no recovery point. A failed run is not retried before the next interval. Since 0.1.4 the automatic backups of many folders are spread out (one at a time, with a gap and a rest, in order of need) and, when turned on, skip a folder that Everything says did not change. Backups can still be started by hand or through the API. |
| Single file and folder restore, export, dry-run, conflict policies | Partial | File and folder restore with dry-run, skip / rename / overwrite, hash verification, per-item results. Restore is **only into a folder below the restore root**; restoring in place and downloading/exporting a file are not implemented. |
| Authentication, roles, path allow-lists, audit, admin settings, health | Partial | Since 0.1.1: open browsing for everyone; company accounts from the Redis user directory with two roles (`user`, `admin`) and an optional local administrator; signed sessions that are re-checked every minute, CSRF, sign-in limits, audit trail, status page and Settings page. Since 0.1.4 also single sign-on through the company sign-on service, and trusted reverse proxies for the client address. Since 0.1.5 the site can be served under a path prefix such as `/tv` behind a reverse proxy (`TIMEVAULT_BASE_PATH`). No per-folder or per-path rights. |
| Retention and safe garbage collection | Partial | Since 0.1.4 each monitored folder can have a limit of versions per file: older versions are deleted, the recovery points that lost versions are marked partial, and repository content that nobody refers to is swept while no backup runs ([ADR 0010](./adr/0010-version-limit-and-garbage-collection.md)). By default every version is kept. Not done: retention by age or by number of recovery points, a repository backup and an off-host copy. |

## Phases

| Phase | Status |
|---|---|
| P0 technical proof | Partial. The Everything adapter was verified against a real Everything 1.4.1.1024 instance. VSS and USN were **not** prototyped. No 5M-file benchmark was run. |
| P1 read-only explorer and service foundation | Done for the MVP scope (HTTP server, migrations, logging, health, templates, sign-in, search adapter tests). Not done: Windows service registration, 5M synthetic load test. |
| P2 backup and history engine | Partial. VSS consistency and USN incremental detection are not implemented; interruption recovery is tested for crash, cancel and restart. Automatic schedules exist since 0.1.1, spread out since 0.1.4. |
| P3 restore | Partial. See the restore row above; in-place restore is not implemented. |
| P4 production hardening | Not done. |

## Acceptance matrix (plan.md §9)

| Scenario | Status | Evidence |
|---|---|---|
| 5M catalog entries, bounded memory, server-side pagination | Not verified | Since 0.1.3 a page of a folder is cut in memory after the whole folder was read, so memory and time are bounded by the largest folder (measured on the development machine: about 0.1 s for 10,000 and 0.5 s for 50,000 entries in one folder); no 5M-entry test yet. |
| Search unavailable, history still usable, explicit degraded state | Done | `TestSearchAndItsDegradedState` |
| Modified file: both versions restorable | Done | `TestIncrementalBackupKeepsHistoryAndTracksChanges`, `TestBackupHistoryAndRestoreThroughTheAPI` |
| Deleted file stays visible and restorable | Done | `TestDeletedFolderIsRestorableFromOlderPoint`, `TestRestoreFolderAndFileFromOlderPointAfterDeletion` |
| Renamed or moved folder resolves correctly at older points | Partial | Old location resolves at old points; the new name is a separate add (`TestIncrementalBackupKeepsHistoryAndTracksChanges`). |
| Locked file reported clearly | Partial | Skipped with the reason and captured later; no VSS (`TestLockedFileIsSkippedThenCapturedWhenReleased`). |
| Interrupted backup: uncommitted point hidden, safe cleanup | Done | `TestCancelledBackupLeavesNoTraceAndRetrySucceeds`, `TestInterruptedBuildIsRecoveredAtStartup` |
| Repository object missing: preview blocks, never false success | Done | `TestMissingObjectBlocksTheRestoreUnlessPartialIsAllowed`, `TestCorruptObjectIsNeverRestored`, `TestVerifyDetectsMissingAndCorruptObjects` |
| Unauthorized search/restore: no names, previews, downloads or writes | Partial | By decision ([ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)), visitors without an account may see names, sizes and history. Restore previews, restores and every other write need an account, no file contents are ever served, and the access matrix is tested (`TestAnonymousVisitorsBrowseButCannotTouchData`, `TestRolesDecideWhoMayManageMonitoredFolders`, `TestSignInAndGuards`, `TestCSRFAndSecurityHeaders`). There is no per-folder authorization. |
| Restore to original path with preview, confirmation, audit | Not done | By design, restores go to a folder below the restore root, never to the original location. Preview, confirmation and audit are done (`TestRestoreThroughTheForms`). |
| Filesystem edge cases | Partial | Unicode and mixed-case names, junctions, links swapped in during a scan, unreadable folders and very deep paths (backup of paths longer than 260 characters; nothing deeper than 256 folders is recorded) are tested. Not tested: restoring paths longer than 260 characters. Not preserved: ACLs, alternate data streams, hard-link relationships. |
| Journal reset / wrap | Not applicable | There is no USN journal use; every backup walks the whole source, so no change is silently omitted. |

## Known limitations

- Every backup walks the whole source; there is no change journal, so very large sources will be slow.
- A rename or move of a large folder is recorded as delete plus add of every entry (the content bytes are stored once).
- Files are read from the live disk, not from a VSS snapshot ("Live scan" is shown on every point).
- Two roles (`user`, `admin`), no per-folder rights; no TLS inside the service. Anonymous visitors can see names, sizes and history (see [security.md](./security.md)).
- No off-host copy, repository encryption or Windows service wrapper. Without a limit of versions per file, automatic backups only add recovery points; with one, the deleted versions are gone for good and the recovery points that lost them are partial.
- Chunking and pack files are not implemented: every file is one object, so millions of tiny files mean millions of repository files.
- The job log keeps at most 2,000 lines per job, and a restore stores per-item results for at most 20,000 successful items (failures are always stored).
- Sources must be local folders; network shares (UNC paths) are rejected as the folder to read (the display folder is only text people are shown, and is not read). A restore can select at most 500 paths and plan at most 2,000,000 items.
- Every page of a folder list reads the whole folder, so a folder with hundreds of thousands of entries takes seconds to open. The deleted-items panel shows at most 500 items, on the first page only.
- The display folder of a monitored folder is checked for its host, not for whether the share really leads to the folder; hiding the real folders in text works on decoded text and misses unusual spellings (see [security.md](./security.md)).
- A normal backup does not re-check that the repository still holds the content of files it carries over unchanged. An integrity check finds missing or damaged objects, and a **Full re-read** backup writes missing content again.
- If the service is killed in the instant between a backup's commit and the update of its job, the job is reported as failed ("interrupted by a service restart") although the recovery point was committed and is fine.
- Power-loss durability of the repository is best effort and untested (see [security.md](./security.md)).
- Only one service can run against a database; a second one refuses to start.

## Verification performed

This is what was actually run. Everything ran on one Windows machine against a private PostgreSQL 16 instance.

- **Automated checks.** `gofmt`, `go build ./...`, `go vet ./...` and `go test ./...` pass. The tests that need PostgreSQL each use a private schema. The fixes made after the two independent code reviews have regression tests; undoing a fix temporarily made its test fail.
- **End-to-end run of the real program (79 checks).** Sign-in, CSRF and security headers; source rules, including a junction that points into the repository; backups and history; byte-for-byte restores with modification times; conflict policies; tampered and hostile paths; deep verification detecting and clearing corruption; cancelling a running backup; the audit trail; the sign-in throttle.
- **Crash recovery (26 checks).** The service was killed in the middle of a backup of 6,000 files. After the restart the partial rows were gone, the job was marked failed, no partial recovery point was visible, the next backup recorded all 6,000 files, and a second instance refused to start without disturbing a running job.
- **Search.** The Everything adapter was exercised against a real Everything 1.4.1.1024 instance in an isolated configuration.
- **Browser check** (light and dark theme, English and Chinese). Sign-in and sign-out; Explorer, Time Machine, Backups, Status and the restore preview; a restore started from the form and compared with the source (450 files, identical names, content and modification times); **Load more** and **Select all** on a folder of 450 entries; and the job page during a backup of 20,000 files (about 15 seconds), where the counters, phase and state update on their own.

The scripts behind the end-to-end and crash-recovery runs were temporary and are **not** part of the repository, so those two runs cannot be repeated from it.

Not verified: Windows Server 2019, Everything 1.5, restoring paths longer than 260 characters, the 5-million-file scale, power-loss durability, and the **Cancel job** button in a browser (cancellation itself was verified over HTTP). The restore, sign-in and HTTP findings of the second code review that were not fixed are listed in [security.md](./security.md).

**Version 0.1.1.** Verified on the same machine, with the tests and the smoke run using the private PostgreSQL cluster.

- **Automated checks.** `gofmt`, `go build ./...`, `go vet ./...` and `go test ./... -count=1` pass. New tests cover the access matrix for visitors, normal users and administrators, the Redis user directory (a fuzzed decoder; an in-process fake Redis fed with pickle data written by Python), sessions and the sign-in limits, the scheduler and the editing of monitored folders. Two of the new checks were undone on purpose and their tests failed, as they should.
- **Real program.** The compiled server was run against a scratch database with a fake Redis seeded with Python pickles: the default port 8660 and `.env` loading, the migrations, anonymous access, sign-in by employee ID, name and e-mail, the roles, scheduled runs (the first run records a point, an unchanged run records nothing, a changed file records a point), a Redis outage, a deactivated and a demoted account reaching sessions that were already open, and `check-redis-users`.
- **Browser check** of the anonymous header and pages and of the Settings page (English and Chinese, light and dark theme).
- **Independent code review** of the access control and the scheduler. The valid findings were fixed; the others are listed as limits in [security.md](./security.md) and [changelog.md](./changelog.md).

Not verified in 0.1.1: the company's own Redis (run `check-redis-users` on it), migration `0002` on a database that already holds real data (it was applied to fresh databases; it only adds two columns), and a deployment behind a real reverse proxy with HTTPS.

**Version 0.1.3.** Verified on the same machine, with the tests and the real-program run using the private PostgreSQL cluster (port 55432) and a scratch database.

- **Automated checks.** `gofmt`, `go build ./...`, `go vet ./...` and `go test ./... -count=1` pass. New tests cover the list engine (types, filter, sort, paging; 100,000 items sorted by type in about 50 ms), the catalog's read of a whole folder, the live and history listings, the network-path parser, the folder map that hides the real folders (30,000 random texts), the display-folder rules of the service and the configuration, migration `0003` on a version-2 schema, and in `internal/server` the list pages and the API (a folder of 253 entries: sort, filter, pages, the redirect past the end, "select all" meaning the page, a restore of 120 paths), the display-folder rules through the form and the API, and what an administrator, an ordinary account and an anonymous visitor each see on every page and endpoint that can show a folder.
- **Real program.** The compiled server ran against a scratch database with `TIMEVAULT_SERVER_HOST` and `TIMEVAULT_RESTORE_DISPLAY_ROOT` set: the display-folder form (a host typed in another case is stored as the setting spells it), a backup of 10,167 files (about 10 s) and of 60,167 files (about 47 s), the pages of a folder with 10,000 files (about 0.1 s) and with 50,000 files (about 0.5 s, 0.7 s at 500 a page), a restore of 100 selected files whose destination is named by its display folder, and a scan of the pages as an anonymous visitor for the real folders of the server (none found).
- **Browser check** (English and Chinese, dark and light theme): the Type column, the sort links, the type filter, the page size, the page links, the jump buttons (a long jump is instant), the Location line and its Copy button, the Settings form and the folded restore selection.

Not verified in 0.1.3: an ordinary company account in a browser (it needs the Redis user directory; the same rules are tested with a fake directory), the company's own share and host name (display folders were checked as text, not by opening `\\server\share` from another computer), migration `0003` on a database that holds real data (it was applied to scratch databases only; it adds one nullable column), and the search through an Everything that is already running (its HTTP server has to be switched on first; see [runbook.md](./runbook.md#everything-search)).

**Version 0.1.4.** Verified on the same machine, with the tests using the private PostgreSQL cluster (port 55432), and a real-program run against a scratch schema of it, a fake Redis, a fake sign-on service and the Everything that runs on this computer (only read).

- **Automated checks.** `gofmt`, `go build ./...`, `go vet ./...` and `go test ./... -count=1` pass. New tests cover the single sign-on flows and the client address behind trusted proxies, the decoder of the sign-on results, the deletion of old versions (which versions go, the partial marks, the confirmation, the sweep of unreferenced content and its gate against running backups), the pacing, the order and the probe of the scheduler on a fake clock, the Everything adapter (folder listing and probe) against a fake HTTP server, the folder sizes of the Explorer, the audit durations, and the Settings form and the API for the version limit.
- **Real program.** The compiled server ran with single sign-on turned on: a sign-in that succeeded (as an administrator, with the audit record), one for an account that is not active, one for a browser without a Windows user, and one whose address did not match. A folder with a limit of 2 versions: backups, the deletion of the older version and of its content, the partial mark, a verification of a partial point (objects checked, manifest skipped), lowering the limit to 1 (refused without confirmation with a count, then accepted, with a cleanup job that ends at "Done"), the audit **Duration** column with the speed of a first backup of 30.8 MB, and folder sizes in the Explorer read from the real Everything. Three folders on a one-minute schedule: the first two backups started 45 seconds apart; with the probe on, the scheduler skipped the unchanged folders every minute and scanned the one in which a file was changed. The Everything adapter was also run against the real Everything (listing of the folders, the number of entries, "changed since", a folder that is not indexed).
- **Browser check** (English and Chinese, dark theme): the login page with the single sign-on button, its messages, the Audit table with the Duration column, the Explorer with folder sizes, the Settings form and the Partial marks.
- **Independent code review** of the version cleanup, the single sign-on flow and the scheduler. It found no loss of data in the deletion and no hole in the sign-on, and reported four problems. All four were fixed and have tests: a cleanup that was skipped or failed after a backup was never retried (it is now queued as a job); a cleanup that waited for a restore held a worker and the folder, so a backup of the folder failed (the job queue now lets the jobs of a folder take turns, and a waiting job holds no worker); the Everything probe ignored a change of the exclude patterns and asked a stopped Everything once for every due folder (the last scan is now forgotten, and the probe is bounded and backs off for a minute); and the page that a single sign-in returns to was not limited in size (it is now 2,048 bytes).
- **After the review fixes** the compiled server ran again on a scratch schema: four backups of a changing file with a limit of 2 versions left two versions and two partial recovery points, lowering the limit to 1 was refused without confirmation (409) and accepted with it, the cleanup job ended at "Succeeded", and the next backup ran without waiting.

Not verified in 0.1.4: the company's own single sign-on service and Redis (the sign-on results are decoded from data written by a test helper that follows the format of the dash application, not from a result the real service wrote), single sign-on through a real reverse proxy (the handling of `X-Forwarded-For` is tested with constructed requests), the pop-up blocker of real browsers (the message for a blocked window was not seen), migration `0004` on a database that holds real data (it was applied to scratch schemas only), the retention and the scheduler on a server with ten real folders and a repository of millions of objects (only small scratch folders were used, so the time of a cleanup or a sweep at that size is unknown), and the probe over the company's own volumes.

**Version 0.1.5.** Found and verified on the same machine, after a report that the application, put behind the company's nginx at `/tv/`, could not be opened. The tests use the private PostgreSQL cluster (port 55432); the real-program run used a scratch schema of it and a second, separate nginx instance (1.24, with its own prefix and port; the nginx that serves this computer was not touched).

- **The fault was reproduced first.** With the `/tv/` block of the company's configuration and version 0.1.4, `/tv/` was answered with `303 Location: /backups` (`/explorer` when there is a monitored folder), the browser followed it to `http://<host>/backups`, outside `/tv/`, and nginx sent it on to the portal (`return 307 /api/`). The TimeVault log showed one request.
- **Automated checks.** `gofmt`, `go build ./...`, `go vet ./...` and `go test ./... -count=1` pass. New tests cover the normalisation and refusal of the setting, the path of the session cookie, the handler that takes the prefix off (encoded paths, the bare prefix, similar names such as `/tvx`), the prefix on `Location` headers (the redirects of the `ServeMux` too) and on cookies, the next and back addresses (including `/tv//host`), every page, the API and the single sign-on under the prefix with the proxy keeping and removing it, and two scans that fail when a template writes an address that ignores the prefix or code sets a cookie for the whole host. The new tests were also run against code that was broken on purpose, and failed as they should.
- **Real program and browser.** The compiled server ran behind nginx with the company's `/tv/` block as it was, and then with the recommended block. The redirect chain stayed under `/tv/`. A browser opened `/tv/`, followed a folder link without a reload, signed in (the page it came from was kept), started a backup with a form and watched the job finish (the polling of `/tv/api/v1/jobs/<id>`), switched to Chinese, opened the Settings page (**Address prefix** `/tv`) and signed out. Every address in the pages was under `/tv/`, the cookies had `Path=/tv`, and the service also answered on its own port with and without the prefix. With the company's block a forged `X-Forwarded-For` was recorded as the address of the client in the audit log; with `X-Forwarded-For $remote_addr` it was not.

Not verified in 0.1.5: the company's own nginx on the real server (the checks used a separate instance with the same block, except for `$http_host` in place of `$host` because it listens on another port than 80), single sign-on through it (the sign-on service and Redis were not available; the flow is tested with a fake directory), other people's browsers, and a deployment with a second proxy in front of the first.

1. Run the P0 spikes that were skipped: VSS create/read/cleanup on a test volume and USN checkpoint reading, then record ADRs.
2. Build a synthetic 5M-entry catalog and measure listing, history browse, backup walk time and PostgreSQL size.
3. Add a repository backup (an off-host copy) and consider retention by age (the version limit per file exists since 0.1.4).
4. Add per-folder rights (path allow-lists).
5. Decide the open questions in plan.md §10 (volumes, change rate, RPO/RTO, repository location, ACL requirements).

---

# 中文

## 概述

这是 TimeVault 相对于 [plan.md](./plan.md) 的真实状态。它是一个**垂直切片**：在本地 NTFS 文件夹上，打通"搜索/浏览、备份、历史、恢复"这一条完整路径。它不是生产级备份系统，这里的任何内容都没有在 500 万文件规模上运行过。0.1.1 版增加了开放浏览、带两种角色的公司账号登录、管理监控文件夹的设置页和自动备份；0.1.3 版增加了可排序、可筛选、可分页的文件夹列表（带类型列）、监控文件夹的显示文件夹，以及只有管理员才能看到服务器真实文件夹的规则；0.1.4 版增加了单点登录、来自 Everything 的文件夹大小、审计日志中每个操作的耗时、每个文件的版本数上限及滚动删除，以及错开时间的自动备份；0.1.5 版让站点可以放在反向代理之后的 `/tv` 等路径前缀下提供服务。下文的表述已按它们更新，[changelog.md](./changelog.md) 列出了具体改动。

状态用语：**已完成**（已实现且有测试）、**部分完成**（可用但有所述限制）、**未完成**（有计划但未实现）、**未验证**（暂无证据）。

## 对照 plan.md §3 的范围

| 计划项 | 状态 | 说明 |
|---|---|---|
| 多根备份源登记、排除规则、备份源与仓库健康状态 | 已完成 | 管理员在设置页，或通过 `POST` 和 `PATCH /api/v1/sources`（0.1.1）添加和编辑监控文件夹；排除通配规则；状态页显示仓库剩余空间和写入测试。 |
| 当前目录树、面包屑、搜索、排序、分页 | 已完成 | 从磁盘读取实时文件夹并标注已保护 / 已变更 / 新增；Everything 搜索。自 0.1.3 起，列表有类型列，可以按名称、类型、大小或修改时间对整个文件夹排序，按类型筛选，并按每页 100 项（50 到 500）分页，还有直达顶部和底部的按钮。自 0.1.4 起，文件夹的大小来自 Everything 的索引（时间机器页面不显示）。 |
| 带时间轴和恢复点选择器的历史浏览 | 已完成 | 时间机器页面；只显示已提交的恢复点。 |
| 文件版本面板 | 已完成 | 采集时间、有效期、大小、修改时间、SHA-256、内容是否可用。"可选的变更原因"未实现。 |
| 检测新增 / 修改 / 重命名 / 移动 / 删除，墓碑记录 | 部分完成 | 新增、修改和删除都会跟踪；重命名和移动被记录为"删除 + 新增"。被删除的路径在较早的恢复点仍然可见。 |
| 首次全量与增量备份、进度、重试、校验 | 部分完成 | 通过遍历备份源并比较大小和修改时间实现全量和增量；有进度、取消、完整性校验（浅层和深度）。自 0.1.1 起，监控文件夹可以设置自动计划（1 到 43200 分钟）；没有发现变化的一次运行不会留下恢复点。失败的运行在下一个间隔之前不会重试。自 0.1.4 起，多个文件夹的自动备份会错开时间（一次一个，带间隔和休息，按需要程度排序），并且在开启后会跳过 Everything 说没有变化的文件夹。备份仍可手动或通过 API 启动。 |
| 单文件与文件夹恢复、导出、dry-run、冲突策略 | 部分完成 | 文件和文件夹恢复，含 dry-run、跳过 / 改名 / 覆盖、哈希校验、逐项结果。恢复**只能写入恢复根目录下的文件夹**；原位置恢复以及文件下载/导出未实现。 |
| 认证、角色、路径白名单、审计、管理设置、健康状态 | 部分完成 | 自 0.1.1 起：所有人可以浏览；公司账号来自 Redis 用户目录，有两种角色（`user`、`admin`），另有可选的本地管理员；签名会话每分钟重新核实一次、CSRF、登录限制、审计记录、状态页和设置页。自 0.1.4 起还有通过公司单点登录服务的单点登录，以及用于客户端地址的受信任反向代理。自 0.1.5 起，站点可以放在反向代理之后的 `/tv` 等路径前缀下提供服务（`TIMEVAULT_BASE_PATH`）。没有按文件夹或按路径的权限。 |
| 保留策略与安全垃圾回收 | 部分完成 | 自 0.1.4 起，每个监控文件夹可以有每个文件的版本数上限：较旧的版本会被删除，失去版本的恢复点会被标记为不完整，没有备份运行时会清扫无人引用的仓库内容（[ADR 0010](./adr/0010-version-limit-and-garbage-collection.md)）。默认保留每个版本。未完成：按时间或按恢复点数量的保留策略、仓库自身的备份和异机副本。 |

## 各阶段

| 阶段 | 状态 |
|---|---|
| P0 技术验证 | 部分完成。Everything 适配器已针对真实的 Everything 1.4.1.1024 实例验证。VSS 和 USN **没有**做原型。没有运行 500 万文件基准测试。 |
| P1 只读浏览器与服务基础 | MVP 范围内已完成（HTTP 服务、迁移、日志、健康检查、模板、登录、搜索适配器测试）。未完成：Windows 服务注册、500 万合成数据负载测试。 |
| P2 备份与历史引擎 | 部分完成。未实现 VSS 一致性和 USN 增量检测；中断恢复已针对崩溃、取消和重启做过测试。自 0.1.1 起有自动计划，自 0.1.4 起错开时间。 |
| P3 恢复 | 部分完成。见上面的恢复一行；未实现原位置恢复。 |
| P4 生产加固 | 未完成。 |

## 验收矩阵（plan.md §9）

| 场景 | 状态 | 证据 |
|---|---|---|
| 500 万目录条目，内存有界，服务端分页 | 未验证 | 自 0.1.3 起，文件夹的一页是在读取整个文件夹之后于内存中切分的，所以内存和耗时受最大文件夹限制（在开发机器上测得：同一个文件夹中 10,000 个条目约 0.1 秒，50,000 个约 0.5 秒）；尚无 500 万条目的测试。 |
| 搜索不可用时历史仍可用，并明确显示降级 | 已完成 | `TestSearchAndItsDegradedState` |
| 修改过的文件：两个版本都可恢复 | 已完成 | `TestIncrementalBackupKeepsHistoryAndTracksChanges`、`TestBackupHistoryAndRestoreThroughTheAPI` |
| 被删除的文件仍然可见并可恢复 | 已完成 | `TestDeletedFolderIsRestorableFromOlderPoint`、`TestRestoreFolderAndFileFromOlderPointAfterDeletion` |
| 重命名或移动的文件夹在较早恢复点能正确解析 | 部分完成 | 旧位置在旧恢复点能解析；新名称是单独的新增（`TestIncrementalBackupKeepsHistoryAndTracksChanges`）。 |
| 被锁定的文件有清晰的报告 | 部分完成 | 跳过并写明原因，之后会被采集；没有 VSS（`TestLockedFileIsSkippedThenCapturedWhenReleased`）。 |
| 备份被中断：未提交的恢复点被隐藏，安全清理 | 已完成 | `TestCancelledBackupLeavesNoTraceAndRetrySucceeds`、`TestInterruptedBuildIsRecoveredAtStartup` |
| 仓库对象缺失：预览阻止，绝不假成功 | 已完成 | `TestMissingObjectBlocksTheRestoreUnlessPartialIsAllowed`、`TestCorruptObjectIsNeverRestored`、`TestVerifyDetectsMissingAndCorruptObjects` |
| 未授权的搜索/恢复：不泄露名称、预览、下载或写入 | 部分完成 | 按决定（[ADR 0005](./adr/0005-redis-user-directory-and-open-browsing.md)），没有账号的访问者可以看到名称、大小和历史。恢复预览、恢复以及其他所有写操作都需要账号，不会提供任何文件内容，访问矩阵有测试（`TestAnonymousVisitorsBrowseButCannotTouchData`、`TestRolesDecideWhoMayManageMonitoredFolders`、`TestSignInAndGuards`、`TestCSRFAndSecurityHeaders`）。没有按文件夹的授权。 |
| 恢复到原路径，需预览、确认和审计 | 未完成 | 设计上恢复只写入恢复根目录下的文件夹，绝不写入原位置。预览、确认和审计已完成（`TestRestoreThroughTheForms`）。 |
| 文件系统边界情况 | 部分完成 | 已测试 Unicode 和大小写混合的名称、联接点、扫描期间被替换进来的链接、不可读文件夹，以及很深的路径（备份长度超过 260 字符的路径；不会记录深于 256 层文件夹的内容）。未测试：恢复长度超过 260 字符的路径。未保留：ACL、备用数据流、硬链接关系。 |
| Journal 重置 / 回卷 | 不适用 | 没有使用 USN journal；每次备份都遍历整个备份源，不会静默漏掉任何变化。 |

## 已知限制

- 每次备份都会遍历整个备份源；没有变更日志，因此非常大的备份源会比较慢。
- 大文件夹的重命名或移动会被记录为每一项的"删除 + 新增"（内容字节只存一份）。
- 文件读取自在线磁盘，而不是 VSS 快照（每个恢复点都会显示"实时扫描"）。
- 两种角色（`user`、`admin`），没有按文件夹的权限；服务内没有 TLS。匿名访问者可以看到名称、大小和历史（见 [security.md](./security.md)）。
- 没有异机副本、仓库加密或 Windows 服务封装。没有每个文件的版本数上限时，自动备份只会不断增加恢复点；有上限时，被删除的版本永远消失，失去它们的恢复点是不完整的。
- 没有实现分块和 Pack 文件：每个文件就是一个对象，所以数百万个小文件意味着仓库中有数百万个文件。
- 每个任务的日志最多保留 2,000 行；恢复最多存储 20,000 个成功项目的逐项结果（失败项始终存储）。
- 备份源必须是本地文件夹；网络共享（UNC 路径）作为要读取的文件夹会被拒绝（显示文件夹只是展示给人们看的文本，不会被读取）。一次恢复最多可选择 500 个路径，最多规划 2,000,000 个条目。
- 文件夹列表的每一页都会读取整个文件夹，所以包含几十万个条目的文件夹需要几秒钟才能打开。已删除项目面板最多显示 500 项，并且只在第一页显示。
- 监控文件夹的显示文件夹只检查主机，不检查共享是否真的通向该文件夹；在文本中隐藏真实文件夹是基于已解码文本的，会漏掉不寻常的写法（见 [security.md](./security.md)）。
- 普通备份不会重新检查仓库中是否仍然保存着它原样沿用的未变化文件的内容。完整性校验可以发现缺失或损坏的对象，而**完整重读**备份会把缺失的内容重新写入。
- 如果服务恰好在备份提交与其任务更新之间的瞬间被强制终止，任务会被报告为失败（"interrupted by a service restart"），尽管恢复点已经提交并且完好。
- 仓库在断电后的持久性是尽力而为，且未经测试（见 [security.md](./security.md)）。
- 同一个数据库只能运行一个服务；第二个服务会拒绝启动。

## 已执行的验证

以下是实际运行过的验证。所有验证都在一台 Windows 机器上、针对一个私有的 PostgreSQL 16 实例进行。

- **自动化检查。** `gofmt`、`go build ./...`、`go vet ./...` 和 `go test ./...` 均通过。需要 PostgreSQL 的测试各自使用一个私有 schema。两轮独立代码审查之后做的修复都有回归测试；临时撤销某项修复后，对应的测试会失败。
- **对真实程序的端到端运行（79 项检查）。** 登录、CSRF 与安全响应头；备份源规则（包括指向仓库的联接点）；备份与历史；逐字节一致且保留修改时间的恢复；冲突策略；被篡改的和恶意构造的路径；深度校验发现并清除损坏；取消运行中的备份；审计记录；登录限流。
- **崩溃恢复（26 项检查）。** 在备份 6,000 个文件的过程中强制终止服务。重启后，未完成的记录被清除，任务被标记为失败，没有任何不完整的恢复点可见，下一次备份记录了全部 6,000 个文件，并且第二个实例拒绝启动，不影响正在运行的任务。
- **搜索。** Everything 适配器已针对真实的 Everything 1.4.1.1024 实例（隔离配置）运行过。
- **浏览器检查**（浅色和深色主题，英文和中文）。登录与退出；文件浏览、时间机器、备份、状态和恢复预览页面；通过表单发起的恢复，并与源文件对比（450 个文件，名称、内容和修改时间完全一致）；在包含 450 项的文件夹上使用**加载更多**和**全选**；以及备份 20,000 个文件（约 15 秒）期间的任务页面，其计数器、阶段和状态会自动更新。

端到端运行和崩溃恢复运行所用的脚本是临时的，**不在**仓库中，所以无法用仓库重复这两次运行。

未验证：Windows Server 2019、Everything 1.5、恢复长度超过 260 字符的路径、500 万文件规模、断电持久性，以及在浏览器中点击**取消任务**按钮（取消功能本身已通过 HTTP 接口验证）。第二轮代码审查中关于恢复、登录和 HTTP 的、未修复的发现列在 [security.md](./security.md) 中。

**0.1.1 版。** 在同一台机器上验证，测试和冒烟运行使用的是私有 PostgreSQL 集群。

- **自动化检查。** `gofmt`、`go build ./...`、`go vet ./...` 和 `go test ./... -count=1` 均通过。新增的测试覆盖：访问者、普通用户和管理员的访问矩阵，Redis 用户目录（经过模糊测试的解码器；用 Python 生成的 pickle 数据喂给进程内的模拟 Redis），会话和登录限制，调度器，以及监控文件夹的编辑。其中两项新的检查被有意撤销，对应的测试按预期失败了。
- **真实程序。** 编译后的服务针对一个临时数据库运行，并配合一个灌入 Python pickle 数据的模拟 Redis：默认端口 8660 和 `.env` 加载、迁移、匿名访问、用工号、姓名和邮箱登录、角色、计划运行（第一次运行记录一个恢复点，没有变化的运行什么也不记录，文件变化后记录一个恢复点）、Redis 故障、被停用和被降级的账号对已打开会话的影响，以及 `check-redis-users`。
- **浏览器检查**：匿名状态下的页头和页面，以及设置页（英文和中文，浅色和深色主题）。
- **独立代码审查**，针对访问控制和调度器。有效的发现都已修复；其余的作为限制列在 [security.md](./security.md) 和 [changelog.md](./changelog.md) 中。

0.1.1 中未验证：公司自己的 Redis（请对它运行 `check-redis-users`）、在已有真实数据的数据库上应用迁移 `0002`（它只在全新的数据库上应用过；它只增加两列），以及放在带 HTTPS 的真实反向代理之后的部署。

**0.1.3 版。** 在同一台机器上验证，测试和真实程序的运行使用的是私有 PostgreSQL 集群（端口 55432）和一个临时数据库。

- **自动化检查。** `gofmt`、`go build ./...`、`go vet ./...` 和 `go test ./... -count=1` 通过。新增的测试覆盖：列表引擎（类型、筛选、排序、分页；100,000 个项目按类型排序约 50 毫秒）、目录库对整个文件夹的读取、实时列表和历史列表、网络路径解析器、隐藏真实文件夹的文件夹映射（30,000 个随机文本）、服务和配置中的显示文件夹规则、在版本 2 的结构上应用迁移 `0003`；以及 `internal/server` 中的列表页面和 API（一个有 253 个条目的文件夹：排序、筛选、分页、超出末尾时的重定向、"全选"只表示当前页、恢复 120 个路径）、通过表单和 API 的显示文件夹规则，以及管理员、普通账号和匿名访问者在每个可能显示文件夹的页面和接口上各自看到什么。
- **真实程序。** 编译好的服务连接临时数据库运行，并设置了 `TIMEVAULT_SERVER_HOST` 和 `TIMEVAULT_RESTORE_DISPLAY_ROOT`：显示文件夹表单（以另一种大小写输入的主机会按设置中的写法保存）、10,167 个文件的备份（约 10 秒）和 60,167 个文件的备份（约 47 秒）、包含 10,000 个文件的文件夹（约 0.1 秒）和包含 50,000 个文件的文件夹（约 0.5 秒，每页 500 项时 0.7 秒）的页面、选择 100 个文件的恢复（目标位置以显示文件夹称呼），以及以匿名访问者身份扫描各页面，查找服务器的真实文件夹（没有发现）。
- **浏览器检查**（英文和中文，深色和浅色主题）：类型列、排序链接、类型筛选、每页数量、页码链接、直达按钮（长距离跳转是瞬间完成的）、位置行及其复制按钮、设置表单，以及折叠起来的恢复选择。

0.1.3 中未验证：浏览器中的普通公司账号（需要 Redis 用户目录；相同的规则用模拟目录测试过）、公司自己的共享和主机名（显示文件夹只作为文本检查过，没有从另一台计算机上打开 `\\server\share`）、在已有真实数据的数据库上应用迁移 `0003`（只在临时数据库上应用过；它只增加一个可为空的列），以及通过你已经在运行的 Everything 进行搜索（需要先打开它的 HTTP 服务；见 [runbook.md](./runbook.md#everything-搜索)）。

**0.1.4 版。** 在同一台机器上验证，测试使用私有 PostgreSQL 集群（端口 55432），真实程序的运行使用它的一个临时 schema、一个模拟 Redis、一个模拟单点登录服务，以及这台电脑上运行的 Everything（只读）。

- **自动化检查。** `gofmt`、`go build ./...`、`go vet ./...` 和 `go test ./... -count=1` 通过。新增的测试覆盖：单点登录流程和受信任代理之后的客户端地址、单点登录结果的解码器、旧版本的删除（哪些版本会被删除、不完整标记、确认、对无人引用内容的清扫及其针对运行中备份的闸门）、调度器在模拟时钟上的间隔控制、顺序和探测、针对模拟 HTTP 服务的 Everything 适配器（文件夹列表和探测）、文件浏览中的文件夹大小、审计耗时，以及设置表单和 API 中的版本数上限。
- **真实程序。** 编译好的服务在开启单点登录的情况下运行：一次成功的登录（以管理员身份，含审计记录）、一次账号无效的登录、一次浏览器没有 Windows 用户的登录，以及一次地址不匹配的登录。一个上限为 2 个版本的文件夹：备份、较旧版本及其内容的删除、不完整标记、对不完整恢复点的校验（检查对象，跳过清单）、把上限调到 1（没有确认时被拒绝并给出数量，然后被接受，并有一个以"完成"结束的清理任务）、审计的**耗时**列以及 30.8 MB 首次备份的速度，以及从真实 Everything 读取的文件浏览中的文件夹大小。三个设为每分钟一次的文件夹：最先的两个备份相隔 45 秒启动；开启探测后，调度器每分钟跳过没有变化的文件夹，并扫描了其中改动过文件的那一个。Everything 适配器也针对真实的 Everything 运行过（文件夹列表、条目数量、"自某时起是否有变化"、未被索引的文件夹）。
- **浏览器检查**（英文和中文，深色主题）：带单点登录按钮的登录页及其提示、带耗时列的审计表、带文件夹大小的文件浏览、设置表单和不完整标记。
- **独立代码审查**，针对版本清理、单点登录流程和调度器。它没有发现删除中的数据丢失，也没有发现单点登录中的漏洞，报告了四个问题，全部已修复并带有测试：备份之后被跳过或失败的清理从不会重试（现在它会作为任务排入队列）；等待恢复的清理占用了一个 worker 和该文件夹，导致该文件夹的备份失败（现在任务队列让同一文件夹的任务轮流执行，等待的任务不占用 worker）；Everything 探测忽略了排除规则的更改，并且对每个到期的文件夹都询问一次已停止的 Everything（现在会忘掉上次的扫描记录，探测有次数限制，并且一分钟内不再询问）；以及单点登录返回的页面地址没有大小限制（现在限制为 2,048 字节）。
- **修复审查发现之后**，编译好的服务在一个临时 schema 上再次运行：对一个不断变化的文件做四次备份、上限为 2 个版本，留下两个版本和两个不完整的恢复点；把上限调到 1，没有确认时被拒绝（409），有确认时被接受；清理任务以"成功"结束，下一次备份无需等待就运行了。

0.1.4 中未验证：公司自己的单点登录服务和 Redis（单点登录结果是用一个遵循 dash 应用格式的测试辅助程序写出的数据解码的，而不是真实服务写出的结果）、通过真实反向代理的单点登录（对 `X-Forwarded-For` 的处理用构造的请求测试过）、真实浏览器的弹窗拦截（没有看到"窗口被拦截"的提示）、在已有真实数据的数据库上应用迁移 `0004`（只在临时 schema 上应用过）、在有十个真实文件夹和数百万对象的仓库的服务器上运行保留策略和调度器（只用过小的临时文件夹，所以在那种规模上清理或清扫要多久不得而知），以及在公司自己的卷上使用探测。

**0.1.5 版。** 在同一台机器上发现并验证，起因是有人反馈：放在公司 nginx 之后的 `/tv/` 下，应用打不开。测试使用私有 PostgreSQL 集群（端口 55432）；真实程序的运行使用它的一个临时 schema，以及另一个独立的 nginx 实例（1.24，有自己的前缀和端口；为这台电脑提供服务的 nginx 没有被动过）。

- **先复现了故障。** 用公司配置中的 `/tv/` 配置块和 0.1.4 版本，`/tv/` 得到的回答是 `303 Location: /backups`（有监控文件夹时是 `/explorer`），浏览器跟着跳到 `http://<主机>/backups`，已经在 `/tv/` 之外，nginx 又把它送到了门户页（`return 307 /api/`）。TimeVault 的日志里只有一次请求。
- **自动化检查。** `gofmt`、`go build ./...`、`go vet ./...` 和 `go test ./... -count=1` 通过。新增的测试覆盖：该设置项的规范化和拒绝、会话 Cookie 的路径、去掉前缀的处理器（编码后的路径、只有前缀本身、`/tvx` 这样的相似名称）、`Location` 头上的前缀（包括 `ServeMux` 的重定向）和 Cookie 上的前缀、next 和 back 地址（包括 `/tv//host`）、前缀之下的每个页面、API 和单点登录（代理保留和去掉前缀两种情况），以及两个扫描：模板写了忽略前缀的地址，或代码设置了作用于整个主机的 Cookie 时会失败。新增的测试也在被有意改坏的代码上运行过，并且按预期失败了。
- **真实程序与浏览器。** 编译好的服务在 nginx 之后运行，先用公司的 `/tv/` 配置块原样，再用推荐的配置块。重定向链始终在 `/tv/` 之下。浏览器打开 `/tv/`，不刷新地跟随文件夹链接，登录（保留了来源页面），用表单启动备份并看着任务完成（轮询 `/tv/api/v1/jobs/<id>`），切换为中文，打开设置页（**地址前缀** `/tv`），然后退出登录。页面中的每个地址都在 `/tv/` 之下，Cookie 的 `Path=/tv`，服务在自己的端口上带前缀和不带前缀也都能回答。用公司的配置块时，伪造的 `X-Forwarded-For` 会被当作客户端的地址记入审计日志；用 `X-Forwarded-For $remote_addr` 则不会。

0.1.5 中未验证：真实服务器上公司自己的 nginx（检查使用的是同一配置块的独立实例，只是因为它监听的端口不是 80，把 `$host` 换成了 `$http_host`）、通过它的单点登录（单点登录服务和 Redis 当时不可用；该流程用模拟目录测试过）、其他人的浏览器，以及第一层代理前面还有第二层代理的部署。

## 建议的下一步

1. 补做被跳过的 P0 验证：在测试卷上验证 VSS 的创建/读取/清理以及 USN 检查点读取，然后记录 ADR。
2. 构建一个 500 万条目的合成目录库，测量列表、历史浏览、备份遍历耗时和 PostgreSQL 体积。
3. 增加仓库自身的备份（异机副本），并考虑按时间的保留策略（每个文件的版本数上限自 0.1.4 起已有）。
4. 增加按文件夹的权限（路径白名单）。
5. 确定 plan.md §10 中尚未决定的问题（卷、变化量、RPO/RTO、仓库位置、ACL 要求）。
