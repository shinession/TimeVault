# English

## Overview

This is the honest status of TimeVault against [plan.md](./plan.md). It is a **vertical slice**: one working path through search/browse, backup, history and restore on a local NTFS folder. It is not a production backup system, and nothing here has been run at the 5-million-file scale. Version 0.1.1 added open browsing, company sign-in with two roles, a Settings page for the monitored folders and automatic backups; version 0.1.3 added sorted, filtered and paged folder lists with a Type column, display folders for the monitored folders, and the rule that only administrators see the server's real folders. The statements below are updated for them, and [changelog.md](./changelog.md) lists what changed.

Status words: **Done** (implemented and tested), **Partial** (works with stated limits), **Not done** (planned, not implemented), **Not verified** (no evidence yet).

## Scope against plan.md §3

| Plan item | Status | Notes |
|---|---|---|
| Multi-root source registration, exclusions, source and repository health | Done | Administrators add and edit monitored folders on the Settings page or with `POST` and `PATCH /api/v1/sources` (0.1.1); exclusion globs; repository free space and write test on the status page. |
| Current folder tree, breadcrumbs, search, sort, pagination | Done | Live folder from disk with protected / changed / new markers; Everything-backed search. Since 0.1.3 the lists have a Type column, sort the whole folder by name, type, size or modified time, filter by type and come in pages of 100 (50 to 500), with buttons that jump to the top and the bottom. |
| Historical browse with timeline and recovery-point selector | Done | Time Machine page; only committed points appear. |
| File version panel | Done | Capture time, validity, size, modification time, SHA-256, content availability. "Optional change reason" is not implemented. |
| Detect new / changed / renamed / moved / deleted, tombstones | Partial | New, changed and deleted are tracked; rename and move are recorded as delete plus add. Deleted paths stay visible at older points. |
| Initial full backup and incremental backups, progress, retries, verification | Partial | Full and incremental by walking the source and comparing size and modification time; progress, cancel, integrity verification (shallow and deep). Since 0.1.1 a monitored folder can have an automatic schedule (1 to 43200 minutes); a run that finds nothing changed leaves no recovery point. A failed run is not retried before the next interval. Backups can still be started by hand or through the API. |
| Single file and folder restore, export, dry-run, conflict policies | Partial | File and folder restore with dry-run, skip / rename / overwrite, hash verification, per-item results. Restore is **only into a folder below the restore root**; restoring in place and downloading/exporting a file are not implemented. |
| Authentication, roles, path allow-lists, audit, admin settings, health | Partial | Since 0.1.1: open browsing for everyone; company accounts from the Redis user directory with two roles (`user`, `admin`) and an optional local administrator; signed sessions that are re-checked every minute, CSRF, sign-in limits, audit trail, status page and Settings page. No per-folder or per-path rights, no SSO. |
| Retention and safe garbage collection | Not done | The repository only grows; nothing is ever deleted. |

## Phases

| Phase | Status |
|---|---|
| P0 technical proof | Partial. The Everything adapter was verified against a real Everything 1.4.1.1024 instance. VSS and USN were **not** prototyped. No 5M-file benchmark was run. |
| P1 read-only explorer and service foundation | Done for the MVP scope (HTTP server, migrations, logging, health, templates, sign-in, search adapter tests). Not done: Windows service registration, 5M synthetic load test. |
| P2 backup and history engine | Partial. VSS consistency and USN incremental detection are not implemented; interruption recovery is tested for crash, cancel and restart. Automatic schedules exist since 0.1.1. |
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
- Two roles (`user`, `admin`), no per-folder rights, no SSO; no TLS inside the service. Anonymous visitors can see names, sizes and history (see [security.md](./security.md)).
- No retention, garbage collection, off-host copy, repository encryption or Windows service wrapper. Automatic backups exist, but without retention they only add recovery points.
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

## Suggested next steps

1. Run the P0 spikes that were skipped: VSS create/read/cleanup on a test volume and USN checkpoint reading, then record ADRs.
2. Build a synthetic 5M-entry catalog and measure listing, history browse, backup walk time and PostgreSQL size.
3. Add retention with reference-checked garbage collection and repository backup (the scheduler exists since 0.1.1, and without retention it only adds recovery points).
4. Add per-folder rights (path allow-lists), trust a reverse proxy for client addresses, then Windows SSO.
5. Decide the open questions in plan.md §10 (volumes, change rate, RPO/RTO, repository location, ACL requirements).

---

# 中文

## 概述

这是 TimeVault 相对于 [plan.md](./plan.md) 的真实状态。它是一个**垂直切片**：在本地 NTFS 文件夹上，打通"搜索/浏览、备份、历史、恢复"这一条完整路径。它不是生产级备份系统，这里的任何内容都没有在 500 万文件规模上运行过。0.1.1 版增加了开放浏览、带两种角色的公司账号登录、管理监控文件夹的设置页和自动备份；0.1.3 版增加了可排序、可筛选、可分页的文件夹列表（带类型列）、监控文件夹的显示文件夹，以及只有管理员才能看到服务器真实文件夹的规则。下文的表述已按它们更新，[changelog.md](./changelog.md) 列出了具体改动。

状态用语：**已完成**（已实现且有测试）、**部分完成**（可用但有所述限制）、**未完成**（有计划但未实现）、**未验证**（暂无证据）。

## 对照 plan.md §3 的范围

| 计划项 | 状态 | 说明 |
|---|---|---|
| 多根备份源登记、排除规则、备份源与仓库健康状态 | 已完成 | 管理员在设置页，或通过 `POST` 和 `PATCH /api/v1/sources`（0.1.1）添加和编辑监控文件夹；排除通配规则；状态页显示仓库剩余空间和写入测试。 |
| 当前目录树、面包屑、搜索、排序、分页 | 已完成 | 从磁盘读取实时文件夹并标注已保护 / 已变更 / 新增；Everything 搜索。自 0.1.3 起，列表有类型列，可以按名称、类型、大小或修改时间对整个文件夹排序，按类型筛选，并按每页 100 项（50 到 500）分页，还有直达顶部和底部的按钮。 |
| 带时间轴和恢复点选择器的历史浏览 | 已完成 | 时间机器页面；只显示已提交的恢复点。 |
| 文件版本面板 | 已完成 | 采集时间、有效期、大小、修改时间、SHA-256、内容是否可用。"可选的变更原因"未实现。 |
| 检测新增 / 修改 / 重命名 / 移动 / 删除，墓碑记录 | 部分完成 | 新增、修改和删除都会跟踪；重命名和移动被记录为"删除 + 新增"。被删除的路径在较早的恢复点仍然可见。 |
| 首次全量与增量备份、进度、重试、校验 | 部分完成 | 通过遍历备份源并比较大小和修改时间实现全量和增量；有进度、取消、完整性校验（浅层和深度）。自 0.1.1 起，监控文件夹可以设置自动计划（1 到 43200 分钟）；没有发现变化的一次运行不会留下恢复点。失败的运行在下一个间隔之前不会重试。备份仍可手动或通过 API 启动。 |
| 单文件与文件夹恢复、导出、dry-run、冲突策略 | 部分完成 | 文件和文件夹恢复，含 dry-run、跳过 / 改名 / 覆盖、哈希校验、逐项结果。恢复**只能写入恢复根目录下的文件夹**；原位置恢复以及文件下载/导出未实现。 |
| 认证、角色、路径白名单、审计、管理设置、健康状态 | 部分完成 | 自 0.1.1 起：所有人可以浏览；公司账号来自 Redis 用户目录，有两种角色（`user`、`admin`），另有可选的本地管理员；签名会话每分钟重新核实一次、CSRF、登录限制、审计记录、状态页和设置页。没有按文件夹或按路径的权限，没有 SSO。 |
| 保留策略与安全垃圾回收 | 未完成 | 仓库只会增长；不会删除任何内容。 |

## 各阶段

| 阶段 | 状态 |
|---|---|
| P0 技术验证 | 部分完成。Everything 适配器已针对真实的 Everything 1.4.1.1024 实例验证。VSS 和 USN **没有**做原型。没有运行 500 万文件基准测试。 |
| P1 只读浏览器与服务基础 | MVP 范围内已完成（HTTP 服务、迁移、日志、健康检查、模板、登录、搜索适配器测试）。未完成：Windows 服务注册、500 万合成数据负载测试。 |
| P2 备份与历史引擎 | 部分完成。未实现 VSS 一致性和 USN 增量检测；中断恢复已针对崩溃、取消和重启做过测试。自 0.1.1 起有自动计划。 |
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
- 两种角色（`user`、`admin`），没有按文件夹的权限，没有 SSO；服务内没有 TLS。匿名访问者可以看到名称、大小和历史（见 [security.md](./security.md)）。
- 没有保留策略、垃圾回收、异机副本、仓库加密或 Windows 服务封装。自动备份已有，但没有保留策略时它只会不断增加恢复点。
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

## 建议的下一步

1. 补做被跳过的 P0 验证：在测试卷上验证 VSS 的创建/读取/清理以及 USN 检查点读取，然后记录 ADR。
2. 构建一个 500 万条目的合成目录库，测量列表、历史浏览、备份遍历耗时和 PostgreSQL 体积。
3. 增加带引用检查的保留策略与垃圾回收，以及仓库自身的备份（调度器自 0.1.1 起已有，没有保留策略时它只会不断增加恢复点）。
4. 增加按文件夹的权限（路径白名单）、信任反向代理给出的客户端地址，然后实现 Windows SSO。
5. 确定 plan.md §10 中尚未决定的问题（卷、变化量、RPO/RTO、仓库位置、ACL 要求）。
