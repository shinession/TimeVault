# TimeVault — Implementation Plan

> **Product:** TimeVault — Enterprise File Backup, Search & Time Travel  
> **Document:** `plan.md` — v0.1 / Project kickoff / 2026-11-14  
> **Status:** Proposed architecture; validate assumptions before implementing production backup/restore  
> **Audience:** Development agents, software engineers, and server administrators  
> **Language:** English first, Chinese second

---

# Part I — English

## 1. Vision and success definition

Build a Windows Server-oriented file time machine that can rapidly search and browse **approximately 5,000,000 files**, preserve historical file/folder states in an independent backup repository, show available historical versions, and safely restore files or whole folders to an **available recovery point**.

**Positioning:** An internal, self-hosted service for enterprise file servers; not a replacement for a complete disaster-recovery, application-aware database backup, or bare-metal recovery product in its first release.

**Important semantic boundary:** “Any time” in the user experience means *any retained recovery point*. Recovering the exact state at an arbitrary second requires continuous, lossless capture of file contents and metadata, which neither Everything nor the USN Journal provides alone. The timeline must snap to actual verified recovery points; never silently claim a non-existent recovery point.

### Definition of done for first production release

1. Discover and browse authorized files/folders within configured source roots without downloading all 5M entries to the browser.
2. Search current files via Everything, with server-side limits/pagination and authorization checks.
3. Run initial full backup and repeated incremental backups to a physically separate backup repository.
4. Record consistent, committed recovery points; show file versions and browse a directory as it existed at a selected recovery point, including deleted items.
5. Preview, export, and restore one file or an entire folder; support alternate destination and in-place restore with explicit conflict policies.
6. Verify content integrity, log every restore, survive service restarts, and test disaster scenarios.
7. Pass a controlled 5M-file benchmark before calling the system production-ready.

## 2. Assumptions and constraints

| Item | Initial assumption / decision |
|---|---|
| Operating system | Windows Server 2019 first; validate compatibility with Windows Server 2016, 2022, and 2025 later. |
| Filesystem | Local NTFS data volumes in MVP; ReFS, remote SMB source shares, and cluster volumes require dedicated validation. |
| Scale | ~5M current files; total bytes, change frequency, directory depth, and retention are unknown and must be measured. |
| Language | Go for backend, job engine, and server-rendered web UI (Go templates or `templ`); small browser-side JS/HTMX allowed. |
| File search | Everything HTTP JSON API as initial adapter; keep a replaceable interface for IPC/other implementations. |
| Version metadata | PostgreSQL for durable metadata and job/audit records. |
| Live file consistency | Use Windows VSS for backup read consistency where supported; verify VSS-writer behavior. |
| Change detection | NTFS USN Journal checkpoints; use periodic reconciliation and full rescan when journal history is lost. |
| Content storage | Separate backup disk/server/repository, content-addressable objects, deduplication, integrity checks. |
| Identity | Intranet-only; integrate corporate Windows authentication through a trusted gateway where appropriate. |
| First users | Administrators or a limited pilot group; expand only after ACL-aware authorization is verified. |

**Before locking design, collect:** source-volume list and sizes, number of folders/files by volume, daily bytes changed, peak writes, backup bandwidth, retention/RPO/RTO requirements, existing backup/storage policy, supported Windows permissions, and approved backup destination.

## 3. Scope

### MVP / in scope

- Multi-root source registration, inclusion/exclusion rules, source and backup repository health.
- Current folder tree, breadcrumb navigation, filename/path search, sort, pagination, filters.
- Historical browse mode with timeline and recovery-point selector.
- File version panel: creation/capture time, file size, checksum, status, source path, optional change reason.
- Detect new/changed/renamed/moved/deleted files and folders; record tombstones for deleted paths.
- Initial full backup + periodic incremental backups, job progress, retries, verification.
- Single file restore, folder restore, download/export, dry-run diff, optional overwrite/rename/skip policies.
- Authentication, basic roles, path allowlists, audit logs, admin settings, service health.
- Retention and safe garbage collection after dependency/reference checks.

### Later phases / out of scope for initial release

- Bare-metal/OS-state recovery; bootable images.
- Application-consistent database backups (PostgreSQL, Oracle, SQL Server) without specific integrations.
- Real-time continuous data protection with per-second recovery guarantees.
- Remote SMB agents, multi-server orchestration, cloud object storage, tape, air-gapped replication.
- In-browser collaborative editing, content indexing/semantic search, ransomware detection by ML.
- Automatically mirroring folder deletes during restore without an explicit reviewed destructive action.

## 4. Architecture

```text
Browser
  └─ Go web UI (server-rendered Go templates/templ + minimal JS/HTMX)
       ├─ Authentication + authorization + audit
       ├─ File browsing and search APIs
       │    └─ Everything adapter ── localhost Everything HTTP JSON API
       ├─ Historical catalog + jobs ── PostgreSQL
       ├─ Backup coordinator and durable job queue
       │    ├─ Windows source adapter / USN checkpoint reader
       │    ├─ VSS snapshot requester (Windows-specific adapter)
       │    ├─ Snapshot manifest builder / metadata historian
       │    ├─ Content hashing, dedup, pack/blob writer
       │    └─ Immutable repository on separate storage
       └─ Restore coordinator ── preview → authorize → stage → verify → commit
```

**Separation of responsibility**

- **Everything**: fast index/search of the *current* filesystem. Do not treat its index as a content backup, a historical archive, or an authorization layer.
- **USN Journal**: helps identify changed NTFS objects. Does **not** contain previous file bytes and can wrap/reset. Detect Journal ID changes and missing ranges.
- **VSS**: provides a point-in-time read view for selected Windows volumes; a VSS snapshot is not by itself the long-term independent backup. Copy content from it to the TimeVault repository and commit a verified manifest.
- **PostgreSQL**: source of truth for recovery-point manifests, metadata history, jobs, integrity status, and audits.
- **Repository**: source of truth for immutable backed-up content, never a directory that Everything re-backs up.

### 4.1 Search integration

- Start with Everything HTTP JSON endpoint reachable **only on `127.0.0.1`** with HTTP file downloading disabled.
- Use request parameters `search`, `offset`, `count`, `json=1`, `path_column=1`, `size_column=1`, `date_modified_column=1` as supported by the installed version.
- Request bounded batches (for example 100–500 rows) and enforce API timeouts/limits; never request the default unlimited JSON result set.
- Sanitize user search expressions and constrain results to configured roots, then **independently enforce per-user authorization**; returning names from Everything without authorization can leak sensitive paths.
- Everything may expose broad indices; use separate restricted instances or an allowed-root adapter as needed. No direct browser access to its HTTP endpoint.
- Provide a mock Everything adapter for tests; design an optional IPC SDK adapter if HTTP is unavailable.
- Everything 1.4 bundles HTTP, while 1.5 requires an HTTP plugin. The distinct Everything Server plugin has enterprise Site License requirements; verify the chosen deployment and licensing before use.

### 4.2 Backup/recovery-point state machine

```text
QUEUED → SNAPSHOT_PREPARING → SNAPSHOT_READY → ENUMERATING
       → CONTENT_COPYING → MANIFEST_VALIDATING → COMMITTING → COMMITTED
                           ↘ FAILED / ABORTED (with cleanup + safe retry)
```

- Create recovery points for configured volumes. If a point spans multiple volumes, label whether the snapshots are truly coordinated as a VSS shadow-copy set.
- Use a per-volume USN high-water mark and journal identifier as incremental input, with reconciliation against the VSS point. Verify checkpoint boundaries in the implementation spike; do not assume a journal event timestamp proves snapshot membership.
- Preserve both content and historical directory metadata (including deleted/renamed entries) so a folder can be reconstructed at time T.
- Write immutable content objects with checksums before committing DB references; use a staging manifest, atomic commit, and an orphan-object sweeper.
- Only a successfully validated and committed point appears as **Restorable**. Incomplete points must never be selectable for restore.
- The timeline must show the actual `captured_at` time, retention status, and whether a recovery point has passed integrity verification.

### 4.3 Historical model and storage efficiency

**Avoid:** `5 million files × every snapshot` full copies in PostgreSQL. Instead, store stable node identity, incremental metadata versions or structurally shared manifests, and content objects only for new unique data.

Proposed logical model (physical schema to be refined after benchmark):

- `sources`: roots, volume IDs, excludes, availability and policy.
- `usn_checkpoints`: volume ID, journal ID, next USN, verified interval.
- `recovery_points`: ID, source scope, capture time, state, manifest root/hash, repository ID, verification status.
- `nodes`: stable filesystem identity where available (volume + file ID), node type, parent linkage.
- `node_history`: validity/recovery-point range; parent, name, attributes, ACL reference, object reference, tombstone state.
- `objects`: content ID/checksum, packed storage location, length, compression/encryption flags, integrity state.
- `backup_jobs`, `restore_jobs`, `job_events`, `retention_runs`, `audit_events`.

Data-design requirements: performant historical tree navigation, directory rename handling without duplicating bytes, case-insensitive Windows path semantics, robust handling of hard links and NTFS file IDs, high-cardinality indexing, transaction-safe recovery-point publication, and garbage collection only after reference liveness is proven. Benchmark the selected representation before committing to all indexes and partition layout.

**Storage implementation:** MVP can use whole-file SHA-256 content-addressable objects for simplicity, with transparent file-level dedup; add chunk-level dedup and pack files only after measuring typical file sizes and file count. Tiny files need pack-file batching to prevent the backup repository from becoming millions of expensive small objects. Every object and manifest must be verifiable independently.

### 4.4 Restore safety rules

1. Default to **restore to alternate destination**, not in-place overwrite.
2. UI always shows selected root, actual recovery-point timestamp, estimated file counts/bytes, missing objects, and conflict summary.
3. Dry-run must list adds/overwrites/skips. Deleting destination-only files to exactly mirror the past is **off by default**.
4. Require a second explicit confirmation for overwrite/in-place restore and privilege elevation for restricted paths.
5. Stage files to a temporary sibling directory or approved staging location; verify checksums before moving into place. Treat whole-folder operations as resumable transactions, not falsely atomic OS directory transactions.
6. Restore timestamps/attributes and NTFS ACLs where supported and approved; preserve original ACL on failure and report partial metadata restoration.
7. Reject traversal (`..`), malicious reparse points, unexpected symlinks, target outside the allowlist, and links to backup repository/source protected paths.
8. Record who initiated, what was restored, source point, target, conflicts, duration, and final per-file results.
9. Never delete the original or backup content as part of a regular restore workflow.

### 4.5 Security and operations

- Intranet access behind reverse proxy + HTTPS and Windows SSO/trusted identity propagation. Never trust client-provided identity headers on the public Go endpoint.
- Defense in depth: role-based operation permissions **and per-path authorization**, independent of Everything search results; admin, operator, auditor, reader roles.
- Run backup and search with least possible privileges; isolate highly privileged VSS/restore functions behind a controlled Windows service interface.
- Exclude backup storage, PostgreSQL data, temporary files, VSS paths, and secrets from source roots to avoid recursion.
- Encrypted backup storage or disk encryption per policy, protected credentials, anti-tamper retention/repository access, and separate physical storage; plan off-host/offline copy for resilience.
- Bounded workers, backpressure, I/O throttling, retry budgets, cancel/resume, recovery after unexpected termination.
- Health checks: Everything connectivity/index freshness, VSS writer/provider health, backup point age, USN journal continuity, database connectivity, free capacity, object verification.
- Use structured JSON logs, metrics, alerts, and documented restore drills.
- Check corporate policy and Everything/plugin licensing before deployment.

## 5. Suggested repository layout

```text
timevault/
├── plan.md
├── README.md
├── docs/
│   ├── architecture.md
│   ├── adr/                       # Architecture decision records
│   ├── api.md
│   ├── restore-runbook.md
│   ├── disaster-recovery.md
│   └── benchmark.md
├── cmd/
│   ├── timevault-server/
│   └── timevault-worker/
├── internal/
│   ├── config/
│   ├── auth/
│   ├── api/
│   ├── ui/                       # Go templates / templ components
│   ├── search/                   # Adapter interface, Everything HTTP impl
│   ├── catalog/                  # Historical browse and metadata
│   ├── backup/                   # Jobs, snapshots, manifests
│   ├── journal/                  # Windows USN adapter
│   ├── vss/                      # Windows VSS adapter
│   ├── repository/               # Immutable objects, GC, verification
│   ├── restore/
│   ├── audit/
│   └── platform/windows/
├── migrations/
├── web/
│   ├── assets/
│   └── templates/
├── configs/
│   └── config.example.yaml
├── scripts/
│   ├── install-service.ps1
│   └── benchmark.ps1
└── tests/
    ├── integration/
    ├── e2e/
    └── fixtures/
```

For MVP, a **single deployable Go service** can host HTTP and limited workers if blocking VSS operations are safely isolated. Split the privileged worker only when the security model or benchmarking warrants it. Do not add Redis or microservices without evidence.

## 6. Initial API surface (illustrative)

| Method | Endpoint | Purpose |
|---|---|---|
| GET | `/api/v1/health` | Readiness, Everything, VSS, DB, repository states |
| GET | `/api/v1/sources` | Authorized source roots |
| GET | `/api/v1/files?source_id=&parent_id=&cursor=` | Current folder listing |
| GET | `/api/v1/search?q=&source_id=&cursor=&limit=` | Current Everything-backed search |
| GET | `/api/v1/recovery-points?source_id=&before=` | Actual committed restore points |
| GET | `/api/v1/history/tree?point_id=&node_id=&cursor=` | Folder as of selected point |
| GET | `/api/v1/history/versions?node_id=` | Historical versions of a file |
| POST | `/api/v1/backups` | Enqueue authorized backup job |
| GET | `/api/v1/jobs/{id}` | Persistent job status/progress |
| POST | `/api/v1/restores/preview` | Dry-run plan, count, conflicts, warnings |
| POST | `/api/v1/restores` | Execute confirmed restore request |
| GET | `/api/v1/audit` | Authorized audit trail |

- Cursor-based pagination for large trees; consistent ordering and stable token encoding.
- Long jobs return IDs, not blocking HTTP requests; stream progress with SSE or polling.
- Restore endpoint requires CSRF protection (if cookie-authenticated), server-side input validation, idempotency keys, and a separate explicit confirmation token tied to the preview.

## 7. UI / UX requirements

The Time Machine-inspired UI must use real recovery points rather than a decorative date slider:

- **Explorer:** source selector, folder tree, breadcrumbs, sortable table, permissions-aware toolbar.
- **Instant search:** Everything-backed filename/path lookup, scoped search, type/size/date filters.
- **Time travel:** horizontal timeline and date jump; list only **available verified points**; clearly mark current vs historical mode.
- **Version detail:** version comparison (metadata first), restore preview, export single version.
- **Restore center:** target selection, conflict policy, dry-run change list, safety confirmation, progress/audit.
- **Backup center:** jobs, schedule, policy, source health, capacity, failed files, verification status.
- **Admin:** source/ignore lists, security, audit logs, retention policies, maintenance and diagnostics.
- Responsive layout, accessible keyboard/focus behavior, no full-page reloads for folder navigation; all links should be relative and deep-linkable.

## 8. Ordered implementation phases for development agents

### P0 — Discovery, proof of concept, and risk retirement

Tasks:
- [ ] Record OS/filesystem/source data characteristics, approved rights and storage path.
- [ ] Confirm Everything version, install mode, HTTP plugin configuration, and licensing.
- [ ] Build `SearchProvider` interface and local Everything HTTP JSON proof of concept, including malformed query, timeout, and large-result tests.
- [ ] Prove VSS create/read/cleanup on test NTFS volume and a locked sample file; capture all error paths.
- [ ] Prove USN checkpoint read, incremental updates, Journal ID reset/missing-history detection.
- [ ] Benchmark 5M-name search, directory browsing, raw metadata import, small-file hashing, and storage throughput on representative hardware.
- [ ] Document ADRs for snapshot semantics, data model, content object format, and service privilege boundary.

**Gate:** No production data changes. P0 must demonstrate search performance and reliable capture of modified/deleted test files before designing the main engine.

### P1 — Read-only explorer and service foundation

Tasks:
- [ ] Go HTTP server, config loader, PostgreSQL migrations, logging/health endpoints.
- [ ] Windows service lifecycle and graceful shutdown.
- [ ] Go-rendered UI shell, explorer with tree, breadcrumbs, server-side paginated search.
- [ ] Authentication and basic roles; authorized-root/path checks before any file information is returned.
- [ ] Search adapter tests and synthetic 5M-record load test.

**Gate:** Users can browse/search safely; no restore or backup writes through the UI.

### P2 — Backup and history engine

Tasks:
- [ ] Source/repository configuration; exclusion and storage health validation.
- [ ] Initial backup from VSS point, content hashing, object writing, persistent job state.
- [ ] Incremental change processing with USN checkpoints + reconciliation/full-rescan fallback.
- [ ] Versioned history manifest, committed recovery-point publication, deleted-file tombstones.
- [ ] Historical tree navigation, file version history, retention policy, verification job.
- [ ] Simulate interruption during every backup state and prove restart recovery.

**Gate:** Full and incremental backups produce independently verifiable historical states; deleted items remain recoverable.

### P3 — Restore workflows

Tasks:
- [ ] Dry-run restore planner for file and folder.
- [ ] Alternate-location restore; then explicitly confirmed in-place restore.
- [ ] File/folder metadata restoration, conflict policies, path validation, resumability.
- [ ] Restore UI with diff summary, progress and per-file results.
- [ ] Audit and role enforcement; automated checksum match after restore.

**Gate:** Successful restore tests for deleted files, renamed folders, overwritten versions, partial failures, large trees, and unauthorized access attempts.

### P4 — Production hardening and operational handover

Tasks:
- [ ] End-to-end load test at 5M files and measured expected daily churn.
- [ ] Simulate loss of Everything, PostgreSQL restart, disk full, VSS failure, journal wrap, interrupted backup and interrupted restore.
- [ ] Repository garbage collection, integrity verification, backup/restore of TimeVault metadata itself.
- [ ] Retention policy, disk capacity alerts, observability, security review, incident runbooks.
- [ ] Test off-host repository and restore on a fresh server; produce final operational checklist.

**Gate:** Operations owner accepts documented RPO/RTO, data recovery drill, and security review.

## 9. Verification plan and acceptance matrix

| Scenario | Required outcome |
|---|---|
| 5M catalog entries | Bounded memory and server-side pagination; no massive full-table browser transfer. |
| Search unavailable | Historical browsing still usable; current search shows an explicit degraded state. |
| Modified file | Original and new version independently restorable when captured by committed points. |
| Deleted file | Historical path remains visible and can be restored. |
| Renamed/moved folder | Historical location resolves correctly at older recovery points. |
| Locked file | VSS proof verifies supported consistency and clearly reports writer exceptions. |
| Interrupted backup | Uncommitted point hidden; recovery or cleanup safe after restart. |
| Repository object missing | Restore preview blocks/flags it and never reports false success. |
| Unauthorized search/restore | No unauthorized path names, previews, downloads, or writes. |
| Restore to original path | Explicit overwrite preview and confirmation, verified result, complete audit. |
| Filesystem edge cases | Unicode, long Windows paths, mixed case, hard links, sparse files, junctions, ACLs handled or explicitly excluded. |
| Journal reset/wrap | Reconciliation/full scan occurs; no silent omission of changes. |

**Provisional non-binding targets** to test on representative hardware: current search P95 < 2 seconds for bounded queries; first explorer page < 2 seconds; normal UI response independent of background backup load. Backup duration and storage sizing **cannot be promised from a 5M-file count alone**; measure total bytes and daily changed bytes first.

## 10. Open decisions (do not guess)

1. Which server(s), volumes, and SMB paths are in scope? Are any protected system files or application databases included?
2. How much total data exists and how many bytes change per day?
3. Desired frequency and retention (example only: hourly 48 h, daily 30 d, weekly 12 w, monthly 12 m)? Define acceptable RPO/RTO.
4. Where will immutable backup objects live (local separate disk, second server, NAS, object storage)? Is an off-host copy mandatory?
5. Are users allowed to view/restore only their NTFS-authorized folders? Which AD groups can overwrite?
6. Is Everything 1.4 or 1.5 installed? Is a separate Everything Server plugin intended? Verify license.
7. Is preserving Windows ACLs, alternate data streams, EFS, hard links, sparse files, and ownership required in MVP?
8. Should snapshots span multiple volumes with atomic consistency, and which VSS writers are required?

## 11. Development agent operating rules

1. Work sequentially: **P0 → P1 → P2 → P3 → P4**; submit ADR/tests before risky implementation changes.
2. Produce architecture, API, and test documentation **bilingually, English before Chinese**.
3. No source data deletion/overwrite as part of testing; use isolated fixtures and non-production paths.
4. Never claim “arbitrary-time recovery” without matching recovery points or continuous captured content.
5. No exposing Everything HTTP endpoint, bypassing authorization, or hard-coded credentials.
6. Prefer Go standard library and a small set of vetted libraries; pin versions and document why each dependency is needed.
7. Every feature needs unit tests, integration tests, failure tests, and a clear rollback procedure where applicable.
8. Maintain backward-compatible DB migrations and validate recovery before destructive retention/GC.
9. Keep Windows-specific code behind build tags/interfaces; allow cross-platform tests of non-Windows logic.
10. End each phase with a checked demo, benchmarks, unresolved issues, and a documented go/no-go decision.

## 12. Reference documentation

- Everything HTTP interface: https://www.voidtools.com/support/everything/http/
- Everything SDK / IPC: https://www.voidtools.com/support/everything/sdk/
- Everything Server Site License distinction: https://www.voidtools.com/support/everything/everything_server/
- Windows USN journal: https://learn.microsoft.com/en-us/windows/win32/fileio/change-journals
- USN journal limitations: https://learn.microsoft.com/en-us/windows/win32/fileio/change-journal-records
- Windows Server VSS: https://learn.microsoft.com/en-us/windows-server/storage/file-server/volume-shadow-copy-service

---

# Part II — 中文

## 1. 项目目标

开发面向 Windows Server 的企业文件时间机器 **TimeVault**。系统需要支持约 **500 万个文件**的快速搜索、目录浏览、历史版本备份，以及将文件或文件夹恢复至**已有且经过验证的历史恢复点**。

**关键边界：**“任意历史时刻”在第一版应严格解释为“任意已保留的恢复点”。如果需要精确恢复到某一秒，就需要在那一秒之前完整保存当时的文件内容与元数据。Everything 和 USN Journal 本身并不会保存这些历史内容。因此时间轴只能选择真正存在的恢复点，不允许虚构历史状态。

### 首个生产版本验收目标

1. 目录浏览和搜索支持数百万级文件，不得将全部文件清单加载到浏览器。
2. Everything 用于当前文件高速搜索，结果分页且经过权限过滤。
3. 全量备份、增量备份及独立备份仓库可稳定工作。
4. 能看到指定恢复点的目录结构、历史文件版本、已删除文件。
5. 支持文件/文件夹恢复、预览差异、恢复到其他位置和确认后原位置覆盖。
6. 支持数据完整性校验、审计、故障重试与服务重启后的恢复。
7. 使用接近真实的 500 万文件数据集完成性能与恢复演练。

## 2. 技术与部署方案

| 模块 | 方案 |
|---|---|
| 操作系统 | 首先支持 Windows Server 2019；后续验证 2016、2022、2025 |
| 文件系统 | MVP 优先本地 NTFS；ReFS/SMB 网络共享另行验证 |
| 前后端 | Go 后端 + Go 模板/templ 服务端渲染；少量 HTMX/JavaScript 负责交互 |
| 实时搜索 | Everything HTTP JSON API，限定 `127.0.0.1` 访问 |
| 历史元数据 | PostgreSQL |
| 变更检测 | NTFS USN Journal，保留检查点并侦测 Journal 变化或记录缺失 |
| 一致性读取 | VSS 创建指定时刻的卷快照 |
| 内容备份 | 独立磁盘或服务器，内容哈希、去重、校验及后续分块优化 |
| 身份权限 | 内网 HTTPS + Windows SSO/可信反向代理 + 路径级授权 |
| 部署方式 | Windows Service；初版优先单体 Go 服务，必要时独立高权限 Worker |

### 职责边界

- **Everything：**搜索与展示当前文件，不保存历史文件内容，不负责访问授权。
- **USN Journal：**检测 NTFS 文件变动事件；不提供旧版本字节，而且存在记录回卷、清除和重建风险。
- **VSS：**读取一致性时间点的卷视图；必须将内容复制到独立仓库才能形成长期备份。
- **PostgreSQL：**维护版本元数据、恢复点、目录历史、备份任务与审计。
- **备份仓库：**独立保存真实历史内容，不能直接放在被备份的目录内，避免递归备份。

## 3. 功能清单

### 基础功能

- 配置备份源和排除规则，查看源状态与备份仓库存储容量。
- 当前目录树、面包屑路径、文件列表、分页、排序、搜索过滤。
- 支持历史时间轴，显示真实存在且已提交的恢复点。
- 查看文件版本、大小、修改时间、采集时间、哈希、版本状态。
- 记录新增、修改、删除、重命名与移动事件，删除的文件也要可回溯。
- 提供全量/增量任务、进度、校验、失败原因、重试及保留策略。
- 允许单文件和整目录恢复到其他目录，确认后可覆盖原位置。
- 提供 Dry Run：提前检查将增加、覆盖、跳过多少文件。
- 每次恢复必须有操作人、版本时间、目标路径、结果和审计日志。

### 暂不纳入第一版

系统盘裸机恢复、专业数据库一致性备份、每秒级连续数据保护、跨服务器代理调度、SMB 网络源的完整支持、云存储/磁带，以及自动删除目标目录额外文件的镜像恢复。

## 4. 核心数据模型

建议建立：

- `sources`：备份来源、NTFS 卷与策略。
- `usn_checkpoints`：Journal ID、Next USN、检查状态。
- `recovery_points`：快照时间、状态、Manifest 校验值、完整性结果。
- `nodes`：文件或目录的稳定标识和目录关系。
- `node_history`：历史名称、父目录、属性、内容引用、生效范围及删除标记。
- `objects`：哈希、存储位置、大小、压缩和校验状态。
- `backup_jobs`、`restore_jobs`、`job_events`、`audit_events`。

**性能原则：**不要每小时在 PostgreSQL 复制 500 万行完整目录树。应使用增量版本记录、可复用 Manifest 或共享树节点；新旧版本共用未改变的文件内容。MVP 先考虑按文件哈希去重，待真实样本确认大量小文件或大文件比例后再引入分块去重和 Pack 文件。

必须评估历史文件夹重命名/移动、目录结构随时间变化、Windows 大小写规则、硬链接、超长路径、ACL 等边界。

## 5. 恢复安全原则

1. 默认恢复到**其他目录**，而不是覆盖线上文件。
2. 先展示恢复点的准确时间、文件数量/大小、预计冲突和缺失对象。
3. 覆盖线上文件时必须明确确认；可选择覆盖、跳过或改名。
4. 默认不删除目标目录中额外存在的文件；镜像式删除只能作为单独高权限操作。
5. 恢复采用暂存、校验、落盘的步骤，并能处理中断和部分失败。
6. 文件 ACL、时间戳和属性需要尽量恢复，失败必须如实显示。
7. 必须阻止目录穿越、恶意软链接/重解析点、超出允许目录的恢复请求。
8. 所有操作必须记录审计，任何普通恢复操作都不能删除备份仓库中的历史内容。

## 6. 开发阶段与交付要求

### P0 — 技术验证

- [ ] 确认备份范围、卷类型、总容量、每天修改量、RPO/RTO 和目标磁盘。
- [ ] 验证 Everything 版本、HTTP JSON、服务模式、安装与授权。
- [ ] 完成搜索 Adapter 的 PoC，验证限定分页、超时、错误处理。
- [ ] 用测试卷验证 VSS：创建、读取被占用文件、释放快照。
- [ ] 验证 USN：首次读取、续读、Journal ID 改变和记录丢失处理。
- [ ] 用代表性数据测试 500 万文件搜索、索引、哈希和备份吞吐。
- [ ] 输出关键 ADR 与测试结果，未通过不得开始生产备份。

### P1 — 只读文件浏览器

- [ ] Go 服务、数据库迁移、配置、日志和健康检查。
- [ ] 目录浏览、面包屑、分页、Everything 即时搜索。
- [ ] Windows Service 注册、优雅退出。
- [ ] SSO/角色权限与路径级权限校验。
- [ ] 完成搜索和大目录性能测试；此阶段不允许页面执行备份/恢复写操作。

### P2 — 备份及历史版本引擎

- [ ] 独立备份仓库、源目录过滤、全量备份。
- [ ] VSS 一致性读取、内容校验、去重、持久化任务。
- [ ] USN 增量处理、Journal 异常检测与完整扫描回退。
- [ ] 恢复点 Manifest、历史目录、已删除文件的历史记录。
- [ ] 历史时间轴、版本列表、清理保留策略。
- [ ] 验证每个备份状态下的强制中断与安全恢复。

### P3 — 文件与文件夹恢复

- [ ] 恢复预览、文件差异、目标位置及覆盖冲突检查。
- [ ] 文件、整目录、其他位置以及确认后原位置恢复。
- [ ] 恢复校验、元数据恢复、审计记录、取消及中断恢复。
- [ ] 验证删除文件、目录移动、大量文件和无权访问场景。

### P4 — 生产准备

- [ ] 完整 500 万文件规模测试与真实日变化量测试。
- [ ] 验证 Everything 不可用、VSS 失败、磁盘不足、数据库重启、USN 回卷等场景。
- [ ] 完整性巡检、保留清理、容量预警、权限与安全审查。
- [ ] 在另一台服务器上完成灾难恢复演练，输出操作手册与交接清单。

## 7. 推荐 API

| 接口 | 用途 |
|---|---|
| `GET /api/v1/files` | 当前文件夹浏览 |
| `GET /api/v1/search` | Everything 当前搜索 |
| `GET /api/v1/recovery-points` | 有效历史恢复点 |
| `GET /api/v1/history/tree` | 指定时间点历史目录 |
| `GET /api/v1/history/versions` | 文件版本历史 |
| `POST /api/v1/backups` | 创建备份任务 |
| `POST /api/v1/restores/preview` | 恢复预览/Dry Run |
| `POST /api/v1/restores` | 确认执行恢复 |
| `GET /api/v1/jobs/{id}` | 持久化任务进度 |
| `GET /api/v1/health` | 服务健康状态 |

大结果必须分页；耗时操作使用后台 Worker + 持久化 Job，不在 HTTP 请求中同步执行。恢复 API 必须具备授权、幂等标识、防 CSRF、二次确认和审计。

## 8. 验收场景

- 500 万文件不会造成前端全量加载或内存无限增长。
- Everything 断线时，历史数据仍可以查阅，实时搜索显示降级状态。
- 修改过的文件在对应有效恢复点可恢复为正确的旧版。
- 被删除文件及被重命名/移动的历史目录仍能回溯。
- 备份中断不会产生“可恢复但缺少内容”的假成功恢复点。
- 仓库内容缺失时预览必须报告错误，禁止把失败显示为成功。
- 未授权用户无法查询敏感文件名、下载或恢复它们。
- 原位置覆盖必须有差异预览、明确确认与完整审计。
- USN 记录丢失时回退重新扫描，不允许静默漏备份。
- 需测试 Unicode、Windows 超长路径、ACL、软链接、硬链接等边界并记录不支持情况。

**暂定性能目标：**在代表性硬件上，限定结果的当前搜索 P95 小于 2 秒，文件夹首次显示小于 2 秒；真正的备份时间须依据总数据量、日变化量及磁盘吞吐实测，不得只凭“500 万文件”承诺时间。

## 9. 待确认决策

1. 具体有哪些 Windows Server、磁盘卷、共享路径需要纳入？
2. 总数据容量和每天变化量有多大？备份频率与保留周期？
3. 恢复点目标 RPO 与整目录恢复所需 RTO？
4. 备份数据要保存到第二磁盘、另一台服务器，还是 NAS？是否要求离线副本？
5. 需要严格按 NTFS ACL/AD 组过滤文件列表和恢复权限吗？
6. Everything 使用 1.4 还是 1.5？是否涉及另需授权的 Everything Server 插件？
7. ACL、ADS、EFS、硬链接、稀疏文件、拥有者信息是否要求完整保留？
8. 备份是否跨多个卷，需要统一时间点及应用 VSS Writer 一致性？

## 10. Agent 执行规则

1. 依序执行 P0 → P4，每个阶段完成技术文档、测试与复核后再进入下一阶段。
2. **所有新技术文档提供中英文双语，英文在前。**
3. 开发测试只使用隔离环境与测试数据，严禁覆盖或删除生产文件。
4. 不得夸大历史恢复精度，时间轴仅显示真实存在的可恢复版本。
5. 严禁将 Everything HTTP 直接暴露到网络前端，严禁绕过路径授权或硬编码凭据。
6. 尽可能采用 Go 标准库与少量已审查依赖；所有依赖固定版本。
7. 备份、恢复、保留清理和迁移功能都需要异常与回滚测试。
8. Windows 特定实现通过接口/build tags 隔离，方便跨平台测试核心逻辑。
9. 每阶段交付演示、性能指标、已知风险、未完成事项和继续开发的 go/no-go 结论。

---

**Project starter note / 项目启动说明：** This file is an executable planning baseline, not a statement that a working backup engine already exists. / 本文件是可供开发 Agent 执行的项目计划，不表示已有可运行的备份引擎。
