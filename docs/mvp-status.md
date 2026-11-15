# English

## Overview

This is the honest status of the MVP against [plan.md](./plan.md). The MVP is a **vertical slice**: one working path through search/browse, backup, history and restore on a local NTFS folder. It is not a production backup system, and nothing here has been run at the 5-million-file scale.

Status words: **Done** (implemented and tested), **Partial** (works with stated limits), **Not done** (planned, not implemented), **Not verified** (no evidence yet).

## Scope against plan.md §3

| Plan item | Status | Notes |
|---|---|---|
| Multi-root source registration, exclusions, source and repository health | Done | UI and `POST /api/v1/sources`; exclusion globs; repository free space and write test on the status page. |
| Current folder tree, breadcrumbs, search, sort, pagination | Done | Live folder from disk with protected / changed / new markers; Everything-backed search; sorting is client-side over the loaded rows. |
| Historical browse with timeline and recovery-point selector | Done | Time Machine page; only committed points appear. |
| File version panel | Done | Capture time, validity, size, modification time, SHA-256, content availability. "Optional change reason" is not implemented. |
| Detect new / changed / renamed / moved / deleted, tombstones | Partial | New, changed and deleted are tracked; rename and move are recorded as delete plus add. Deleted paths stay visible at older points. |
| Initial full backup and incremental backups, progress, retries, verification | Partial | Full and incremental by walking the source and comparing size and modification time; progress, cancel, integrity verification (shallow and deep). Automatic retries and schedules are not implemented (backups are started by hand or through the API). |
| Single file and folder restore, export, dry-run, conflict policies | Partial | File and folder restore with dry-run, skip / rename / overwrite, hash verification, per-item results. Restore is **only into a folder below the restore root**; restoring in place and downloading/exporting a file are not implemented. |
| Authentication, roles, path allow-lists, audit, admin settings, health | Partial | Single administrator, signed sessions, CSRF, audit trail, status page. No roles, no per-path authorization, no SSO. |
| Retention and safe garbage collection | Not done | The repository only grows; nothing is ever deleted. |

## Phases

| Phase | Status |
|---|---|
| P0 technical proof | Partial. The Everything adapter was verified against a real Everything 1.4.1.1024 instance. VSS and USN were **not** prototyped. No 5M-file benchmark was run. |
| P1 read-only explorer and service foundation | Done for the MVP scope (HTTP server, migrations, logging, health, templates, sign-in, search adapter tests). Not done: Windows service registration, 5M synthetic load test. |
| P2 backup and history engine | Partial. VSS consistency and USN incremental detection are not implemented; interruption recovery is tested for crash, cancel and restart. |
| P3 restore | Partial. See the restore row above; in-place restore is not implemented. |
| P4 production hardening | Not done. |

## Acceptance matrix (plan.md §9)

| Scenario | Status | Evidence |
|---|---|---|
| 5M catalog entries, bounded memory, server-side pagination | Not verified | Listing uses keyset paging and per-folder processing, so memory is bounded by the largest folder; no scale test yet. |
| Search unavailable, history still usable, explicit degraded state | Done | `TestSearchAndItsDegradedState` |
| Modified file: both versions restorable | Done | `TestIncrementalBackupKeepsHistoryAndTracksChanges`, `TestBackupHistoryAndRestoreThroughTheAPI` |
| Deleted file stays visible and restorable | Done | `TestDeletedFolderIsRestorableFromOlderPoint`, `TestRestoreFolderAndFileFromOlderPointAfterDeletion` |
| Renamed or moved folder resolves correctly at older points | Partial | Old location resolves at old points; the new name is a separate add (`TestIncrementalBackupKeepsHistoryAndTracksChanges`). |
| Locked file reported clearly | Partial | Skipped with the reason and captured later; no VSS (`TestLockedFileIsSkippedThenCapturedWhenReleased`). |
| Interrupted backup: uncommitted point hidden, safe cleanup | Done | `TestCancelledBackupLeavesNoTraceAndRetrySucceeds`, `TestInterruptedBuildIsRecoveredAtStartup` |
| Repository object missing: preview blocks, never false success | Done | `TestMissingObjectBlocksTheRestoreUnlessPartialIsAllowed`, `TestCorruptObjectIsNeverRestored`, `TestVerifyDetectsMissingAndCorruptObjects` |
| Unauthorized search/restore: no names, previews, downloads or writes | Partial | Sign-in and CSRF are enforced (`TestSignInAndGuards`, `TestCSRFAndSecurityHeaders`); there is only one role, so there is no per-user authorization to test. |
| Restore to original path with preview, confirmation, audit | Not done | By design, restores go to a folder below the restore root, never to the original location. Preview, confirmation and audit are done (`TestRestoreThroughTheForms`). |
| Filesystem edge cases | Partial | Unicode and mixed-case names, junctions, links swapped in during a scan, unreadable folders and very deep paths (backup of paths longer than 260 characters; nothing deeper than 256 folders is recorded) are tested. Not tested: restoring paths longer than 260 characters. Not preserved: ACLs, alternate data streams, hard-link relationships. |
| Journal reset / wrap | Not applicable | There is no USN journal use; every backup walks the whole source, so no change is silently omitted. |

## Known limitations

- Every backup walks the whole source; there is no change journal, so very large sources will be slow.
- A rename or move of a large folder is recorded as delete plus add of every entry (the content bytes are stored once).
- Files are read from the live disk, not from a VSS snapshot ("Live scan" is shown on every point).
- One administrator, no roles, no SSO; no TLS inside the service.
- No scheduler, retention, garbage collection, off-host copy, repository encryption or Windows service wrapper.
- Chunking and pack files are not implemented: every file is one object, so millions of tiny files mean millions of repository files.
- The job log keeps at most 2,000 lines per job, and a restore stores per-item results for at most 20,000 successful items (failures are always stored).
- Sources must be local folders; network shares (UNC paths) are rejected. A restore can select at most 50 paths and plan at most 2,000,000 items.
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

## Suggested next steps

1. Run the P0 spikes that were skipped: VSS create/read/cleanup on a test volume and USN checkpoint reading, then record ADRs.
2. Build a synthetic 5M-entry catalog and measure listing, history browse, backup walk time and PostgreSQL size.
3. Add a scheduler, retention with reference-checked garbage collection and repository backup.
4. Add roles and path allow-lists, then Windows SSO through a trusted proxy.
5. Decide the open questions in plan.md §10 (volumes, change rate, RPO/RTO, repository location, ACL requirements).

---

# 中文

## 概述

这是 MVP 相对于 [plan.md](./plan.md) 的真实状态。MVP 是一个**垂直切片**：在本地 NTFS 文件夹上，打通"搜索/浏览、备份、历史、恢复"这一条完整路径。它不是生产级备份系统，这里的任何内容都没有在 500 万文件规模上运行过。

状态用语：**已完成**（已实现且有测试）、**部分完成**（可用但有所述限制）、**未完成**（有计划但未实现）、**未验证**（暂无证据）。

## 对照 plan.md §3 的范围

| 计划项 | 状态 | 说明 |
|---|---|---|
| 多根备份源登记、排除规则、备份源与仓库健康状态 | 已完成 | 界面和 `POST /api/v1/sources`；排除通配规则；状态页显示仓库剩余空间和写入测试。 |
| 当前目录树、面包屑、搜索、排序、分页 | 已完成 | 从磁盘读取实时文件夹并标注已保护 / 已变更 / 新增；Everything 搜索；排序在已加载的行上由前端完成。 |
| 带时间轴和恢复点选择器的历史浏览 | 已完成 | 时间机器页面；只显示已提交的恢复点。 |
| 文件版本面板 | 已完成 | 采集时间、有效期、大小、修改时间、SHA-256、内容是否可用。"可选的变更原因"未实现。 |
| 检测新增 / 修改 / 重命名 / 移动 / 删除，墓碑记录 | 部分完成 | 新增、修改和删除都会跟踪；重命名和移动被记录为"删除 + 新增"。被删除的路径在较早的恢复点仍然可见。 |
| 首次全量与增量备份、进度、重试、校验 | 部分完成 | 通过遍历备份源并比较大小和修改时间实现全量和增量；有进度、取消、完整性校验（浅层和深度）。自动重试和定时计划未实现（备份需手动或通过 API 启动）。 |
| 单文件与文件夹恢复、导出、dry-run、冲突策略 | 部分完成 | 文件和文件夹恢复，含 dry-run、跳过 / 改名 / 覆盖、哈希校验、逐项结果。恢复**只能写入恢复根目录下的文件夹**；原位置恢复以及文件下载/导出未实现。 |
| 认证、角色、路径白名单、审计、管理设置、健康状态 | 部分完成 | 单一管理员、签名会话、CSRF、审计记录、状态页。没有角色、没有按路径授权、没有 SSO。 |
| 保留策略与安全垃圾回收 | 未完成 | 仓库只会增长；不会删除任何内容。 |

## 各阶段

| 阶段 | 状态 |
|---|---|
| P0 技术验证 | 部分完成。Everything 适配器已针对真实的 Everything 1.4.1.1024 实例验证。VSS 和 USN **没有**做原型。没有运行 500 万文件基准测试。 |
| P1 只读浏览器与服务基础 | MVP 范围内已完成（HTTP 服务、迁移、日志、健康检查、模板、登录、搜索适配器测试）。未完成：Windows 服务注册、500 万合成数据负载测试。 |
| P2 备份与历史引擎 | 部分完成。未实现 VSS 一致性和 USN 增量检测；中断恢复已针对崩溃、取消和重启做过测试。 |
| P3 恢复 | 部分完成。见上面的恢复一行；未实现原位置恢复。 |
| P4 生产加固 | 未完成。 |

## 验收矩阵（plan.md §9）

| 场景 | 状态 | 证据 |
|---|---|---|
| 500 万目录条目，内存有界，服务端分页 | 未验证 | 列表使用键集分页并按文件夹处理，所以内存受最大文件夹限制；尚无规模测试。 |
| 搜索不可用时历史仍可用，并明确显示降级 | 已完成 | `TestSearchAndItsDegradedState` |
| 修改过的文件：两个版本都可恢复 | 已完成 | `TestIncrementalBackupKeepsHistoryAndTracksChanges`、`TestBackupHistoryAndRestoreThroughTheAPI` |
| 被删除的文件仍然可见并可恢复 | 已完成 | `TestDeletedFolderIsRestorableFromOlderPoint`、`TestRestoreFolderAndFileFromOlderPointAfterDeletion` |
| 重命名或移动的文件夹在较早恢复点能正确解析 | 部分完成 | 旧位置在旧恢复点能解析；新名称是单独的新增（`TestIncrementalBackupKeepsHistoryAndTracksChanges`）。 |
| 被锁定的文件有清晰的报告 | 部分完成 | 跳过并写明原因，之后会被采集；没有 VSS（`TestLockedFileIsSkippedThenCapturedWhenReleased`）。 |
| 备份被中断：未提交的恢复点被隐藏，安全清理 | 已完成 | `TestCancelledBackupLeavesNoTraceAndRetrySucceeds`、`TestInterruptedBuildIsRecoveredAtStartup` |
| 仓库对象缺失：预览阻止，绝不假成功 | 已完成 | `TestMissingObjectBlocksTheRestoreUnlessPartialIsAllowed`、`TestCorruptObjectIsNeverRestored`、`TestVerifyDetectsMissingAndCorruptObjects` |
| 未授权的搜索/恢复：不泄露名称、预览、下载或写入 | 部分完成 | 强制登录和 CSRF（`TestSignInAndGuards`、`TestCSRFAndSecurityHeaders`）；目前只有一个角色，所以没有可测试的按用户授权。 |
| 恢复到原路径，需预览、确认和审计 | 未完成 | 设计上恢复只写入恢复根目录下的文件夹，绝不写入原位置。预览、确认和审计已完成（`TestRestoreThroughTheForms`）。 |
| 文件系统边界情况 | 部分完成 | 已测试 Unicode 和大小写混合的名称、联接点、扫描期间被替换进来的链接、不可读文件夹，以及很深的路径（备份长度超过 260 字符的路径；不会记录深于 256 层文件夹的内容）。未测试：恢复长度超过 260 字符的路径。未保留：ACL、备用数据流、硬链接关系。 |
| Journal 重置 / 回卷 | 不适用 | 没有使用 USN journal；每次备份都遍历整个备份源，不会静默漏掉任何变化。 |

## 已知限制

- 每次备份都会遍历整个备份源；没有变更日志，因此非常大的备份源会比较慢。
- 大文件夹的重命名或移动会被记录为每一项的"删除 + 新增"（内容字节只存一份）。
- 文件读取自在线磁盘，而不是 VSS 快照（每个恢复点都会显示"实时扫描"）。
- 一个管理员，没有角色，没有 SSO；服务内没有 TLS。
- 没有计划任务、保留策略、垃圾回收、异机副本、仓库加密或 Windows 服务封装。
- 没有实现分块和 Pack 文件：每个文件就是一个对象，所以数百万个小文件意味着仓库中有数百万个文件。
- 每个任务的日志最多保留 2,000 行；恢复最多存储 20,000 个成功项目的逐项结果（失败项始终存储）。
- 备份源必须是本地文件夹；网络共享（UNC 路径）会被拒绝。一次恢复最多可选择 50 个路径，最多规划 2,000,000 个条目。
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

## 建议的下一步

1. 补做被跳过的 P0 验证：在测试卷上验证 VSS 的创建/读取/清理以及 USN 检查点读取，然后记录 ADR。
2. 构建一个 500 万条目的合成目录库，测量列表、历史浏览、备份遍历耗时和 PostgreSQL 体积。
3. 增加调度器、带引用检查的保留策略与垃圾回收，以及仓库自身的备份。
4. 增加角色与路径白名单，然后通过可信代理实现 Windows SSO。
5. 确定 plan.md §10 中尚未决定的问题（卷、变化量、RPO/RTO、仓库位置、ACL 要求）。
