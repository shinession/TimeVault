# English

## Status

Accepted for 0.1.4 (2026-11-16). Extends [0001](./0001-recovery-point-history-model.md) (the history model) and [0002](./0002-content-addressed-repository.md) (the repository). It settles the "retention and garbage collection" item that the ADR index listed as undecided.

## Context

- Every changed file adds a version to the catalog and an object to the repository, and nothing was ever deleted.
- The administrators want a maximum number of versions, with the oldest ones rolling off when it is exceeded.
- The question was asked and answered: the limit counts **versions of one file**, it is a setting of each monitored folder, a limit of 0 means "keep everything", and it is accepted that the oldest recovery points then no longer show every file.

## Decision

- **A setting per monitored folder: "Versions kept per file".** Stored in `sources.max_versions`, from 1 to 10000; empty (`NULL`) keeps everything, and in the form the value 0 means the same. It is set when a folder is added and on its edit form (API: `max_versions`).
- **What is kept.** For each file, the newest N versions that were captured (state `ok`) are kept, newest meaning the latest first recovery point. Everything of that file that is older than the oldest of those N is deleted, versions that were only recorded as "not captured" (skipped) included. Folders, the versions that are still open and the history of the newest recovery point are never touched. Only committed history counts: the leftovers of a backup that did not finish are neither counted nor deleted.
- **Recovery points that lose versions are marked `partial`** (`recovery_points.partial`), in the same transaction as the deletion; a point is never left without its mark. A partial point no longer shows the files whose versions were deleted, which is the accepted loss. The pages show a **Partial** chip with an explanation. **Verify** of a partial point still checks the objects, but skips the comparison with the manifest (`ManifestSkipped`), because the manifest lists files that are no longer there.
- **When it runs.**
  - Right after each backup that committed a recovery point, for the files that backup changed. It never fails the backup. It gives way to a restore or a verification of the folder that is queued or running instead of waiting for it; and when it cannot do its work, or fails, the next backup would not make up for it (it only looks at the files it changed itself), so a cleanup job for the whole folder is queued instead.
  - As a **version cleanup** job (`jobs.kind = 'prune'`, at most one active per folder) when a limit is set or set lower than before (no limit counts as the highest) on a folder that has history, and as the fallback above. It handles every file of the folder, in batches of 500 files per transaction, and can be repeated safely when it is interrupted. The job queue takes turns for it: a cleanup of a folder is not started while a backup, a restore or a verification of the folder runs, and none of them is started while the cleanup runs. A job that has to wait stays queued and holds no worker, and a queued job survives a restart of the service.
- **Lowering a limit needs confirmation** when it would delete versions. The Settings page says how many versions of how many files would go and asks for a tick (`confirm_prune`); without it the change is refused (HTTP 409 `prune_not_confirmed` in the API). The preview and the deletion share the same SQL, so they cannot disagree. While a deletion of the folder runs, its limit cannot change (`prune_running`): the run that is under way would go on with the old one.
- **Freeing the repository is a separate, global step.** After versions were deleted, an object that no version refers to any more is deleted: its file first, then its record, so an interruption leaves at most a record without a file that the next sweep removes. The sweep only runs while no backup runs (a gate in the backup engine: a backup holds the shared side, the sweep takes the exclusive side with a try-lock and never makes a backup wait), within a budget of 2 minutes per sweep. When it cannot run or cannot finish, the scheduler repeats it on its next tick. The service remembers that a sweep is pending only in memory.
- **A restore or a verification of a folder is refused** (`prune_running`, HTTP 409) while its older versions are being deleted, so that nobody starts on versions that are about to go. A cleanup that is only queued does not count, because it can wait for hours behind a long verification. One that was queued before the deletion began waits for it to end.
- **Audit.** `retention.prune` ("Old versions deleted") with the limit, the number of files and versions, the recovery points marked partial, the objects and bytes freed and the duration. It is written when something was deleted, and always for a cleanup job. Putting a cleanup off because the folder is busy is not a failure and is not audited.

## Alternatives considered

- **Keep the newest N recovery points.** Simple, and every kept point stays complete; but the history then reaches back only N backups for every file, however often it changed, so a file that changes once a month keeps almost none of its past.
- **Count references on objects and delete when the count reaches 0.** One more number to keep right in every transaction; the orphan sweep needs nothing but "no version refers to it" and also repairs the leftovers of an interrupted backup.
- **Delete objects inside the backup.** Makes the backup slower and puts deletion in the path that must never fail.
- **Move deleted versions to a trash first.** Not asked for; the confirmation is the safety net.
- **Let the cleanup job wait inside its worker for the folder.** What the first version did. It occupied one of the few workers for up to half an hour, and while it held the writer lock of the folder a backup of that folder failed and, if it was an automatic one, lost a whole interval.

## Consequences

- Deleted versions are gone for good. Older recovery points lose files, so a restore from them can be incomplete; the pages say so with the **Partial** chip.
- The limit is per file, not per recovery point: how far back the history reaches differs from file to file.
- Space comes back only after a sweep, which waits for a time when no backup runs. On a server that is always backing something up, space can come back late. A restart of the service forgets that a sweep is pending; the next deletion of old versions sweeps everything again.
- A very long cleanup of a large folder delays the backups of that folder (they wait in the queue), and restores and verifications are refused while it runs.
- Lowering a limit queues the cleanup at once; until that job has run, the folder still holds the old versions. A cleanup that has to wait for a long backup or verification waits just as long.

---

# 中文

## 状态

于 2026-11-16 在 0.1.4 中被接受。扩展 [0001](./0001-recovery-point-history-model.md)（历史记录模型）和 [0002](./0002-content-addressed-repository.md)（仓库）。它解决了 ADR 索引中列为"尚未决定"的"保留策略与垃圾回收"。

## 背景

- 每个变更的文件都会给目录库增加一个版本，给仓库增加一个对象，而此前从不删除任何内容。
- 管理员希望设置最大版本数，超过后滚动删除最旧的版本。
- 这个问题已经问过并得到答复：限制统计的是**单个文件的版本数**，它是每个监控文件夹的设置，限制为 0 表示"全部保留"，并且接受"最旧的恢复点之后不再显示所有文件"。

## 决定

- **每个监控文件夹一个设置："每个文件保留的版本数"。** 保存在 `sources.max_versions`，范围 1 到 10000；为空（`NULL`）表示全部保留，表单中的值 0 表示同样的意思。添加文件夹时和编辑表单中都可以设置（API：`max_versions`）。
- **保留什么。** 对每个文件，保留最新的 N 个已被捕获（状态 `ok`）的版本，"最新"指首次出现的恢复点最晚。该文件中比这 N 个版本里最旧的那个更早的内容全部删除，其中包括只被记录为"未捕获"（skipped）的版本。文件夹、仍然开放的版本以及最新恢复点的历史从不触及。只有已提交的历史才计入：没有完成的备份留下的内容既不计数也不删除。
- **失去版本的恢复点会被标记为 `partial`**（`recovery_points.partial`），与删除在同一个事务中完成；恢复点不会没有标记。不完整的恢复点不再显示其版本已被删除的文件，这是接受的损失。页面显示带说明的**不完整**标签。对不完整恢复点的**校验**仍然检查对象，但跳过与清单的比较（`ManifestSkipped`），因为清单列出的文件已经不在了。
- **何时运行。**
  - 在每次提交了恢复点的备份之后立即运行，针对这次备份改动过的文件。它绝不会让备份失败。它不会等待排队或正在运行的恢复或校验，而是给它们让路；当它无法完成工作或失败时，下一次备份也不会补上（下一次备份只查看它自己改动过的文件），所以会改为排入一个针对整个文件夹的清理任务。
  - 当在已有历史的文件夹上设置了限制，或把限制调得比以前更低时（没有限制视为最高），以及作为上面的后备方案，作为**版本清理**任务（`jobs.kind = 'prune'`，每个文件夹同时最多一个活动任务）运行。它处理该文件夹的每个文件，每个事务 500 个文件，被中断后可以安全地重复。任务队列会让它轮流执行：该文件夹的备份、恢复或校验正在运行时不会启动它的清理，清理运行时也不会启动它们中的任何一个。需要等待的任务保持排队状态，不占用任何 worker，已排队的任务在服务重启后依然存在。
- **调低限制需要确认**，只要它会删除版本。设置页面会说明将删除多少文件的多少个版本，并要求勾选（`confirm_prune`）；没有勾选则拒绝这次更改（API 返回 HTTP 409 `prune_not_confirmed`）。预览和删除使用同一段 SQL，所以不会不一致。文件夹的删除正在运行时，它的限制不能更改（`prune_running`）：正在进行的那一次会继续按旧的限制执行。
- **释放仓库空间是单独的全局步骤。** 版本被删除后，不再被任何版本引用的对象会被删除：先删文件，再删记录，所以中断最多留下一条没有文件的记录，下一次清扫会把它删掉。清扫只在没有备份运行时进行（备份引擎中的一道闸门：备份持有共享侧，清扫以尝试加锁的方式取得独占侧，绝不让备份等待），每次清扫的预算为 2 分钟。当它不能运行或不能完成时，调度器在下一个周期重复它。服务只在内存中记住有一次清扫待办。
- **正在删除某个文件夹的旧版本时，该文件夹的恢复或校验会被拒绝**（`prune_running`，HTTP 409），这样没有人会在即将消失的版本上开始操作。只是排在队列里的清理不算，因为它可能在一个很长的校验后面等上好几个小时。在删除开始之前就已排队的恢复或校验，会等到删除结束。
- **审计。** `retention.prune`（"已删除旧版本"），包含限制、文件数和版本数、被标记为不完整的恢复点、释放的对象和字节数以及耗时。有内容被删除时会写入，清理任务则总是写入。因为文件夹忙而推迟清理不算失败，也不会被审计。

## 考虑过的备选方案

- **保留最新的 N 个恢复点。** 简单，保留下来的每个恢复点都是完整的；但这样历史对每个文件都只能追溯 N 次备份，无论它变化得多频繁，所以一个每月才变化一次的文件几乎保不住它的过去。
- **给对象计数引用，计数为 0 时删除。** 每个事务都要多维护一个数字；孤立对象清扫只需要"没有版本引用它"，还能修复被中断的备份留下的残余。
- **在备份过程中删除对象。** 会让备份变慢，并把删除放进绝不能失败的路径。
- **先把删除的版本移到回收站。** 没有被要求；确认步骤就是安全网。
- **让清理任务在它的 worker 里等待文件夹空闲。** 这是第一版的做法。它会占用为数不多的 worker 之一长达半小时，而且在它持有该文件夹的写入锁期间，该文件夹的备份会失败，如果是自动备份，还会损失整整一个间隔。

## 影响

- 被删除的版本永远消失。较旧的恢复点会丢失文件，所以从它们恢复可能不完整；页面用**不完整**标签说明这一点。
- 限制是按文件而不是按恢复点的：历史能追溯多远因文件而异。
- 空间只有在清扫之后才会回来，而清扫要等到没有备份运行的时候。在总有备份在运行的服务器上，空间可能回来得很晚。服务重启后会忘记有一次清扫待办；下一次删除旧版本时会再次清扫全部内容。
- 对大文件夹很长时间的清理会延迟该文件夹的备份（它们在队列中等待），清理期间恢复和校验会被拒绝。
- 调低限制会立即排入清理任务；在该任务运行之前，文件夹仍然保留着旧版本。需要等待一个很长的备份或校验的清理，会等同样长的时间。
