# English

## Status

Accepted for 0.1.4 (2026-11-16). Extends the scheduling of [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md); the schedule of a folder is still "every N minutes, counted from the end of the previous run, and nothing is recorded when nothing changed".

## Context

- A server can have around 10 monitored folders. In 0.1.3 every folder whose interval had passed was queued at the next tick, so folders with the same interval (60 minutes, say) were all scanned at the same moment, one after the other, and the file shares of the same computer felt slow.
- The folders differ: some are large and take minutes to scan, some are small; some change often, most rarely.
- Everything (see [0007](./0007-display-folders-and-hiding-server-paths.md) and the search engine of 0.1.3) already indexes the volumes and can tell cheaply whether a folder changed.

## Decision

- **Automatic backups run one at a time and are spread out.** `TIMEVAULT_SCHEDULER_PARALLEL` (default 1, from 1 to 4) is how many may be queued or running. A backup that someone starts by hand is not limited and does not count. The scheduler looks every 15 seconds.
- **Gap between starts.** `TIMEVAULT_SCHEDULER_GAP`: `auto` (the default) is `1 / Σ(1 / interval)` over the scheduled folders, the gap that just keeps up with all of them (10 folders of 60 minutes: one start every 6 minutes), within 30 seconds and 10 minutes. A duration such as `5m` sets it; `0s` turns the spacing off.
- **Rest after a run.** `TIMEVAULT_SCHEDULER_REST`: `auto` (the default) is as long as the run took, within 30 seconds and 10 minutes, so a folder that was scanned for ten minutes is followed by a pause and the server is not busy all day. A duration sets it; `0s` turns it off. It applies only while `PARALLEL` is 1. The next start is at the later of the gap after the previous start and the rest after the previous end.
- **Order when several folders are due:** folders that have no committed recovery point yet; then the one most overdue in proportion to its own interval (a short interval is not starved by a long one); then the one whose last run was the quickest (small folders do not wait behind a big one); then the lowest id. The time a run took is kept per folder (`sources.last_run_ms`) and shown on the Settings page.
- **The interval is counted from the end of every run,** also a run that found nothing to do. A run that finds nothing changed still leaves neither a job nor an audit record.
- **Everything as a change detector (off by default).** With `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE=true`, a folder that is due is not scanned when Everything says nothing changed. The probe (about 0.1 s) asks for the number of entries below the folder and whether anything was created, modified, renamed, moved, or had its attributes changed since the last complete scan, less 5 minutes. The scan is skipped only when all of these hold: Everything knows the folder; it answers within 3 seconds; the number of entries is the number recorded right before the last complete scan (a deletion only shows as a lower number); nothing changed; the last recovery point has no item that was not captured; and the last complete scan is younger than `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY` (default 24 hours, from 1 hour to 720 hours). In every other case the folder is scanned as before. A skipped folder restarts its interval. A probe is not paced. A tick asks Everything at most 25 times, and when Everything does not answer it is left alone for a minute (a folder that Everything does not know says nothing about an outage), so a stopped Everything costs one timeout of 3 seconds and not one for every due folder. Changing the exclude patterns of a folder makes its next run scan it, because what a scan has to capture changed without any file changing. The facts are kept in `sources.last_walk_at` and `sources.last_walk_items`.
- **Process priority.** `TIMEVAULT_PROCESS_PRIORITY`: `below_normal` (the default), `normal`, or `background` (which also lowers the disk priority). A backup scan then yields to the file shares of the same server.
- **The pace is in memory.** After a restart the first automatic backup may start at once.

## Alternatives considered

- **A fixed offset per folder or random jitter.** Does not adapt to how long runs take or to folders being added.
- **Only a fixed gap, as in the example of 6 minutes.** A fixed gap cannot know that one folder takes ten times longer than the others; the rest after a run does.
- **Slow down when the computer is busy (CPU or disk load).** Needs a measurement and a policy that behave differently on every machine; the pace plus a low priority gives the same relief with something that can be explained.
- **Back off folders that never change.** Not chosen: the probe makes an unchanged folder nearly free, and a longer interval would delay the first backup after a change.
- **Read the NTFS change journal (USN) ourselves.** A deliberate non-goal of [0003](./0003-live-scan-without-vss-and-usn.md); Everything already reads it.

## Consequences

- With one automatic backup at a time, one very long backup delays the others; the order above keeps the others from starving.
- If the folders' intervals are shorter than the scheduler can serve (the gap is at least 30 seconds), they run as often as the pace allows and the real interval is longer than the setting.
- The probe trusts Everything's index. If Everything is stopped or behind, a change can go unseen until the next scan that is not skipped: at most `FULL_SCAN_EVERY` later, or at once with **Back up now**. Everything must monitor the volume of the folder and keep "Index recently changed date" on; a folder on a volume it does not index is simply scanned.
- The first scan of a folder still takes as long as before.

---

# 中文

## 状态

于 2026-11-16 在 0.1.4 中被接受。扩展 [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md) 的计划任务；文件夹的计划仍然是"每 N 分钟，从上一次运行结束时开始计算，没有变化时不记录任何内容"。

## 背景

- 一台服务器可能有大约 10 个监控文件夹。在 0.1.3 中，间隔已过的每个文件夹都会在下一个周期被排入队列，所以间隔相同（比如 60 分钟）的文件夹会在同一时刻一个接一个地被扫描，同一台电脑上的文件共享就会变慢。
- 文件夹各不相同：有的很大，扫描要几分钟，有的很小；有的经常变化，大多数很少变化。
- Everything（见 [0007](./0007-display-folders-and-hiding-server-paths.md) 和 0.1.3 的搜索引擎）已经为各卷建立了索引，可以很低成本地判断一个文件夹是否有变化。

## 决定

- **自动备份一次只运行一个，并且错开时间。** `TIMEVAULT_SCHEDULER_PARALLEL`（默认 1，范围 1 到 4）是允许排队或运行的数量。有人手动启动的备份不受限制，也不计入。调度器每 15 秒检查一次。
- **两次启动之间的间隔。** `TIMEVAULT_SCHEDULER_GAP`：`auto`（默认）是对各计划文件夹计算 `1 / Σ(1 / 间隔)`，即刚好跟得上所有文件夹的间隔（10 个文件夹、每个 60 分钟：每 6 分钟启动一个），限制在 30 秒到 10 分钟之内。可以用 `5m` 这样的时长设置；`0s` 关闭间隔。
- **运行后的休息。** `TIMEVAULT_SCHEDULER_REST`：`auto`（默认）等于这次运行所花的时间，限制在 30 秒到 10 分钟之内，所以扫描了十分钟的文件夹之后会有一段停顿，服务器不会一整天都忙着。可以用时长设置；`0s` 关闭。只在 `PARALLEL` 为 1 时生效。下一次启动时间取"上次启动之后的间隔"和"上次结束之后的休息"中较晚的那个。
- **有多个文件夹到期时的顺序：** 还没有已提交恢复点的文件夹；然后是相对于自己的间隔超期最多的（短间隔不会被长间隔饿死）；然后是上次运行最快的（小文件夹不必排在大文件夹后面）；最后是 id 最小的。每个文件夹所花的时间会保存下来（`sources.last_run_ms`），并显示在设置页面上。
- **间隔从每次运行结束时开始计算，** 没有发现变化的运行也一样。没有变化的运行仍然既不留下任务，也不留下审计记录。
- **把 Everything 用作变化探测器（默认关闭）。** 设置 `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE=true` 后，到期的文件夹如果 Everything 说没有变化，就不会被扫描。探测（约 0.1 秒）询问文件夹之下的条目数量，以及自上次完整扫描前 5 分钟以来是否有任何内容被创建、修改、重命名、移动或更改了属性。只有同时满足以下所有条件才会跳过扫描：Everything 认识该文件夹；3 秒内有响应；条目数量等于上次完整扫描之前记录的数量（删除只表现为数量变少）；没有任何变化；最新的恢复点没有未捕获的项目；并且上次完整扫描比 `TIMEVAULT_SCHEDULER_FULL_SCAN_EVERY`（默认 24 小时，范围 1 小时到 720 小时）更新。其他任何情况都和以前一样扫描该文件夹。被跳过的文件夹会重新开始它的间隔。探测不受间隔限制。一个周期最多询问 Everything 25 次，当 Everything 没有应答时，一分钟内不会再询问它（Everything 不认识某个文件夹并不说明它出了故障），所以已停止的 Everything 只造成一次 3 秒的超时，而不是每个到期文件夹各一次。更改文件夹的排除规则会让它的下一次运行进行扫描，因为扫描必须捕获的内容变了，而没有任何文件发生变化。相关信息保存在 `sources.last_walk_at` 和 `sources.last_walk_items`。
- **进程优先级。** `TIMEVAULT_PROCESS_PRIORITY`：`below_normal`（默认）、`normal` 或 `background`（同时降低磁盘优先级）。这样备份扫描会让位于同一台服务器上的文件共享。
- **间隔状态保存在内存中。** 重启后，第一个自动备份可能立即启动。

## 考虑过的备选方案

- **每个文件夹固定偏移或随机抖动。** 无法适应运行所花的时间，也无法适应文件夹的增减。
- **只用固定间隔，比如示例中的 6 分钟。** 固定间隔不知道某个文件夹比其他文件夹慢十倍；运行后的休息知道。
- **电脑繁忙（CPU 或磁盘负载高）时放慢。** 需要测量和策略，在每台机器上表现都不同；间隔加低优先级可以用能解释清楚的方式达到同样的缓解效果。
- **对从不变化的文件夹退避。** 没有选择：探测让没有变化的文件夹几乎没有成本，而更长的间隔会延迟变化之后的第一次备份。
- **自己读取 NTFS 变更日志（USN）。** 这是 [0003](./0003-live-scan-without-vss-and-usn.md) 有意不做的事；Everything 已经在读取它。

## 影响

- 自动备份一次只运行一个，所以一个很长的备份会延迟其他备份；上面的顺序避免其他文件夹被饿死。
- 如果文件夹的间隔比调度器能服务的更短（间隔至少 30 秒），它们只会按间隔允许的速度运行，实际间隔比设置更长。
- 探测信任 Everything 的索引。如果 Everything 停止或落后，变化可能在下一次没有被跳过的扫描之前不被发现：最晚在 `FULL_SCAN_EVERY` 之后，或者用**立即备份**马上发现。Everything 必须监控该文件夹所在的卷，并保持开启"索引最近更改日期"；在它不索引的卷上，文件夹只会照常被扫描。
- 文件夹的第一次扫描仍然和以前一样久。
