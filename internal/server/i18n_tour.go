package server

// The texts of the guide of a page (web/assets/js/tour.js). For every part of a page that the guide explains there are two
// messages, tour.<key>.title and tour.<key>.text, where <key> is what the template passes to Page.Tour. The rest are the
// words of the guide's own buttons and of the two round buttons that open it and the documentation.
//
// They live in this file, apart from the other messages, because there are many of them and they are long. The tests of
// this package check that every step a template names has both messages in both languages, and that no message is left
// over (unit_test.go).
func init() {
	for lang, texts := range map[string]map[string]string{"en": tourEN, "zh": tourZH} {
		for key, text := range texts {
			messages[lang][key] = text
		}
	}
}

var tourEN = map[string]string{
	"fab.guide": "Guide: a step-by-step tour of this page", "fab.docs": "Documentation: the user guide, in a new tab",
	"help.title": "User guide",
	"tour.label": "Page guide", "tour.next": "Next", "tour.back": "Back", "tour.done": "Done", "tour.close": "Close the guide",
	"tour.step": "Step {n} of {total}",

	// The header and the buttons that every page has.
	"tour.nav.title":     "Navigation",
	"tour.nav.text":      "Move between the pages here. Explorer shows your folders as they are now, Time Machine as they were, and Backups lets you back up and follow the jobs. Signed-in accounts also see Audit and Settings. The highlighted tab is the page you are on.",
	"tour.theme.title":   "Light or dark",
	"tour.theme.text":    "Switches between the light and the dark theme. Until you use it, the page follows your system setting; after that this browser remembers your choice.",
	"tour.lang.title":    "Language",
	"tour.lang.text":     "Switches the whole page between English and 中文. This browser remembers your choice.",
	"tour.account.title": "Your account",
	"tour.account.text":  "Shows who you are signed in as; the button next to your name signs you out. What you may do depends on your account and, for each monitored folder, on the access it gives you.",
	"tour.signin.title":  "Sign in",
	"tour.signin.text":   "Anyone can browse the folders that allow guests. Sign in with your company account to back up, restore or change settings.",
	"tour.fab.title":     "The round buttons",
	"tour.fab.text":      "The arrows jump to the top or the bottom of a long page. The book opens the documentation, a full guide to every page, in a new tab. The compass starts this guide.",

	// What several pages have in common.
	"tour.source.title":   "Monitored folder",
	"tour.source.text":    "Choose the monitored folder to look at. A monitored folder is a folder that TimeVault backs up; you only see the ones you may browse.",
	"tour.crumbs.title":   "Where you are",
	"tour.crumbs.text":    "The path of the folder you are looking at. Click any part to go back up; the first part is the monitored folder itself.",
	"tour.location.title": "Location",
	"tour.location.text":  "The folder as people open it over the network. “Copy” puts the path on your clipboard, so you can paste it into a file manager. Administrators also see where the folder is on the server.",
	"tour.tools.title":    "List controls",
	"tour.tools.text":     "Show only one type of item, choose how many items fit on a page, and see how many there are. Page links appear when the folder is long.",
	"tour.empty.title":    "Nothing to browse yet",
	"tour.empty.text":     "There is no monitored folder you can browse. If you have not signed in, sign in; otherwise ask an administrator to give you access. Administrators add folders in Settings.",

	// Explorer.
	"tour.explorer.search.title":  "Search",
	"tour.explorer.search.text":   "Searches this folder by name. Press / to jump to the box. Each result shows the folder it is in; click it to open it. “Clear search” brings the folder back.",
	"tour.explorer.latest.title":  "Last recovery point",
	"tour.explorer.latest.text":   "When this folder was last backed up, and how its integrity check ended (Pending, Validated, Verified or Failed). “Open in Time Machine” shows the folder as it was then.",
	"tour.explorer.list.title":    "Files and folders",
	"tour.explorer.list.text":     "The folder as it is on the disk now. Click a folder to open it, or a file to see all its saved versions. Click Name, Type, Size or Modified to sort; folders always come first.",
	"tour.explorer.status.title":  "Backup status",
	"tour.explorer.status.text":   "How each item compares with the newest recovery point. Protected: backed up and unchanged. Changed: changed since then. New: not backed up yet. Not captured: it could not be read last time, for example a locked file.",
	"tour.explorer.deleted.title": "Deleted since the last backup",
	"tour.explorer.deleted.text":  "Items that were in the newest recovery point but are gone from the disk. Open one to see its versions and restore it.",

	// Time Machine.
	"tour.history.timeline.title": "Recovery points",
	"tour.history.timeline.text":  "Each dot is one recovery point: the saved state of the folder at one moment, with its time, its number and how it was checked. Click one to travel back to that moment. “Now” returns to the live folder.",
	"tour.history.banner.title":   "What you are viewing",
	"tour.history.banner.text":    "Names the recovery point you are looking at and how far it can be trusted. Live scan: the files were read from the running disk. Verified or Validated: the result of its integrity check. “Older point”, “Not restorable” and “Partial” warn you that it is not the newest, cannot be restored from, or has lost some old versions.",
	"tour.history.actions.title":  "Restore",
	"tour.history.actions.text":   "“Restore selected…” restores the items you ticked; “Restore this folder…” (at the top level “Restore everything…”) restores the whole folder. Nothing is written yet: the next page is a preview, and the copies go into a new folder in the restore location, never over your originals. Without signing in you see “Sign in to restore files”.",
	"tour.history.list.title":     "The folder at that moment",
	"tour.history.list.text":      "Folders and files as they were in this recovery point. Tick items to restore them; the box in the header ticks the items on this page only. Click a folder to open it, or a file to see its versions.",
	"tour.history.compare.title":  "Compared with now",
	"tour.history.compare.text":   "Unchanged: the same as today. Changed later: modified after this point. Deleted later: gone from the disk now. Not captured: it could not be backed up at that time.",

	// Versions of one file.
	"tour.versions.card.title":    "Versions of this file",
	"tour.versions.card.text":     "Every state of this file that a recovery point saved. Each one is a real, restorable copy.",
	"tour.versions.table.title":   "Reading the table",
	"tour.versions.table.text":    "Captured: when this version was first saved. Until: when it stopped being the current one (“Current” for the latest). Size and Modified describe the file. SHA-256 is its checksum. Content says whether the stored copy is available.",
	"tour.versions.restore.title": "Restore a version",
	"tour.versions.restore.text":  "“Restore…” opens a preview for restoring this version into a new folder; nothing is written until you confirm. “Browse point” opens the folder as it was in the recovery point that first saved this version.",
	"tour.versions.back.title":    "Back",
	"tour.versions.back.text":     "Returns to the list you came from.",

	// Backups.
	"tour.backups.manage.title":  "Manage monitored folders",
	"tour.backups.manage.text":   "Opens Settings, where monitored folders are added and changed.",
	"tour.backups.card.title":    "A monitored folder",
	"tour.backups.card.text":     "One card for each monitored folder: its name, what you may do with it (the chip next to the name) and its network path. A folder that is “Switched off” keeps its history but is not backed up.",
	"tour.backups.actions.title": "Back up",
	"tour.backups.actions.text":  "“Back up now” starts a backup at once; it trusts file size and date to skip files that did not change. “Full re-read” reads and checks every file again, which takes longer. These buttons need Back up or Full control for the folder.",
	"tour.backups.running.title": "A backup is running",
	"tour.backups.running.text":  "The link opens the job, with its progress and log. This page updates by itself when the job ends.",
	"tour.backups.facts.title":   "Where the folder stands",
	"tour.backups.facts.text":    "The latest recovery point (time and number), the automatic schedule and when the next backup is due, when it last ran, and which names are excluded.",
	"tour.backups.points.title":  "Recovery points",
	"tour.backups.points.text":   "Each row is one backup: number, type (Full or Incremental), state, when it was captured, files and size, the changes (+ added, ~ modified, - deleted), how many files could not be backed up, and the integrity check.",
	"tour.backups.verify.title":  "Browse and Verify",
	"tour.backups.verify.text":   "“Browse” opens that recovery point in Time Machine. “Verify” re-reads every stored object of the point and checks its checksum: use it when you want to be sure the backup is intact.",
	"tour.backups.jobs.title":    "Recent jobs",
	"tour.backups.jobs.text":     "Every backup, verification, restore and version cleanup: type, folder, state, when it was created, how long it took and who asked for it. Click the number to open the job.",
	"tour.backups.restore.title": "Where restores go",
	"tour.backups.restore.text":  "The place restored copies are written below. A restore never overwrites the original files and never writes into a monitored folder.",

	// A job.
	"tour.job.card.title":     "The job",
	"tour.job.card.text":      "What kind of job it is and its state. A running job refreshes by itself. “Cancel job” stops it, if you may.",
	"tour.job.facts.title":    "Details",
	"tour.job.facts.text":     "The folder and recovery point it belongs to, who requested it, when it was created, how long it ran and the phase it is in.",
	"tour.job.progress.title": "Progress",
	"tour.job.progress.text":  "While the job runs, the bar shows how far it is.",
	"tour.job.counters.title": "Counters",
	"tour.job.counters.text":  "What the job has done so far, or in total once it is over: files and folders handled, data read or written, and what was added, changed, deleted, skipped or failed.",
	"tour.job.items.title":    "Items of a restore",
	"tour.job.items.text":     "For a restore, the result for each file and folder: created, replaced, saved as a copy, skipped or failed, with a message. “Failed only” narrows the list.",
	"tour.job.log.title":      "Log",
	"tour.job.log.text":       "The step-by-step log of the job, for signed-in accounts. If a job fails, read the error above and this log.",

	// The preview of a restore.
	"tour.restore.facts.title":   "What you are restoring",
	"tour.restore.facts.text":    "The folder and recovery point, the items you selected and the destination, written the way people open it over the network.",
	"tour.restore.summary.title": "Summary",
	"tour.restore.summary.text":  "How many files and folders, how much data, and how many will be created, replaced, saved as copies, skipped or blocked. Blocked items must be solved, or a partial restore allowed, before you can start.",
	"tour.restore.options.title": "Options",
	"tour.restore.options.text":  "Name the destination folder (it is created inside the restore location), choose what happens when a file is already there (keep it, keep both, or replace it) and, if you want, allow a partial restore. “Update preview” recalculates the plan with your choices.",
	"tour.restore.changes.title": "Planned changes",
	"tour.restore.changes.text":  "The first items the restore would write, with the action for each. Nothing has been written yet.",
	"tour.restore.confirm.title": "Confirm",
	"tour.restore.confirm.text":  "“Start restore” begins the restore. The preview is valid for 15 minutes and belongs to your account and exactly this plan. Replacing existing files needs an extra tick.",

	// Audit.
	"tour.audit.filter.title": "Filter",
	"tour.audit.filter.text":  "Show only one kind of action, such as sign-ins or backups.",
	"tour.audit.table.title":  "The record",
	"tour.audit.table.text":   "Who did what and when: time, user, action, target, result, how long it took, the address it came from and details. Records cannot be changed or deleted. You see the events of the folders you may browse and the events that belong to no folder, such as sign-ins.",
	"tour.audit.more.title":   "Load more",
	"tour.audit.more.text":    "Shows older events.",

	// Settings.
	"tour.settings.intro.title":   "Monitored folders",
	"tour.settings.intro.text":    "The folders you can see, each with what you may do with it. Administrators add folders; whoever has full control of a folder changes its settings and who may use it.",
	"tour.settings.folder.title":  "A folder",
	"tour.settings.folder.text":   "Its name with chips (switched off, no display folder yet, your access) and its network path, then the schedule, the latest recovery point, the last run and how long it took, the versions kept and the names excluded.",
	"tour.settings.edit.title":    "Edit",
	"tour.settings.edit.text":     "Opens the form for the name, the display folder, the exclude patterns, the on and off switch, the schedule, the versions kept per file and who may do what. Administrators can also change the folder on the server until it has its first recovery point.",
	"tour.settings.access.title":  "Who may do what",
	"tour.settings.access.text":   "For each level (Browse, Back up, Full control): guests, everyone who is signed in, or the people named.",
	"tour.settings.add.title":     "Add a folder",
	"tour.settings.add.text":      "Administrators add a monitored folder here: a name, the display folder people use, the folder on the server, what to exclude, an optional schedule and who may use it. The folder is only ever read, never changed.",
	"tour.settings.storage.title": "Storage",
	"tour.settings.storage.text":  "The backup folder, where the contents of the backed-up files are kept, with its free space, and the restore folder, where restored copies go.",
	"tour.settings.service.title": "The service",
	"tour.settings.service.text":  "The version of the service, and the Status page, which shows how each part is doing. The local administrator also finds the button for the Site settings here: how the service is set up.",
	"tour.settings.updates.title": "Program updates",
	"tour.settings.updates.text":  "Whether the service updates itself from a release folder, which version is released, when it last looked and its state. Administrators can also ask for a check at any time.",

	// Status.
	"tour.status.table.title": "Components",
	"tour.status.table.text":  "How each part is doing: database, repository (with free space), search, user accounts, restore folder and job queue. OK, Degraded (it works, but something is wrong), Not enabled or Failed.",
	"tour.status.note.title":  "Not implemented yet",
	"tour.status.note.text":   "VSS snapshots and NTFS change tracking are planned. Recovery points are live scans of the folder.",

	// The user guide itself.
	"tour.help.toc.title":  "Contents",
	"tour.help.toc.text":   "Jump to the part of the guide you need. It covers each page of the application, how automatic backups and the versions kept work, the problems you may meet, and a glossary of every label.",
	"tour.help.body.title": "The guide",
	"tour.help.body.text":  "Buttons and labels are written the way they appear on screen, in the language you chose at the top.",
}

var tourZH = map[string]string{
	"fab.guide": "页面导览：逐步介绍本页", "fab.docs": "文档：在新标签页中打开用户手册",
	"help.title": "用户手册",
	"tour.label": "页面导览", "tour.next": "下一步", "tour.back": "上一步", "tour.done": "完成", "tour.close": "关闭导览",
	"tour.step": "第 {n} 步，共 {total} 步",

	// The header and the buttons that every page has.
	"tour.nav.title":     "导航",
	"tour.nav.text":      "在这里切换页面。“文件浏览”显示文件夹现在的样子，“时间机器”显示它过去的样子，“备份”用来备份并查看任务。已登录的账号还会看到“审计”和“设置”。高亮的标签就是你当前所在的页面。",
	"tour.theme.title":   "浅色或深色",
	"tour.theme.text":    "在浅色和深色主题之间切换。在你使用它之前，页面跟随系统设置；之后这个浏览器会记住你的选择。",
	"tour.lang.title":    "语言",
	"tour.lang.text":     "把整个页面在 English 和 中文 之间切换。这个浏览器会记住你的选择。",
	"tour.account.title": "你的账号",
	"tour.account.text":  "显示你以谁的身份登录；名字旁边的按钮用来退出登录。你能做什么，取决于你的账号，以及每个监控文件夹给你的权限。",
	"tour.signin.title":  "登录",
	"tour.signin.text":   "任何人都可以浏览允许访客查看的文件夹。使用公司账号登录后，才能备份、恢复或修改设置。",
	"tour.fab.title":     "右下角的圆形按钮",
	"tour.fab.text":      "箭头可在较长的页面中跳到顶部或底部。书本图标在新标签页中打开文档，也就是每个页面的完整使用说明。指南针图标就是现在这个页面导览。",

	// What several pages have in common.
	"tour.source.title":   "监控文件夹",
	"tour.source.text":    "选择要查看的监控文件夹。监控文件夹是 TimeVault 要备份的文件夹；你只会看到自己有权浏览的那些。",
	"tour.crumbs.title":   "当前位置",
	"tour.crumbs.text":    "你正在查看的文件夹的路径。点击其中任何一段可以返回上一级；第一段就是监控文件夹本身。",
	"tour.location.title": "位置",
	"tour.location.text":  "人们通过网络打开该文件夹时使用的路径。点击“复制”会把路径放进剪贴板，方便粘贴到文件管理器中。管理员还能看到文件夹在服务器上的实际位置。",
	"tour.tools.title":    "列表控制",
	"tour.tools.text":     "只显示某一种类型的项目，选择每页显示多少项，并查看一共有多少项。文件夹内容较多时会出现分页链接。",
	"tour.empty.title":    "还没有可浏览的内容",
	"tour.empty.text":     "目前没有你可以浏览的监控文件夹。如果还没有登录，请先登录；否则请联系管理员给你授权。管理员在“设置”中添加文件夹。",

	// Explorer.
	"tour.explorer.search.title":  "搜索",
	"tour.explorer.search.text":   "按名称搜索这个文件夹。按 / 键可直接跳到搜索框。每条结果会显示它所在的文件夹，点击即可打开。“清除搜索”会回到文件夹本身。",
	"tour.explorer.latest.title":  "最近的恢复点",
	"tour.explorer.latest.text":   "这个文件夹最后一次备份的时间，以及它的完整性检查结果（待处理、已验证提交、已完整校验或校验失败）。“在时间机器中打开”会显示当时文件夹的样子。",
	"tour.explorer.list.title":    "文件和文件夹",
	"tour.explorer.list.text":     "文件夹现在在磁盘上的样子。点击文件夹可以打开，点击文件可以查看它已保存的所有版本。点击“名称”“类型”“大小”或“修改时间”可以排序；文件夹始终排在前面。",
	"tour.explorer.status.title":  "备份状态",
	"tour.explorer.status.text":   "每个项目与最新恢复点的比较结果。已保护：已备份且没有变化。已变更：之后发生了变化。新增：还没有备份。未能备份：上次无法读取，例如被锁定的文件。",
	"tour.explorer.deleted.title": "自上次备份以来已删除",
	"tour.explorer.deleted.text":  "这些项目存在于最新的恢复点，但磁盘上已经不在了。打开其中一项即可查看它的版本并恢复。",

	// Time Machine.
	"tour.history.timeline.title": "恢复点",
	"tour.history.timeline.text":  "每个圆点是一个恢复点：文件夹在某一时刻的保存状态，并显示它的时间、编号和校验情况。点击其中一个，就能回到那一刻。“现在”会回到实时文件夹。",
	"tour.history.banner.title":   "你正在查看的内容",
	"tour.history.banner.text":    "说明你正在看哪个恢复点，以及它有多可靠。实时扫描：文件读取自正在运行的磁盘。已完整校验或已验证提交：它的完整性检查结果。“较早的恢复点”“不可恢复”和“不完整”会提醒你：它不是最新的、不能用来恢复，或者丢失了一些旧版本。",
	"tour.history.actions.title":  "恢复",
	"tour.history.actions.text":   "“恢复所选项…”恢复你勾选的项目；“恢复此文件夹…”（在最上层是“恢复全部内容…”）恢复整个文件夹。此时还没有写入任何内容：下一页是预览，副本会写入恢复位置下的新文件夹，绝不会覆盖你的原文件。没有登录时，这里显示的是“登录后可恢复文件”。",
	"tour.history.list.title":     "那一刻的文件夹",
	"tour.history.list.text":      "文件夹和文件在这个恢复点里的样子。勾选项目即可恢复它们；表头的复选框只会勾选本页的项目。点击文件夹可以打开，点击文件可以查看它的版本。",
	"tour.history.compare.title":  "与当前对比",
	"tour.history.compare.text":   "未变化：和现在一样。之后被修改：这个时间点之后被改动过。之后被删除：现在磁盘上已经没有了。未能备份：当时没能备份它。",

	// Versions of one file.
	"tour.versions.card.title":    "这个文件的版本",
	"tour.versions.card.text":     "恢复点保存过的这个文件的每一种状态。每一个都是真实、可恢复的副本。",
	"tour.versions.table.title":   "如何阅读这张表",
	"tour.versions.table.text":    "采集时间：这个版本第一次被保存的时间。有效至：它不再是当前版本的时间（最新的显示为“当前版本”）。“大小”和“修改时间”描述文件本身。SHA-256 是它的校验值。“内容”说明已保存的副本是否可用。",
	"tour.versions.restore.title": "恢复某个版本",
	"tour.versions.restore.text":  "“恢复…”会打开预览，把这个版本恢复到一个新文件夹；确认之前不会写入任何内容。“浏览该恢复点”会打开第一次保存这个版本的恢复点中文件夹的样子。",
	"tour.versions.back.title":    "返回",
	"tour.versions.back.text":     "回到你来时的列表。",

	// Backups.
	"tour.backups.manage.title":  "管理监控文件夹",
	"tour.backups.manage.text":   "打开“设置”页面，监控文件夹在那里添加和修改。",
	"tour.backups.card.title":    "一个监控文件夹",
	"tour.backups.card.text":     "每个监控文件夹一张卡片：名称、你对它的权限（名称旁边的标签）和它的网络路径。“已停用”的文件夹会保留历史，但不再备份。",
	"tour.backups.actions.title": "备份",
	"tour.backups.actions.text":  "“立即备份”马上开始备份；它依据文件大小和日期跳过没有变化的文件。“完整重读”会重新读取并检查每个文件，耗时更长。要使用这两个按钮，你需要对该文件夹拥有“备份”或“完全控制”权限。",
	"tour.backups.running.title": "备份正在进行",
	"tour.backups.running.text":  "点击链接可以打开任务，查看进度和日志。任务结束时，本页会自动更新。",
	"tour.backups.facts.title":   "文件夹的现状",
	"tour.backups.facts.text":    "最新恢复点（时间和编号）、自动备份计划和下次备份的时间、上次运行的时间，以及排除了哪些名称。",
	"tour.backups.points.title":  "恢复点",
	"tour.backups.points.text":   "每一行是一次备份：编号、类型（全量或增量）、状态、采集时间、文件数和大小、变化（+ 新增、~ 修改、- 删除）、有多少文件未能备份，以及完整性检查。",
	"tour.backups.verify.title":  "浏览和校验",
	"tour.backups.verify.text":   "“浏览”在时间机器中打开这个恢复点。“校验”会重新读取这个恢复点的每个已存储对象并核对校验值：想确认备份完好无损时使用它。",
	"tour.backups.jobs.title":    "最近的任务",
	"tour.backups.jobs.text":     "每一次备份、校验、恢复和版本清理：类型、文件夹、状态、创建时间、耗时和发起人。点击编号可以打开该任务。",
	"tour.backups.restore.title": "恢复结果放在哪里",
	"tour.backups.restore.text":  "恢复出的副本会写入这个位置之下。恢复绝不会覆盖原文件，也绝不会写入监控文件夹。",

	// A job.
	"tour.job.card.title":     "任务",
	"tour.job.card.text":      "任务的类型和状态。运行中的任务会自动刷新。如果你有权限，“取消任务”可以停止它。",
	"tour.job.facts.title":    "详情",
	"tour.job.facts.text":     "它所属的文件夹和恢复点、谁发起的、何时创建、运行了多久，以及它所处的阶段。",
	"tour.job.progress.title": "进度",
	"tour.job.progress.text":  "任务运行时，进度条显示它完成了多少。",
	"tour.job.counters.title": "计数",
	"tour.job.counters.text":  "任务到目前为止做了什么，结束后则是总共做了什么：处理的文件和文件夹、读取或写入的数据量，以及新增、修改、删除、跳过或失败的数量。",
	"tour.job.items.title":    "恢复的各个项目",
	"tour.job.items.text":     "对于恢复任务，这里是每个文件和文件夹的结果：已创建、已替换、已另存、已跳过或失败，并附有说明。“仅失败项”可以缩小列表范围。",
	"tour.job.log.title":      "日志",
	"tour.job.log.text":       "任务的逐步日志，仅对已登录的账号显示。任务失败时，请查看上面的错误和这里的日志。",

	// The preview of a restore.
	"tour.restore.facts.title":   "你要恢复的内容",
	"tour.restore.facts.text":    "文件夹和恢复点、你选择的项目，以及目标位置（按人们通过网络打开它的方式书写）。",
	"tour.restore.summary.title": "概要",
	"tour.restore.summary.text":  "有多少文件和文件夹、多少数据，以及将要创建、替换、另存、跳过或受阻的数量。要开始恢复，必须先解决受阻的项目，或者允许部分恢复。",
	"tour.restore.options.title": "选项",
	"tour.restore.options.text":  "为目标文件夹命名（它会创建在恢复位置之内），选择文件已存在时怎么办（保留、两者都保留，或替换），也可以允许部分恢复。“更新预览”会按你的选择重新计算计划。",
	"tour.restore.changes.title": "计划的变更",
	"tour.restore.changes.text":  "恢复将要写入的前若干项，以及对每一项要执行的操作。此时还没有写入任何内容。",
	"tour.restore.confirm.title": "确认",
	"tour.restore.confirm.text":  "“开始恢复”会开始恢复。预览的有效期是 15 分钟，并且只属于你的账号和这份计划。替换已有文件还需要额外勾选确认。",

	// Audit.
	"tour.audit.filter.title": "筛选",
	"tour.audit.filter.text":  "只显示某一种操作，例如登录或备份。",
	"tour.audit.table.title":  "记录",
	"tour.audit.table.text":   "谁在什么时候做了什么：时间、用户、操作、对象、结果、耗时、来源地址和详情。记录不能被修改或删除。你能看到自己有权浏览的文件夹的事件，以及不属于任何文件夹的事件，例如登录。",
	"tour.audit.more.title":   "加载更多",
	"tour.audit.more.text":    "显示更早的事件。",

	// Settings.
	"tour.settings.intro.title":   "监控文件夹",
	"tour.settings.intro.text":    "你能看到的文件夹，以及你对每个文件夹的权限。管理员负责添加文件夹；对某个文件夹拥有完全控制权的人，可以修改它的设置以及谁能使用它。",
	"tour.settings.folder.title":  "一个文件夹",
	"tour.settings.folder.text":   "名称和标签（已停用、还没有显示文件夹、你的权限）以及网络路径，下面是自动备份计划、最新恢复点、上次运行及耗时、保留的版本和排除的名称。",
	"tour.settings.edit.title":    "编辑",
	"tour.settings.edit.text":     "打开表单，可修改名称、显示文件夹、排除规则、启用/停用开关、自动备份计划、每个文件保留的版本数，以及谁能做什么。在第一个恢复点产生之前，管理员还可以修改服务器上的文件夹。",
	"tour.settings.access.title":  "谁能做什么",
	"tour.settings.access.text":   "每个级别（浏览、备份、完全控制）对应的人：访客、所有已登录的人，或指定的人员。",
	"tour.settings.add.title":     "添加文件夹",
	"tour.settings.add.text":      "管理员在这里添加监控文件夹：名称、人们使用的显示文件夹、服务器上的文件夹、要排除的内容、可选的自动备份计划，以及谁能使用它。文件夹只会被读取，绝不会被修改。",
	"tour.settings.storage.title": "存储",
	"tour.settings.storage.text":  "备份文件夹（保存被备份文件的内容）及其剩余空间，以及恢复文件夹（恢复出的副本放在这里）。",
	"tour.settings.service.title": "服务",
	"tour.settings.service.text":  "服务的版本，以及“状态”页面，它会显示各个部分运行得怎么样。本地管理员还会在这里看到“站点设置”按钮：服务是如何配置的。",
	"tour.settings.updates.title": "程序更新",
	"tour.settings.updates.text":  "服务是否从发布文件夹自动更新自己、已发布的版本、上次检查的时间及当前状态。管理员还可以随时手动发起一次检查。",

	// Status.
	"tour.status.table.title": "组件",
	"tour.status.table.text":  "各个部分运行得怎么样：数据库、备份仓库（含剩余空间）、搜索、用户账号、恢复文件夹和任务队列。状态有正常、降级（能用，但有问题）、未启用或故障。",
	"tour.status.note.title":  "尚未实现",
	"tour.status.note.text":   "VSS 快照和 NTFS 变更跟踪属于后续计划。恢复点是对文件夹的实时扫描。",

	// The user guide itself.
	"tour.help.toc.title":  "目录",
	"tour.help.toc.text":   "跳到你需要的部分。手册介绍应用的每个页面、自动备份与保留的版本数如何运作、你可能遇到的问题，以及每个标签的词汇表。",
	"tour.help.body.title": "手册正文",
	"tour.help.body.text":  "按钮和标签都按它们在屏幕上显示的样子书写，语言与你在页面顶部选择的一致。",
}
