package server

// The texts of the servers of a group (docs/adr/0019-several-servers-one-database-one-schema-each.md): the Servers page
// (web/templates/servers.html), the tabs that switch a page between the servers, and what the pages of another server's
// folders say where this server cannot do what it does for its own. A test checks that every state of a server has its texts
// in both languages.
func init() {
	for lang, texts := range map[string]map[string]string{"en": clusterEN, "zh": clusterZH} {
		for key, text := range texts {
			messages[lang][key] = text
		}
	}
}

var clusterEN = map[string]string{
	"nav.servers": "Servers",

	"scope.label": "Servers",
	"scope.this":  "This server",

	"servers.title":            "Servers",
	"servers.intro":            "The servers that share this database. Every server keeps its backups on its own disk; here you see all of them, and the Time Machine, the Backups page and the Audit log can show the history of the folders of any server that you may browse.",
	"servers.name":             "Server",
	"servers.state":            "State",
	"servers.version":          "Version",
	"servers.folders":          "Folders",
	"servers.latest":           "Latest backup",
	"servers.seen":             "Last heard of",
	"servers.schema":           "Database schema",
	"servers.contract":         "(read contract %d)",
	"servers.this":             "this server",
	"servers.note":             "Folders are counted as far as you may browse them. The live view, the search and every action on the folders of another server are not available from this server in this version; its history is read from the database, also when that server is offline.",
	"servers.no_folders_title": "No folders to show",
	"servers.no_folders_text":  "This server has no monitored folder that you may browse.",
	"servers.unavailable_note": "The folders of the server %s could not be listed right now.",

	"servers.state.online":       "Online",
	"servers.state.offline":      "Offline",
	"servers.state.waiting":      "Not reported yet",
	"servers.state.incompatible": "Version not compatible",
	"servers.hint.online":        "It reported a moment ago.",
	"servers.hint.offline":       "It has not reported for a while. Its history is still read from the database; its live view, its search and its actions are not available.",
	"servers.hint.waiting":       "It is registered but has not reported yet, so its history is not read.",
	"servers.hint.incompatible":  "It runs a version whose data this server does not read. Update the servers to the same version.",

	"history.now_other_title":      "The live view of the folders of other servers is not available here in this version.",
	"history.now_other_hint":       "not available here",
	"history.restore_other_server": "A restore is made on the server that owns the folder; it is not available from here in this version.",
	"history.on_server":            "Server %s",
	"history.on_server_hint":       "This folder is on another server of the group; its history is read from the database.",

	"versions.recorded":      "Recorded",
	"versions.recorded_hint": "The catalog of that server records this content. Its repository is on the disk of that server and was not checked from here.",

	"explorer.other_servers_note": "The folders of other servers have no live view here in this version; their history is in the Time Machine and on the Backups page.",

	"errors.server_unreachable":  "The history of that server cannot be read right now.",
	"errors.server_not_readable": "The history of that server is not available: it has not reported yet, or it runs a version whose data this server does not read.",
	"errors.other_server":        "This is not available for the folders of other servers in this version.",

	"tour.servers.table.title": "The servers of the group",
	"tour.servers.table.text":  "Every server that shares this database, how it is doing, and how many of its folders you may browse. The Time Machine and Backups buttons show the history of that server's folders.",
	"tour.scope.title":         "Servers",
	"tour.scope.text":          "Switch between this server and the other servers of the group. The backups and the audit log of another server are read from the database; nothing can be started on it from here.",
}

var clusterZH = map[string]string{
	"nav.servers": "服务器",

	"scope.label": "服务器",
	"scope.this":  "本服务器",

	"servers.title":            "服务器",
	"servers.intro":            "共用这个数据库的服务器。每台服务器的备份都保存在它自己的磁盘上；这里可以看到所有服务器，时间机器、备份页和审计日志可以显示任何一台服务器上你有权浏览的文件夹的历史。",
	"servers.name":             "服务器",
	"servers.state":            "状态",
	"servers.version":          "版本",
	"servers.folders":          "文件夹",
	"servers.latest":           "最近备份",
	"servers.seen":             "最近报告",
	"servers.schema":           "数据库 schema",
	"servers.contract":         "（读取契约 %d）",
	"servers.this":             "本服务器",
	"servers.note":             "文件夹按你有权浏览的范围统计。在这个版本里，其他服务器上文件夹的实时视图、搜索和各种操作，都不能从本服务器进行；它们的历史从数据库读取，那台服务器离线时也一样。",
	"servers.no_folders_title": "没有可显示的文件夹",
	"servers.no_folders_text":  "这台服务器上没有你有权浏览的监控文件夹。",
	"servers.unavailable_note": "目前无法列出服务器 %s 的文件夹。",

	"servers.state.online":       "在线",
	"servers.state.offline":      "离线",
	"servers.state.waiting":      "尚未报告",
	"servers.state.incompatible": "版本不兼容",
	"servers.hint.online":        "它刚刚报告过。",
	"servers.hint.offline":       "它有一段时间没有报告了。它的历史仍然从数据库读取；它的实时视图、搜索和各种操作不可用。",
	"servers.hint.waiting":       "它已登记，但还没有报告过，所以不读取它的历史。",
	"servers.hint.incompatible":  "它运行的版本，其数据本服务器无法读取。请把各服务器更新到同一个版本。",

	"history.now_other_title":      "在这个版本里，其他服务器上文件夹的实时视图在这里不可用。",
	"history.now_other_hint":       "这里不可用",
	"history.restore_other_server": "恢复要在拥有该文件夹的服务器上进行；在这个版本里不能从这里进行。",
	"history.on_server":            "服务器 %s",
	"history.on_server_hint":       "这个文件夹在组内的另一台服务器上；它的历史从数据库读取。",

	"versions.recorded":      "已记录",
	"versions.recorded_hint": "那台服务器的目录库记录了这份内容。它的仓库在那台服务器的磁盘上，没有从这里检查。",

	"explorer.other_servers_note": "在这个版本里，其他服务器的文件夹在这里没有实时视图；它们的历史在时间机器和备份页中。",

	"errors.server_unreachable":  "目前无法读取那台服务器的历史。",
	"errors.server_not_readable": "那台服务器的历史不可用：它还没有报告过，或者它运行的版本，其数据本服务器无法读取。",
	"errors.other_server":        "在这个版本里，这对其他服务器的文件夹不可用。",

	"tour.servers.table.title": "组内的服务器",
	"tour.servers.table.text":  "共用这个数据库的每一台服务器、它的状态，以及你有权浏览的文件夹数量。“时间机器”和“备份”按钮显示那台服务器上文件夹的历史。",
	"tour.scope.title":         "服务器",
	"tour.scope.text":          "在本服务器和组内其他服务器之间切换。其他服务器的备份和审计日志从数据库读取；不能从这里在它上面启动任何操作。",
}
