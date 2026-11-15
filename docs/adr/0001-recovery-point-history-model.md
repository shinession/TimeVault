# English

## Status

Accepted for the MVP (2026-11-15). Covers the "snapshot semantics" and "data model" items of plan.md P0.

## Context

TimeVault must answer "what did this folder look like at recovery point N?" and "which versions of this file exist?" for sources that may hold millions of entries and many recovery points. plan.md §6 sketches a `node_history` table with a validity range. A recovery point has to be either fully visible or invisible: an interrupted backup must never show a half-built state.

Options that were considered:

- **A full manifest per recovery point.** Simple to reason about, but it stores every entry again for every point; with millions of entries it costs rows and time even when nothing changed.
- **Manifest files outside the database.** Cheap to write but impossible to browse a folder or list a file's versions without reading whole files.
- **Validity ranges (chosen).** Store one row per state of a path and the range of recovery points for which that state held.

## Decision

- Each source has recovery points numbered `seq = 1, 2, 3 ...` (`recovery_points`). A point is `building`, `committed`, `failed` or `aborted`; only one point per source can be `building` (partial unique index).
- Every path is a slot in `nodes` (parent and exact name, plus a lower-cased `name_key` for case-insensitive ordering and lookups). The states of a slot are rows in `node_versions` with `first_point` and `last_point`; `last_point IS NULL` means "still current". A version is visible at point `s` when `first_point <= s AND (last_point IS NULL OR last_point >= s)`.
- A backup writes only what changed. An unchanged entry costs nothing; a modified entry closes its old version at the previous point and opens a new one; a deleted entry only closes its version, so the path is still visible, and restorable, at older points. A rename or move is a delete plus an add; the content is stored once in the repository, so the bytes are not duplicated, but every entry gets new rows.
- A build writes its rows under its own `seq`. Readers only query committed points, so nothing the build has written is visible before the commit. Before a new build starts, and at service start, rows left by a failed or interrupted build are removed and versions it closed are reopened, under a per-source advisory lock.
- Each point stores a manifest digest (SHA-256 over canonical text lines for every entry, in a fixed depth-first order). It is computed during the scan and recomputed by the verify job from the catalog, so a change to the catalog after the commit is detected.
- Entries that could not be read are stored as versions with `state = 'skipped'` and a reason; the previous good version is kept when a re-read fails. Directories have versions only to say that they exist; they are never "modified".
- `recovery_points.consistency` records how the point was read (`live_scan` today; `vss` is allowed by the schema for the future).

## Consequences

- Browsing a folder at a point is one indexed query per level; the cost depends on the folder size and not on the number of points.
- Storage grows with the amount of change, not with the number of points. A point without changes adds one `recovery_points` row.
- Names use the `C` collation so the exact bytes are kept; case-insensitive Windows behaviour is handled through `name_key`. The schema does not forbid two names that differ only by case in one folder.
- Retention is not implemented. Deleting an old point would mean merging ranges and finding unreferenced objects; it needs its own design.
- The 5-million-entry target is not proven; see [mvp-status.md](../mvp-status.md).

---

# 中文

## 状态

已接受，用于 MVP（2026-11-15）。对应 plan.md P0 中的"快照语义"和"数据模型"两项。

## 背景

对于可能含有数百万条目、并且有许多恢复点的备份源，TimeVault 必须能回答"这个文件夹在恢复点 N 时是什么样子？"以及"这个文件存在哪些版本？"。plan.md §6 给出了带有效区间的 `node_history` 表的设想。一个恢复点必须要么完全可见，要么完全不可见：被中断的备份绝不能暴露一个只构建了一半的状态。

考虑过的方案：

- **每个恢复点存一份完整清单。** 容易理解，但每个恢复点都要重新存储所有条目；条目达到数百万时，即使什么都没变也要花费行数和时间。
- **把清单存成数据库之外的文件。** 写入便宜，但不读取整个文件就无法浏览文件夹或列出某个文件的版本。
- **有效区间（已选）。** 为路径的每个状态存一行，并记录该状态成立的恢复点区间。

## 决定

- 每个备份源的恢复点编号为 `seq = 1, 2, 3 ...`（`recovery_points`）。恢复点的状态为 `building`、`committed`、`failed` 或 `aborted`；每个备份源同一时间只能有一个 `building` 状态的恢复点（部分唯一索引保证）。
- 每个路径是 `nodes` 中的一个槽位（父节点加精确名称，另有小写形式的 `name_key` 用于不区分大小写的排序和查找）。槽位经历的各个状态是 `node_versions` 中带有 `first_point` 和 `last_point` 的行；`last_point IS NULL` 表示"仍是当前状态"。当 `first_point <= s AND (last_point IS NULL OR last_point >= s)` 时，该版本在恢复点 `s` 可见。
- 备份只写入发生变化的内容。未变化的条目不产生任何开销；被修改的条目在上一个恢复点关闭旧版本并开启新版本；被删除的条目只关闭其版本，因此该路径在较早的恢复点仍然可见、可恢复。重命名或移动等于删除加新增；内容在仓库中只存一份，所以字节不会重复，但每个条目会产生新行。
- 一次构建用它自己的 `seq` 写入行。读取方只查询已提交的恢复点，因此提交之前构建写入的任何内容都不可见。在新的构建开始之前以及服务启动时，会在按备份源加的咨询锁保护下，清除失败或被中断的构建留下的行，并重新打开它关闭过的版本。
- 每个恢复点保存一个清单摘要（对每个条目按固定的深度优先顺序生成规范化文本行，再做 SHA-256）。它在扫描期间计算，校验任务会根据目录库重新计算，因此提交之后对目录库的改动能被发现。
- 无法读取的条目以 `state = 'skipped'` 的版本存储并附带原因；重新读取失败时保留之前的良好版本。目录的版本只用来表示它存在；目录永远不会被"修改"。
- `recovery_points.consistency` 记录恢复点是如何读取的（目前是 `live_scan`；数据库结构允许将来使用 `vss`）。

## 影响

- 浏览某个恢复点的文件夹，每一层只需一次带索引的查询；成本取决于文件夹的大小，而不取决于恢复点的数量。
- 存储随变化量增长，而不是随恢复点数量增长。没有变化的恢复点只增加一行 `recovery_points`。
- 名称使用 `C` 排序规则以保留精确字节；Windows 不区分大小写的行为通过 `name_key` 处理。数据库结构并不禁止同一文件夹中出现仅大小写不同的两个名称。
- 保留策略尚未实现。删除旧恢复点意味着合并区间并找出不再被引用的对象，需要单独设计。
- 500 万条目的目标尚未得到证明；参见 [mvp-status.md](../mvp-status.md)。
