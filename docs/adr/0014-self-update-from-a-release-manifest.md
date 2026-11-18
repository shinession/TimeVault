# English

## Status

Accepted for 0.1.8 (2026-11-18). Adds a way to deploy and to start the program; the routes, the accounts, the permissions and the data are unchanged. Relies on [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md) for where settings live and on [0010](./0010-version-limit-and-garbage-collection.md) and [0011](./0011-staggered-scheduler-and-change-probe.md) for how jobs and the scheduler behave.

## Context

- Up to 0.1.7 a new version was deployed by hand: build `timevault-server.exe`, stop the service, copy the file to the server, start it. There was no continuous delivery, and nobody said when a copy had gone wrong (a half-copied file, an old number, the wrong file).
- The wish for 0.1.8: the build writes a **release folder** with the program and a small `timevault.json` (`latest_version`, `sha256`, `package_url`, `size`); the service **reads that file** from an address in `.env` at an interval; when `latest_version` is not the running version, it **waits until the running backup has finished and then restarts itself**, without anybody being at the server.
- The service is started by hand, in a console window or by a scheduled task, not by a service manager. Nothing restarts it when it ends.
- On Windows a program file that runs **cannot be overwritten or deleted, but it can be renamed**, and a new file can be put at the old name (tested on this computer, and by `TestSwapReplacesAProgramThatIsRunning`). So a new program can be put in place while the old one still runs, and the swap needs no gap in which there is no program.
- A scheduled task that starts the service ends the processes that the service started when the task ends. A program that starts its successor and exits would lose it.
- Backups can run for a long time (a large folder is scanned for minutes), and a restart in the middle of one throws its work away (it is recovered as a failed job and the scan starts again).
- Everybody who can write the folder that the service reads decides which program runs on the server. That is the one trust decision of this feature.

## Decision

- **A release is a folder with two files, written by `scripts/release.ps1`:** `timevault.exe` and `timevault.json`. The script runs `go vet`, builds into a temporary name, asks the program for its version (the number in `cmd/timevault-server/main.go`), hashes and measures it, puts the program in place and writes the manifest **last**, atomically, as UTF-8 without a byte order mark, with exactly the four fields. `package_url` is the file name by default (the service finds it next to the manifest it read), or the full address with `-BaseUrl`. It warns when a release has the version of the one before it but another program, because no service would update to it.
- **Three settings.** `TIMEVAULT_UPDATE_URL` is the address of the manifest or of its folder: an `http(s)` address, or the absolute path of a folder or a share; it may not hold a user name, a password, a query or a fragment, and it is empty by default, which means the service never updates itself. `TIMEVAULT_UPDATE_INTERVAL` is how often it is read (10 minutes by default; 1 minute to 24 hours). `TIMEVAULT_UPDATE_RESTART` is `supervise` (default) or `exit`. A service that has an update address **must have `TIMEVAULT_AUTO_MIGRATE=true`** and refuses to start otherwise: a new version may change the database, and nobody runs `migrate` when the service updates itself.
- **The decision is plain.** The service updates when `latest_version` is **not** the version it runs, in either direction (a leading `v` is ignored), so a manifest that goes back to an older version is a downgrade, and a manifest that goes back to the running version cancels an update that was waiting. There is no ordering of versions to get wrong.
- **What `internal/update` does, in this order,** from the first look (about 10 seconds after the start) on, at every interval and when an administrator asks for a look (**Check now** on the Settings page, `POST /api/v1/update/check`):
  1. Reads the manifest and checks it strictly (at most 64 KiB; a version of letters, digits and `. _ + -`; 64 hexadecimal characters of hash; a size from 1 byte to 512 MiB; unknown fields are ignored so that a later script can add some). A manifest that cannot be read is an error that is shown and tried again at the next look; the service goes on as it is.
  2. Downloads the program to `timevault.new.exe` next to the running program, through a `.part` file, and keeps it only when its size and its sha256 are the manifest's. A mismatch is not final: it is what a release that is still being copied looks like, so it is tried again at the next look. A download of an `http(s)` address that receives no data for two minutes is given up (an updater that waits for a server for good would never look again), and a program that has gone from the folder since it was downloaded (a virus scanner) is downloaded again.
  3. Runs the downloaded program: `version` must print the version of the manifest (a program of another version is not tried again until the manifest changes, so that a wrong release cannot loop), and `check-config` must accept the settings of this service (the same environment and `.env`; it connects to nothing). A refusal is shown and tried again at every look, because an administrator may fix the `.env`.
  4. **Drains the job runner** (`jobs.Runner.Drain`): from now on no job is taken from the queue. The jobs that run go on until they are done; backups that are asked for meanwhile, by people or by the scheduler, are queued and wait. The Settings page says "waiting for the running jobs", and a notice under the header tells signed-in accounts that a version will be installed and that new jobs wait.
  5. When no job runs any more, **puts the program in place**: the running `timevault.exe` is renamed to `timevault.old-<version>.exe`, the new one is renamed to `timevault.exe`; when that fails the old one gets its name back, the jobs are taken again and the release is marked as not to be tried again until the manifest changes. A note, `timevault.exe.update.json`, is written next to it.
  6. Stops the service the usual way (the scheduler, the HTTP server, the job runner), which releases the database lock, and starts the new program.
  - Waiting has no time limit (a backup is worth its time), and nothing is deleted from the queue.
- **How the new program is started.**
  - `supervise` (default): the process that was started stays alive as a **small launcher** (`update.Supervise`) and runs the new program as its child, with the same arguments, environment, working folder and console. The launcher takes Ctrl+C and the termination signal without dying (the child gets them too and stops itself), ends with the child's exit code, and starts the next program when the child ends with the exit code **75**, which is what a later update does. The standard input of the child is a pipe that only the launcher holds and never writes to: when the launcher ends in any way, even when it is killed (which is what the Task Scheduler does to a task that is ended), the child reads the end of its input and shuts down in order (`update.StopWithLauncher`), so no program is left behind with nobody to start the next one, holding the port and the database lock. A console window and a scheduled task keep seeing one running program. The environment of the child holds `TIMEVAULT_SUPERVISED_BY` (the launcher's process id), which is how it knows to exit with 75 instead of starting a launcher of its own.
  - `exit`: the process ends with the exit code 75 and whatever starts programs again starts the new one: a service manager (NSSM), or a scheduled task whose trigger repeats every minute with "Do not start a new instance". The Task Scheduler setting "If the task fails, restart" does not restart a program that ended with 75 (tried), so it is not a way to do this.
- **The new program says that it took over.** It reads and deletes the note, writes `automatic update: this program has taken over` to the log and an audit event with `stage` `done` (nothing is written when the note names another version).
- **Audit.** One action, `app.update`, by the actor `updater`, with the stage in the detail (`staged`, `restart`, `done`, `cancelled`, `failed`), the versions, the hash and, for a failure, the step that failed. The detail has **no path and no error text**: the audit log is shown to everybody who signs in. The log of the service has the full text. The actor has no picture and the action is in the audit filter.
- **What who sees.** Everybody who is signed in sees the state, the running and the released versions and when the manifest was last read, and the notice. Only administrators see the address of the release and the last error (they may quote a share or a folder of the server, like every real path of the server, see [0007](./0007-display-folders-and-hiding-server-paths.md)), and only they can ask for a look. The API has the same split.
- **No signature, no automatic way back.** The size and the hash protect against a half-copied or damaged file, not against somebody who can write the release folder, who can write both. The old program is kept (`timevault.old-<version>.exe`, the newest three) for a way back **by hand**; the runbook says how, and what a migration does to it.

## Alternatives considered

- **Start the new program and exit.** The simplest, and wrong here: a scheduled task ends the processes that the service started when the task ends, and a console window shows the old program as ended. It is still what `exit` leaves to a service manager.
- **A separate updater program or service.** Another file to deploy and another thing to run, for a program that is one file. The launcher is the small part of this that cannot be avoided, and it is the program itself.
- **Replace the file only at start.** Needs something that starts the program in the first place; with that, the update could be a step of it. Not for a service that is started by hand.
- **Overwrite the file in place.** Windows refuses for a running program. The rename lets the file be replaced while the old program runs and leaves the old one for a way back.
- **Ordered versions** (update only to a larger version). Needs a version scheme and a comparison, and makes a rollback by changing the manifest impossible. Equality is enough and is what a rollback needs.
- **Restart at once, resume the job afterwards.** Jobs are not resumable: a restart throws a backup's work away. Waiting for the end costs time on the server, not data.
- **A signature** (Authenticode or a public key in the program). Does what the hash cannot, at the price of keys to keep and to rotate; not asked for, and the release folder is already an administrators' place. It can be added to the manifest without changing the rest.
- **A time limit for the wait.** Would end a long backup that has run for hours. If somebody needs the restart sooner, stopping the service by hand is what it always was.
- **Automatic way back when the new program does not start.** Cannot tell a bad program from a database that is down, and a migration that the new program made is not undone by the old program (0.1.7 and 0.1.6 cannot be run on the same database, see the upgrade notes). A wrong automatic decision would be worse than a stopped service that says why.

## Consequences

- **The first update is the last manual one.** Versions before 0.1.8 cannot update themselves; 0.1.8 is copied to the server once, with `TIMEVAULT_UPDATE_URL` and `TIMEVAULT_AUTO_MIGRATE=true` in the `.env`, and updates itself from then on.
- **Rights.** The account that runs the service must be able to write the folder of the program (the staged file, the old copy and the note go there) and to read the release folder. The service says so in its log when it starts and the folder cannot take a new file; an update without the right fails at the swap and the service goes on with the old program.
- **The trust.** Whoever can write the release folder, or sit on the network path to it, decides what runs on the server: use a folder that only administrators can write, and `https` for an address. [security.md](../security.md) says so.
- **The launcher stays the old program** while it lives, so the old file stays locked, and at most three older copies are kept (the ones that cannot be deleted stay until the launcher ends). Its own code does not change with an update; a change to the launcher needs a restart of the launcher.
- **While a release waits, the queue grows** and nothing starts, also the scheduler's backups. A restart is as late as the longest running job.
- **A new program that is accepted by the checks and still fails later** (a migration that fails, a folder that is gone) leaves the service down; the launcher ends with the exit code of the program, and the log of the service has the reason. Going back is by hand.
- **`check-config` checks the settings**, not the world. It does not connect to the database, Redis or the search engine.
- **Tests.** The state machine of the update (same version, staging, draining, waiting, restart, cancel, a changed release, wrong size, wrong hash, wrong version, refused settings, a failed swap, an unreachable manifest, audit without paths), the files (download, swap, rename of a program that runs, prune, note), the sources (http and file, relative and absolute package address, redirects, a `202`), the launcher, the settings and the pages and the API are covered by automated tests. See [mvp-status.md](../mvp-status.md) for what was run with the compiled program.
- **Checked with the compiled programs** (2026-11-18, one Windows computer, no administrator rights): a release folder and a web address; a wrong hash, size or version and an unreadable manifest, which are refused; an update that waits for a running backup while a job requested meanwhile stays queued; the swap; the launcher and its child, with Ctrl+C in the console stopping both; the mode `exit` with the code 75; and the Windows Task Scheduler, with a task of the current user: in `supervise` mode the task keeps running through an update, and ending the task ends the launcher and the program (this found a program that was left behind, which led to the input pipe described above); in `exit` mode a task whose trigger repeats every minute starts the new program after the exit with 75, while the setting "If the task fails, restart" did not. Not checked: NSSM, a Windows service, a network share, a program of hundreds of megabytes, the company's servers.

---

# 中文

## 状态

于 2026-11-18 在 0.1.8 中被接受。增加了一种部署和启动程序的方式；路由、账号、权限和数据保持不变。设置放在哪里依赖 [0006](./0006-settings-in-env-and-database-and-scheduled-backups.md)，任务和调度器的行为依赖 [0010](./0010-version-limit-and-garbage-collection.md) 和 [0011](./0011-staggered-scheduler-and-change-probe.md)。

## 背景

- 直到 0.1.7，新版本都是手工部署的：构建 `timevault-server.exe`，停止服务，把文件复制到服务器，再启动。没有持续交付，复制出了问题（文件只复制了一半、版本号是旧的、文件复制错了）也没有人会告诉你。
- 0.1.8 的需求：构建时生成一个**发布文件夹**，里面有程序和一个小的 `timevault.json`（`latest_version`、`sha256`、`package_url`、`size`）；服务按 `.env` 中的地址和间隔**读取这个文件**；当 `latest_version` 不是正在运行的版本时，它会**等正在运行的备份结束后再自己重启**，不需要任何人守在服务器旁。
- 服务是手工启动的，在控制台窗口中或由计划任务启动，而不是由服务管理器启动。它结束之后没有任何东西会重新启动它。
- 在 Windows 上，正在运行的程序文件**不能被覆盖或删除，但可以被重命名**，并且可以在原来的文件名上放一个新文件（已在这台计算机上试过，并由 `TestSwapReplacesAProgramThatIsRunning` 覆盖）。所以新程序可以在旧程序仍在运行时就位，交换的过程中不会有"没有程序"的空档。
- 由计划任务启动的服务，在任务结束时，服务启动的进程也会被结束。一个先启动继任者再自己退出的程序，会把继任者一起丢掉。
- 备份可能运行很久（大文件夹要扫描好几分钟），在备份中途重启会丢掉它的工作（它会作为失败的任务被恢复，扫描要重新开始）。
- 谁能写入服务读取的那个文件夹，谁就决定了服务器上运行哪个程序。这是该功能唯一需要信任的地方。

## 决定

- **发布是一个有两个文件的文件夹，由 `scripts/release.ps1` 生成：** `timevault.exe` 和 `timevault.json`。脚本运行 `go vet`，用临时名称构建，向程序询问它的版本（即 `cmd/timevault-server/main.go` 中的版本号），计算哈希并测量大小，把程序放到位，**最后**以不带字节顺序标记的 UTF-8 原子地写入清单，且恰好只有四个字段。`package_url` 默认是文件名（服务会在它读取的清单旁边找到程序），使用 `-BaseUrl` 时则是完整地址。当某个发布与上一个发布版本号相同、程序却不同时，脚本会发出警告，因为没有任何服务会更新到它。
- **三个设置项。** `TIMEVAULT_UPDATE_URL` 是清单或其所在文件夹的地址：`http(s)` 地址，或文件夹/共享的绝对路径；其中不能包含用户名、密码、查询串或片段，默认为空，意思是服务从不更新自己。`TIMEVAULT_UPDATE_INTERVAL` 是读取的频率（默认 10 分钟；范围 1 分钟到 24 小时）。`TIMEVAULT_UPDATE_RESTART` 是 `supervise`（默认）或 `exit`。有更新地址的服务**必须设置 `TIMEVAULT_AUTO_MIGRATE=true`**，否则拒绝启动：新版本可能修改数据库，而服务自己更新时没有人会运行 `migrate`。
- **判断很简单。** 当 `latest_version` **不是**正在运行的版本时就更新，不分方向（开头的 `v` 会被忽略），所以回到旧版本的清单就是降级，而回到正在运行的版本的清单会取消正在等待的更新。没有可能会出错的版本排序。
- **`internal/update` 依次做的事情**，从第一次查看（启动后约 10 秒）开始，每个间隔一次，以及管理员要求查看时（设置页的**立即检查**，`POST /api/v1/update/check`）：
  1. 读取清单并严格检查（至多 64 KiB；版本由字母、数字和 `. _ + -` 组成；哈希为 64 个十六进制字符；大小从 1 字节到 512 MiB；忽略未知字段，以便以后的脚本可以增加字段）。读不到清单是一个会显示出来的错误，下次查看时再试；服务保持原样继续运行。
  2. 通过 `.part` 文件把程序下载到正在运行的程序旁边的 `timevault.new.exe`，只有大小和 sha256 都与清单一致时才保留。不一致不是最终结论：还在复制中的发布就是这个样子，所以下次查看时会再试。对 `http(s)` 地址的下载如果两分钟内没有收到任何数据就会放弃（一个会永远等待服务器的更新器将再也不会去查看），而下载之后从文件夹中消失的程序（被病毒扫描程序删除）会被重新下载。
  3. 运行下载的程序：`version` 必须输出清单中的版本（版本不同的程序在清单改变之前不会再试，这样错误的发布不会造成循环），`check-config` 必须接受本服务的设置（相同的环境变量和 `.env`；它不连接任何东西）。被拒绝时会显示出来，并且每次查看都会再试，因为管理员可能修好 `.env`。
  4. **让任务运行器进入排空状态**（`jobs.Runner.Drain`）：从此不再从队列中取任务。正在运行的任务继续运行到结束；这期间由人或调度器请求的备份进入队列等待。设置页显示"等待正在运行的任务"，页眉下面的提示告诉已登录的账号将要安装新版本、新任务需要等待。
  5. 当不再有任务运行时，**把程序放到位**：正在运行的 `timevault.exe` 被重命名为 `timevault.old-<版本>.exe`，新程序被重命名为 `timevault.exe`；失败时旧程序会恢复它的名字，任务重新被取用，并且该发布会被标记为在清单改变之前不再尝试。旁边会写入一个说明文件 `timevault.exe.update.json`。
  6. 以通常的方式停止服务（调度器、HTTP 服务器、任务运行器），这会释放数据库锁，然后启动新程序。
  - 等待没有时间上限（备份值得等），队列里的任务也不会被删除。
- **新程序怎样启动。**
  - `supervise`（默认）：被启动的那个进程作为一个**小型启动器**（`update.Supervise`）继续存在，并把新程序作为子进程运行，参数、环境变量、工作文件夹和控制台都与之前相同。启动器在收到 Ctrl+C 和终止信号时不会退出（子进程也会收到并自行停止），以子进程的退出码结束，并且在子进程以退出码 **75** 结束时启动下一个程序，后续的更新就是这样做的。子进程的标准输入是一个只由启动器持有、从不写入的管道：启动器无论以何种方式结束，即使被强制终止（结束计划任务时，任务计划程序就是这样处理的），子进程都会读到输入结束并有序关闭（`update.StopWithLauncher`），这样就不会留下一个没有人再来启动下一个程序、还占着端口和数据库锁的程序。控制台窗口和计划任务看到的始终是一个正在运行的程序。子进程的环境变量中有 `TIMEVAULT_SUPERVISED_BY`（启动器的进程 ID），它据此知道应当以 75 退出，而不是再启动一个自己的启动器。
  - `exit`：进程以退出码 75 结束，由会重新启动程序的东西启动新程序：服务管理器（NSSM），或者触发器每分钟重复一次并选择"不要启动新实例"的计划任务。任务计划程序的"如果任务失败，重新启动"设置不会在程序以 75 结束后重新启动它（试过），所以它不是做这件事的办法。
- **新程序会说明它已经接手。** 它读取并删除说明文件，在日志中写入 `automatic update: this program has taken over`，并写入 `stage` 为 `done` 的审计事件（说明文件指向另一个版本时不写）。
- **审计。** 一个动作 `app.update`，操作者为 `updater`，详情中有阶段（`staged`、`restart`、`done`、`cancelled`、`failed`）、版本、哈希，失败时还有失败的步骤。详情中**没有路径，也没有错误文本**：审计日志会显示给每一个登录的人。完整文本在服务的日志中。该操作者没有头像，该动作在审计筛选中可选。
- **谁能看到什么。** 每个已登录的人都能看到状态、正在运行的版本和已发布的版本、清单最近一次读取的时间，以及提示。只有管理员能看到发布地址和最近的错误（它们可能引用服务器的共享或文件夹，与服务器的所有真实路径一样，见 [0007](./0007-display-folders-and-hiding-server-paths.md)），也只有他们能要求立即查看。API 也是同样的区分。
- **没有签名，没有自动回退。** 大小和哈希能防止文件只复制了一半或损坏，防不住能写入发布文件夹的人，他能同时写这两样。旧程序会被保留（`timevault.old-<版本>.exe`，保留最新的三个），用于**手工**回退；运行手册说明了怎么做，以及数据库迁移对此的影响。

## 考虑过的备选方案

- **启动新程序后退出。** 最简单，但在这里是错的：计划任务结束时会结束服务启动的进程，控制台窗口也会显示旧程序已经结束。这仍然是 `exit` 留给服务管理器的做法。
- **单独的更新程序或服务。** 为一个只有一个文件的程序再多部署一个文件、再多运行一个东西。启动器是其中无法避免的那一小部分，而它就是程序本身。
- **只在启动时替换文件。** 需要有东西来启动程序；有了它，更新才能成为它的一个步骤。不适合手工启动的服务。
- **原地覆盖文件。** Windows 对正在运行的程序不允许。重命名则允许在旧程序运行时替换文件，并把旧程序留作回退之用。
- **有顺序的版本**（只更新到更大的版本）。需要版本方案和比较，而且无法通过修改清单来回退。相等判断就够了，也正是回退所需要的。
- **立即重启，之后再恢复任务。** 任务不能续跑：重启会丢掉备份已做的工作。等它结束花的是服务器上的时间，而不是数据。
- **签名**（Authenticode 或程序中的公钥）。能做到哈希做不到的事，代价是要保管和轮换密钥；没有人要求，而且发布文件夹本来就是管理员的地方。它可以不改动其余部分地加入清单。
- **等待的时间上限。** 会结束已运行数小时的长备份。如果有人需要更早重启，手工停止服务一直都是可行的。
- **新程序无法启动时自动回退。** 分不清是程序有问题还是数据库没起来，而且新程序做过的迁移不会被旧程序撤销（0.1.7 和 0.1.6 不能在同一个数据库上运行，见升级说明）。错误的自动决定比一个会说明原因的已停止的服务更糟。

## 影响

- **第一次更新就是最后一次手工更新。** 0.1.8 之前的版本不能自己更新；0.1.8 手工复制到服务器一次，并在 `.env` 中设置 `TIMEVAULT_UPDATE_URL` 和 `TIMEVAULT_AUTO_MIGRATE=true`，此后就自己更新。
- **权限。** 运行服务的账号必须能写入程序所在的文件夹（下载的文件、旧程序的副本和说明文件都放在那里），并能读取发布文件夹。服务启动时若该文件夹不能写入新文件，会在日志中说明；没有这个权限的更新会在交换这一步失败，服务继续使用旧程序运行。
- **信任。** 谁能写入发布文件夹，或者能坐在通往它的网络路径上，谁就决定了服务器上运行什么：请使用只有管理员能写入的文件夹，地址请使用 `https`。[security.md](../security.md) 中有说明。
- **启动器会一直是旧程序**，所以旧文件会一直被锁定，至多保留三个较旧的副本（无法删除的会留到启动器结束）。它自己的代码不会随更新而改变；要改动启动器，需要重启启动器。
- **发布在等待期间队列会变长**，所有任务（包括调度器的备份）都不会开始。重启的时间最晚就是运行最久的那个任务结束的时间。
- **通过了检查却在之后失败的新程序**（迁移失败、文件夹不见了）会让服务停止；启动器以程序的退出码结束，原因在服务的日志中。回退要手工完成。
- **`check-config` 检查的是设置**，而不是外部环境。它不连接数据库、Redis 或搜索引擎。
- **测试。** 更新的状态机（版本相同、暂存、排空、等待、重启、取消、发布变更、大小错误、哈希错误、版本错误、设置被拒绝、交换失败、清单不可达、审计中没有路径）、文件（下载、交换、重命名正在运行的程序、清理、说明文件）、来源（http 和文件、相对和绝对的程序地址、重定向、`202`）、启动器、设置、页面和 API 都有自动化测试覆盖。用编译后的程序运行过什么，见 [mvp-status.md](../mvp-status.md)。
- **用编译后的程序检查过**（2026-11-18，一台 Windows 电脑，没有管理员权限）：发布文件夹和 Web 地址；哈希、大小或版本错误以及读不了的清单，它们都被拒绝；等待正在运行的备份、期间请求的任务保持排队的更新；交换；启动器及其子进程，在控制台中按 Ctrl+C 会同时停止两者；退出码 75 的 `exit` 模式；以及 Windows 任务计划程序（当前用户的任务）：在 `supervise` 模式下任务在更新期间保持运行，结束任务会结束启动器和程序（这一项发现了一个被留下的程序，由此有了上文所说的输入管道）；在 `exit` 模式下，触发器每分钟重复一次的任务会在以 75 退出之后启动新程序，而"如果任务失败，重新启动"这个设置没有做到。没有检查：NSSM、Windows 服务、网络共享、几百兆的程序、公司的服务器。
