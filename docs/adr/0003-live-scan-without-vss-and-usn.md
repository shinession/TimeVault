# English

## Status

Accepted for the MVP (2026-11-15). This deliberately **deviates from plan.md** §4–§5, which use VSS for consistent reads and the NTFS USN journal for change detection. It replaces the "snapshot semantics" spike of P0 for now; the spikes remain to be done.

**Update, 1.0.1.** The NTFS change journal is now read, by the index that serves search, folder sizes and the scheduler's change probe ([0018](./0018-native-ntfs-index-replaces-everything.md)). Backups are unchanged: they still walk the source, and use neither VSS nor the journal. The record is left as it was written.

## Context

VSS and USN both need elevated privileges, a test volume, and a study of their error paths (writer failures, journal wrap and reset) before they can be trusted. The first goal of the MVP was a complete vertical slice (browse, back up, keep history, restore) with correct semantics, and the project owner chose to build that slice first and leave VSS and USN out.

## Decision

- Backups read the **live file system**. Every backup walks the whole source, one folder at a time, through a root handle opened on the source (`os.Root`), so reads cannot leave the source folder. Junctions, symbolic links and other reparse points are not followed; they are recorded as skipped. A file or folder that is replaced by a link after its folder was listed is detected (the path is checked again after the read, and before a folder is entered) and skipped instead of recording another file's content. Paths deeper than 256 folders are recorded as skipped, because browsing and restoring accept at most that depth.
- A file is considered unchanged when its size and modification time equal those of its last recorded version. The **Full re-read** option reads and hashes every file again.
- A file is copied into the repository while it is hashed, and its size and modification time are compared before and after the read. If they differ the read is repeated, up to 3 attempts, then the file is recorded as skipped with the reason "changed while it was being backed up". A file that cannot be opened (locked, no permission) is recorded as skipped with the reason, and the previous good version is kept.
- Each recovery point is marked `consistency = live_scan`. The UI shows "Live scan" and the Status page lists VSS and USN as disabled. A recovery point means "the state observed during one scan", never "the state at one instant"; the timeline snaps to real, committed recovery points, as plan.md §1 requires.

## Consequences

- Files that belong together can come from slightly different moments, and a file that is open for writing but not locked, and whose size and time do not change during the read, can be captured in a state that never existed. This gap is real; VSS closes it.
- A change that keeps both size and modification time identical is not seen by a normal backup; **Full re-read** finds it.
- Large trees are slow because every backup lists every folder; a change journal would avoid that. The cost has not been measured at scale.
- No journal means no journal-wrap handling is needed, and no change can be silently missed because of a lost journal range.
- The `consistency` column already allows `vss`, and the engine works on a root path, so a later VSS implementation can read from a snapshot path. This is a direction, not a finished design.

## Next steps

1. Run the P0 spikes: VSS create/read/clean up with a locked file, and USN checkpoint reading with journal-id reset detection, on a test NTFS volume.
2. Record the results as new ADRs, then decide whether the privileged part runs in a separate worker (see [0004](./0004-restore-safety-and-privileges.md)).

---

# 中文

## 状态

已接受，用于 MVP（2026-11-15）。这是有意对 plan.md §4–§5 的**偏离**：plan.md 使用 VSS 做一致性读取，使用 NTFS USN journal 做变更检测。它暂时取代 P0 中的"快照语义"验证；这些验证仍有待完成。

**更新，1.0.1 版。** 现在已经会读取 NTFS 变更日志了，由为搜索、文件夹大小和调度器的变更探测服务的索引来读（[0018](./0018-native-ntfs-index-replaces-everything.md)）。备份没有变：它们仍然遍历备份源，既不使用 VSS，也不使用 journal。本记录保持原样。

## 背景

VSS 和 USN 都需要提升权限、一个测试卷，以及对其错误路径（写入器失败、journal 回卷与重置）的研究，之后才能被信任。MVP 的首要目标是一个完整的垂直切片（浏览、备份、保留历史、恢复），并保证语义正确；项目负责人选择先构建这个切片，暂不做 VSS 和 USN。

## 决定

- 备份读取**在线文件系统**。每次备份都遍历整个备份源，一次一个文件夹，并通过在备份源上打开的根句柄（`os.Root`）读取，所以读取不会越出备份源文件夹。联接点、符号链接和其他重解析点不会被跟随；它们会被记录为"跳过"。如果文件或文件夹在其所在文件夹被列出之后被替换成链接，会被检测到（读取之后以及进入文件夹之前会再次检查该路径）并跳过，而不会记录另一个文件的内容。深度超过 256 层文件夹的路径会被记录为"跳过"，因为浏览和恢复最多接受这个深度。
- 当文件的大小和修改时间与其最近记录的版本相同时，视为未变化。**完整重读**选项会重新读取并哈希每个文件。
- 文件在被哈希的同时复制进仓库，并在读取前后比较其大小和修改时间。如果不同，则重复读取，最多 3 次，之后该文件被记录为"跳过"，原因是"changed while it was being backed up"。无法打开的文件（被锁定、没有权限）会被记录为"跳过"并附带原因，同时保留之前的良好版本。
- 每个恢复点都标记为 `consistency = live_scan`。界面显示"实时扫描"，状态页把 VSS 和 USN 列为已禁用。恢复点的含义是"一次扫描期间观察到的状态"，而不是"某一瞬间的状态"；时间轴只会吸附到真实、已提交的恢复点，这与 plan.md §1 的要求一致。

## 影响

- 属于同一组的文件可能来自略有不同的时刻；一个正被写入但没有被锁定、且读取期间大小和时间没有变化的文件，可能被采集到一个从未存在过的状态。这个缺口是真实存在的；VSS 可以弥补它。
- 大小和修改时间都保持不变的改动不会被普通备份发现；**完整重读**可以发现。
- 大型目录树会比较慢，因为每次备份都要列出每个文件夹；变更日志可以避免这一点。这一成本尚未在大规模数据上测量。
- 没有 journal，就不需要处理 journal 回卷，也不会因为丢失 journal 区间而悄悄漏掉变化。
- `consistency` 列已经允许取值 `vss`，而且引擎基于根路径工作，所以以后的 VSS 实现可以从快照路径读取。这是一个方向，而不是已完成的设计。

## 后续步骤

1. 做 P0 验证：在测试用 NTFS 卷上验证带有被锁定文件的 VSS 创建/读取/清理，以及带有 journal id 重置检测的 USN 检查点读取。
2. 把结果记录为新的 ADR，然后决定特权部分是否放在独立的 worker 中（见 [0004](./0004-restore-safety-and-privileges.md)）。
