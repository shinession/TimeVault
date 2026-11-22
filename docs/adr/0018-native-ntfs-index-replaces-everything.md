# English

## Status

Accepted for 1.0.1 (2026-11-22). Replaces the search integration through Everything (plan.md §2, §4.1; the search adapter of 0.1.0; the folder sizes and the change probe of [0011](./0011-staggered-scheduler-and-change-probe.md) that asked Everything). It also revises two statements of earlier records: [0003](./0003-live-scan-without-vss-and-usn.md) kept the NTFS change journal out of scope, and [0004](./0004-restore-safety-and-privileges.md) chose a single unprivileged process. The decision of 0003 that a backup is a live scan stands: backups still walk the source and use neither VSS nor the journal.

## Context

- Search, the sizes of folders in the Explorer and the scheduler's "did this folder change" all came from **Everything**, a separate program that somebody has to install, configure (its HTTP server, a port, credentials, "index folder size", "index recently changed date"), keep running and keep bound to the loopback address. The Site License of Everything Server that the owner looked up costs several thousand US dollars a year, which the owner judged too much for something that can be built inside TimeVault.
- The owner asked for the core of what Everything gave, written in Go as part of TimeVault: no external program, nothing to switch on, no HTTP server. What is needed is only a search of file names below a monitored folder, the size of the folders in a listing and a cheap answer to "did anything below this folder change since the last scan".
- The largest monitored folder is about 500 GB and 1,000,000 files (the owner's figure), on NTFS volumes with a drive letter.
- Reading the file records and the change journal of an NTFS volume (`FSCTL_ENUM_USN_DATA`, `FSCTL_QUERY_FILE_LAYOUT`, `FSCTL_READ_USN_JOURNAL`) is what Everything does, needs no walk of the folders, and is only allowed to a process with administrator rights.

## Decision

- **An index in the memory of the service, from the volume.** `internal/ntfsindex` builds, for every drive letter that has monitored folders, a tree of the files and folders below them, and keeps it current by reading the change journal. The records of the volume are read twice: the folders first (cheap, no names decoded), to find which are inside the monitored folders, then the entries with their sizes and times, decoding a name only for an entry that is inside. Everything outside the monitored folders is dropped when the build is done. A record of the journal that was written while the build ran is applied afterwards, and applying a record twice does no harm.
- **The whole service runs elevated (option A of the discussion).** The index is a package behind the interfaces that the service already used (`search.Provider`, `search.FolderLister`, `search.ChangeProber`, implemented by `search.NTFS`), so the rest of the program does not change. A separate privileged helper that only reads the volume (option B) was weighed and not chosen: it needs a second process, an interface between the two and a way to deploy and supervise it, for a service that has one deployment. The volume is opened read-only and only metadata controls are used.
- **Everything is removed:** the adapter, its settings (`TIMEVAULT_EVERYTHING_*`, `TIMEVAULT_SCHEDULER_EVERYTHING_PROBE`, which became `scheduler_probe`), the development script, the live tests and the documentation.
- **Only the core.** Search matches a name that contains every term (plain text, ignoring case, the terms separated by blanks, phrases in double quotes), inside the monitored folder, folders first and then by name, in pages of at most 200. There are no operators, wildcards, regular expressions or content search. The index answers the folders directly inside a folder with the size of everything below each, and a probe: how many entries are below a folder and whether the monitored folder that holds it saw a change since a time.
- **The probe is conservative.** `liveSince` is the moment the build began to read the journal; a question about a time before it is answered "changed". The scheduler asks about the time of the last complete scan less 5 minutes, so after a start or a rebuild a folder is scanned as usual until the index has watched for 5 minutes longer than the last scan. A change anywhere below the monitored folder counts for all of it. The scheduler's other conditions of [0011](./0011-staggered-scheduler-and-change-probe.md) (the number of entries, the full scan every 24 hours, an item that was not captured, a change of the exclude patterns) are unchanged.
- **When the index cannot be used.** Without administrator rights, with a volume that is not NTFS, with a volume that has no change journal, or with a folder on a share, the index says why (the Status page and `GET /api/v1/health`), search answers "unavailable", folder sizes show "-" and the scheduler scans as it always did. Nothing else is affected. A journal that was replaced or overwritten, and a folder that was moved into a monitored folder from outside, make the volume be built again (the second at most once a minute); a volume that failed is tried again after 30 seconds.
- **Switches.** The Site setting `search_index` is `auto` (default) or `off`; `scheduler_probe` (default off) lets the scheduler use the probe ([0017](./0017-site-settings-in-the-database.md)).

## Alternatives considered

- **Keep Everything and automate its set-up.** The licence cost and the separate program with its own configuration, port and credentials remain, and the service would depend on something it cannot restart or check beyond an HTTP answer.
- **Start a private Everything instance from TimeVault.** Still a separate program, still a licence question for the redistribution and for the server edition, and a process to supervise.
- **Walk the folders for every search, and for every folder size.** A walk of 1,000,000 files takes minutes; a search and a listing must answer in a fraction of a second.
- **Windows Search.** It is not installed on every server, it indexes what the administrator chose, it is not a list of every name that exists now, and it cannot say that something below a folder was deleted or renamed.
- **Read the `$MFT` file directly and parse it.** The supported controls give the same records without parsing NTFS structures that the program would have to keep right.
- **Only the enumeration of the volume (names from the USN records), and `stat` the files for their sizes.** One `stat` for each of a million files is the slow part; the file layout control returns sizes and times in bulk.
- **Keep the index on disk between starts.** It would make a start faster, but a build takes seconds (13 seconds for 2.0 million entries on the development computer), and a saved index has to be matched with the journal and kept right; not worth a file format and a failure mode.
- **A privileged helper process (option B).** Better separation, rejected for now as described above; it can be added later behind the same interfaces.

## Consequences

- **Measured on the development computer** (Windows 11, NTFS, elevated): 2.0 million entries built in 13 seconds, about 230 bytes of memory per held entry (453 MB for those 2.0 million), a search below a tree of 1.75 million entries in about 0.13 seconds, and a created, deleted or renamed file found by search within 2 seconds. A monitored folder of one million files is about 230 MB. A monitored folder of 500 GB on a real volume was not tried.
- The service runs with administrator rights, and so does its web server: a flaw in the web layer is a flaw with administrator rights on the server ([security.md](../security.md)). The decision can be reversed behind the same interfaces with a helper process.
- A hard-linked file is held under its first name only, a junction or symbolic link is not entered, short (8.3) names are skipped, and only the length of the unnamed data stream is counted as the size.
- The first scans after a start or a rebuild are not skipped; for a short interval (under 5 minutes) that means several.
- The index misses nothing that the journal records. When NTFS writes the record of a file that a program keeps open was not measured, so a change to such a file can be seen late; the full scan every `scheduler_full_scan_every` is the guard.
- Not tried: ReFS, FAT and exFAT, a volume without a change journal, a journal that wraps around while the service runs (a model only), the service as a scheduled task or as SYSTEM.
- Everything-based installations: the settings of Everything are ignored and named in a warning at start; nothing else to do. The licence question is gone.

---

# 中文

## 状态

于 2026-11-22 在 1.0.1 中被接受。取代通过 Everything 集成搜索的做法（plan.md §2、§4.1；0.1.0 的搜索适配器；以及 [0011](./0011-staggered-scheduler-and-change-probe.md) 中向 Everything 询问的文件夹大小和变化探测）。它还修订了此前记录中的两处表述：[0003](./0003-live-scan-without-vss-and-usn.md) 把 NTFS 变更日志排除在范围之外，[0004](./0004-restore-safety-and-privileges.md) 选择了单一的非特权进程。0003 中"备份是实时扫描"的决定依然成立：备份仍然遍历备份源，既不使用 VSS，也不使用 journal。

## 背景

- 搜索、文件浏览中的文件夹大小，以及调度器的"这个文件夹有没有变化"，都来自 **Everything**。它是一个独立的程序，需要有人安装、配置（它的 HTTP 服务、端口、凭据、"索引文件夹大小"、"索引最近更改日期"）、让它一直运行并一直绑定在回环地址上。所有者查到的 Everything Server Site License 每年要几千美元，所有者认为，对于一个可以在 TimeVault 内部实现的东西来说，这太贵了。
- 所有者要求把 Everything 所提供的核心部分，用 Go 写成 TimeVault 的一部分：没有外部程序，没有什么需要打开，没有 HTTP 服务。需要的只是在监控文件夹之下按文件名搜索、在列表中显示文件夹大小，以及对"自上次扫描以来这个文件夹之下有没有任何变化"给出廉价的回答。
- 最大的监控文件夹大约是 500 GB、1,000,000 个文件（所有者给出的数字），位于带盘符的 NTFS 卷上。
- 读取 NTFS 卷的文件记录和变更日志（`FSCTL_ENUM_USN_DATA`、`FSCTL_QUERY_FILE_LAYOUT`、`FSCTL_READ_USN_JOURNAL`）正是 Everything 所做的事，不需要遍历文件夹，并且只允许具有管理员权限的进程这样做。

## 决定

- **服务内存中、由卷建立的索引。** `internal/ntfsindex` 为每个有监控文件夹的盘符建立其下文件和文件夹的树，并通过读取变更日志保持它最新。卷的记录会读两遍：先读文件夹（很便宜，不解码名称），找出哪些在监控文件夹之内；再读带大小和时间的条目，只对在其内的条目解码名称。建立完成时，监控文件夹之外的一切都会被丢弃。建立期间日志写下的记录会在之后应用，一条记录应用两次也没有害处。
- **整个服务以提升的权限运行（讨论中的方案 A）。** 索引是一个包，位于服务原来就在使用的接口之后（`search.Provider`、`search.FolderLister`、`search.ChangeProber`，由 `search.NTFS` 实现），所以程序的其余部分不变。曾权衡过只读取卷的独立特权辅助进程（方案 B），但没有选择它：它需要第二个进程、两者之间的接口，以及部署和监督它的方式，而这个服务只有一台部署。卷以只读方式打开，只使用元数据控制。
- **完全删除 Everything：** 适配器、它的设置（`TIMEVAULT_EVERYTHING_*`、`TIMEVAULT_SCHEDULER_EVERYTHING_PROBE`，后者变成了 `scheduler_probe`）、开发脚本、在线测试和文档。
- **只做核心。** 搜索匹配名称包含每一个词的项目（纯文本，不区分大小写，词用空白分隔，短语用双引号），范围在监控文件夹之内，先文件夹、再按名称排序，每页最多 200 条。没有操作符、通配符、正则表达式或内容搜索。索引回答文件夹中直接包含的各个文件夹及其下所有内容的大小，以及探测：某个文件夹之下有多少条目，以及包含它的监控文件夹自某个时间以来是否看到变化。
- **探测是保守的。** `liveSince` 是建立过程开始读取日志的时刻；对此之前的某个时间的提问，回答"有变化"。调度器询问的是上次完整扫描的时间减去 5 分钟，所以在启动或重新建立之后，一个文件夹照常被扫描，直到索引监视的时间比上次扫描晚了 5 分钟。监控文件夹之下任何位置的变化，都算作整个监控文件夹的变化。[0011](./0011-staggered-scheduler-and-change-probe.md) 中调度器的其他条件（条目数量、每 24 小时一次完整扫描、有未捕获的项目、排除规则的更改）保持不变。
- **索引无法使用时。** 没有管理员权限、卷不是 NTFS、卷没有变更日志、或文件夹在共享上时，索引会说明原因（状态页和 `GET /api/v1/health`），搜索回答"不可用"，文件夹大小显示"-"，调度器照旧扫描。其他功能不受影响。日志被替换或被覆盖，以及有文件夹从外部被移入监控文件夹，都会让该卷重新建立（后者最多每分钟一次）；失败的卷会在 30 秒后重试。
- **开关。** 站点设置 `search_index` 为 `auto`（默认）或 `off`；`scheduler_probe`（默认关闭）让调度器使用探测（[0017](./0017-site-settings-in-the-database.md)）。

## 考虑过的备选方案

- **保留 Everything 并把它的设置自动化。** 授权费用，以及带有自己的配置、端口和凭据的独立程序依然存在，而且服务会依赖一个它无法重启、也只能通过 HTTP 应答来检查的东西。
- **由 TimeVault 启动一个私有的 Everything 实例。** 仍然是一个独立的程序，重新分发和服务器版本仍然有授权问题，还多了一个要监督的进程。
- **每次搜索、每个文件夹大小都遍历文件夹。** 遍历 1,000,000 个文件需要几分钟；而搜索和列表必须在不到一秒的时间内给出回答。
- **Windows Search。** 并非每台服务器都安装了它，它索引的是管理员选定的内容，它不是现在存在的每个名称的清单，也无法说明某个文件夹之下有东西被删除或重命名了。
- **直接读取并解析 `$MFT` 文件。** 受支持的控制给出同样的记录，而不必去解析程序自己要保证正确的 NTFS 结构。
- **只枚举卷（从 USN 记录得到名称），再对文件逐个 `stat` 取得大小。** 对一百万个文件各做一次 `stat` 是慢的部分；文件布局控制可以成批返回大小和时间。
- **在两次启动之间把索引保存在磁盘上。** 这会让启动更快，但建立只需要几秒钟（在开发电脑上 200 万个条目需要 13 秒），而保存的索引要与日志对上并保持正确；不值得为它增加一种文件格式和一种故障模式。
- **特权辅助进程（方案 B）。** 隔离更好，如上所述暂不选择；以后可以在同样的接口之后加上它。

## 影响

- **在开发电脑上测得**（Windows 11，NTFS，已提升权限）：200 万个条目在 13 秒内建好，每个被保存的条目大约占用 230 字节内存（这 200 万个条目共 453 MB），在一棵有 175 万个条目的树之下搜索大约需要 0.13 秒，而新建、删除或重命名的文件在 2 秒内就能被搜索找到。一个有一百万个文件的监控文件夹大约占用 230 MB。没有在真实卷上试过 500 GB 的监控文件夹。
- 服务以管理员权限运行，它的 Web 服务器也是：Web 层的一个缺陷就是服务器上带管理员权限的缺陷（[security.md](../security.md)）。这个决定可以在同样的接口之后，用一个辅助进程来撤销。
- 有硬链接的文件只按它的第一个名称保存，联接点或符号链接不会被进入，短（8.3）名称会被跳过，大小只统计未命名数据流的长度。
- 启动或重新建立之后的最初几次扫描不会被跳过；对于较短的间隔（小于 5 分钟），这意味着好几次。
- journal 记录下来的内容索引都不会漏掉。NTFS 何时写入某个被程序一直打开着的文件的记录没有测量过，所以对这样的文件的修改可能被较晚发现；每隔 `scheduler_full_scan_every` 的完整扫描就是这道保护。
- 没有试过：ReFS、FAT 和 exFAT、没有变更日志的卷、服务运行期间日志回绕的情况（只用模型测试过）、作为计划任务或以 SYSTEM 运行的服务。
- 原来基于 Everything 的安装：Everything 的设置会被忽略，并在启动时的警告里被点名；没有别的要做。授权问题不复存在。
