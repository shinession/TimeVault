package server

import (
	"fmt"
	"net/http"
	"strings"
)

// Locale translates message keys for one language.
type Locale struct {
	lang string
}

// T returns the message for key in the locale's language, falling back to
// English and finally to the key itself. Extra arguments are formatted in.
func (l *Locale) T(key string, args ...any) string {
	msg, ok := messages[l.lang][key]
	if !ok {
		msg, ok = messages["en"][key]
	}
	if !ok {
		return key
	}
	if len(args) > 0 {
		return fmt.Sprintf(msg, args...)
	}
	return msg
}

func messageExists(key string) bool {
	_, ok := messages["en"][key]
	return ok
}

// locale picks the language: the cookie wins, then Accept-Language, then English.
func (s *Server) locale(r *http.Request) *Locale {
	if c, err := r.Cookie(cookieLang); err == nil && (c.Value == "en" || c.Value == "zh") {
		return &Locale{lang: c.Value}
	}
	for _, part := range strings.Split(r.Header.Get("Accept-Language"), ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		switch {
		case strings.HasPrefix(tag, "zh"):
			return &Locale{lang: "zh"}
		case strings.HasPrefix(tag, "en"):
			return &Locale{lang: "en"}
		}
	}
	return &Locale{lang: "en"}
}

var messages = map[string]map[string]string{
	"en": {
		"nav.label": "Main navigation", "nav.explorer": "Explorer", "nav.history": "Time Machine", "nav.backups": "Backups",
		"nav.audit": "Audit", "nav.status": "Status", "nav.logout": "Sign out", "nav.settings": "Settings", "nav.login": "Sign in",
		"theme.toggle": "Switch light / dark theme", "lang.label": "Language", "footer.powered": "Powered by TimeVault",

		"login.title": "Sign in", "login.heading": "TimeVault", "login.subtitle": "Enterprise file backup, search and time travel",
		"login.username": "Account (employee ID, name or e-mail)", "login.password": "Password", "login.submit": "Sign in",
		"login.invalid": "Invalid account or password.", "login.throttled": "Too many failed attempts. Wait a few minutes and try again.",
		"login.unavailable": "Signing in is not possible right now: the user accounts cannot be reached. Browsing still works.",
		"login.hint":        "Anyone can browse. You only need to sign in to back up, restore or change settings.",

		"errors.title": "Error", "errors.generic": "The request could not be completed.", "errors.internal": "Something went wrong on the server.",
		"errors.not_found": "The page, folder or recovery point was not found.", "errors.bad_path": "The path is not valid.",
		"errors.csrf":               "The form has expired or is invalid. Reload the page and try again.",
		"errors.forbidden":          "You are not allowed to do this.",
		"errors.restore_disabled":   "Restore is not enabled: no restore folder is configured.",
		"errors.not_restorable":     "This recovery point cannot be restored from (it failed its integrity check).",
		"errors.stale_preview":      "The preview is out of date. Review the new preview and confirm again.",
		"errors.not_confirmed":      "The confirmation is missing or invalid. Review the preview and confirm again.",
		"errors.blocked":            "Some items cannot be restored. Fix the problems or allow a partial restore.",
		"errors.source_unavailable": "The folder cannot be read right now.",
		"errors.source_disabled":    "This monitored folder is switched off. An administrator can switch it on in Settings.",
		"errors.source_busy":        "A backup of this monitored folder is running. Try again when it has finished.",
		"errors.reference":          "Reference", "errors.home": "Back to start",

		"common.source": "Monitored folder", "common.go": "Go", "common.breadcrumb": "Location", "common.load_more": "Load more",
		"common.back": "Back", "common.cancel": "Cancel", "common.select_all": "Select all",

		"explorer.title": "Explorer", "explorer.no_sources_title": "No monitored folder yet",
		"explorer.no_sources_text": "An administrator adds a folder in Settings; then you can browse it here and travel back in time.",
		"explorer.go_backups":      "Go to Backups", "explorer.search_placeholder": "Search files in this folder…", "explorer.search": "Search",
		"explorer.source_down": "The folder cannot be read right now. Time Machine history is still available.",
		"explorer.last_backup": "Last recovery point", "explorer.open_history": "Open in Time Machine",
		"explorer.no_backup_yet": "This folder has not been backed up yet.",
		"explorer.results_for":   "Search results for “%s”", "explorer.results_count": "%d matches", "explorer.clear_search": "Clear search",
		"explorer.no_results": "Nothing matches your search.", "explorer.empty_folder": "This folder is empty.",
		"explorer.hidden_items":  "%d item(s) with names that cannot be backed up are not shown.",
		"explorer.deleted_title": "Deleted since the last backup (%d)",
		"explorer.deleted_hint":  "These items existed at the newest recovery point but are gone from the disk. Open one to restore it.",
		"explorer.deleted_more":  "More deleted items exist than are listed here.",
		"search.unavailable":     "Search is unavailable right now (the search engine did not answer). Browsing and history still work.",
		"search.invalid":         "That search is not valid. Use up to 16 words and avoid control characters.",

		"list.name": "Name", "list.folder": "Folder", "list.size": "Size", "list.modified": "Modified",
		"list.backup_status": "Backup", "list.vs_now": "Compared with now",
		"live.protected": "Protected", "live.changed": "Changed", "live.new": "New", "live.not_captured": "Not captured", "live.deleted": "Deleted",
		"now.same": "Unchanged", "now.modified": "Changed later", "now.deleted": "Deleted later",
		"verify.pending": "Pending", "verify.validated": "Validated", "verify.verified": "Verified", "verify.failed": "Failed",
		"consistency.live_scan": "Live scan", "consistency.vss": "VSS snapshot",

		"history.title": "Time Machine", "history.no_points_title": "No recovery points yet",
		"history.no_points_text": "Run the first backup to create a recovery point you can travel back to.",
		"history.timeline":       "Recovery points", "history.point_title": "Recovery point #%d: %s files, %s",
		"history.now": "Now", "history.now_hint": "Live files", "history.viewing": "Viewing recovery point #%d captured %s",
		"history.live_scan_hint": "Files were read from the live disk, not from a VSS snapshot. A file that changed during the scan was re-read or skipped.",
		"history.older_point":    "Older point", "history.not_restorable": "Not restorable",
		"history.restore_selected": "Restore selected…", "history.restore_folder": "Restore this folder…", "history.restore_all": "Restore everything…",
		"history.restore_disabled":   "Restore is disabled because no restore folder is configured.",
		"history.sign_in_to_restore": "Sign in to restore files",

		"versions.title": "File versions", "versions.intro": "%d recorded version(s). Each one is a real, restorable state captured by a recovery point.",
		"versions.captured": "Captured", "versions.until": "Until", "versions.checksum": "SHA-256", "versions.state": "Content",
		"versions.current": "Current", "versions.folder": "Folder", "versions.content_missing": "Content missing", "versions.available": "Available",
		"versions.restore": "Restore…", "versions.browse_point": "Browse point",

		"backups.title": "Backups", "backups.no_sources_title": "Add your first monitored folder",
		"backups.no_sources_text": "A monitored folder is a local folder TimeVault backs up. An administrator adds it on the Settings page.",
		"backups.manage_sources":  "Manage monitored folders", "backups.sign_in_to_run": "Sign in to back up",
		"backups.disabled": "Switched off", "backups.schedule": "Schedule", "backups.every_minutes": "every %d min",
		"backups.next_run": "next backup", "backups.last_run": "Last run", "backups.run_soon": "any moment now",
		"backups.run": "Back up now", "backups.run_rehash": "Full re-read", "backups.rehash_hint": "Read every file again instead of trusting size and date",
		"backups.running_job": "Backup job #%d is in progress", "backups.latest": "Latest recovery point", "backups.never": "Never",
		"backups.excludes": "Excluded", "backups.add_title": "Add a monitored folder", "backups.field_name": "Name", "backups.field_path": "Folder path",
		"backups.field_excludes": "Exclude patterns (one per line)", "backups.excludes_hint": "Case-insensitive; * and ? match any characters in a name.",
		"backups.add":          "Add monitored folder",
		"backups.source_rules": "The folder must be local, must not overlap the repository, the restore folder or another monitored folder, and is only ever read — never changed.",
		"backups.jobs":         "Recent jobs", "backups.no_jobs": "No jobs yet.", "backups.restore_settings": "Restore",
		"backups.restore_root": "Restores are written below", "backups.restore_hint": "Restores are always written to a folder below this location; the backed-up folders themselves are never changed.",

		"settings.title": "Settings", "settings.sources": "Monitored folders",
		"settings.sources_intro": "TimeVault backs up these folders. Anyone can browse them; only an administrator can change this list.",
		"settings.admin_only":    "Only an administrator can change the monitored folders.",
		"settings.edit":          "Edit", "settings.save": "Save changes", "settings.enabled": "Enabled", "settings.disabled": "Switched off",
		"settings.enabled_label": "Back up this folder (untick to switch it off; its history stays)",
		"settings.schedule":      "Automatic backup", "settings.manual_only": "Manual only", "settings.every_minutes": "Every %d minute(s)",
		"settings.next_run": "Next backup", "settings.next_soon": "any moment now", "settings.last_run": "Last run",
		"settings.schedule_on": "Back up automatically every", "settings.minutes": "minutes (1 to 43200)", "settings.minutes_label": "Interval in minutes",
		"settings.schedule_hint": "The interval is counted from the end of the previous backup. An automatic backup that finds nothing changed records nothing.",
		"settings.path_locked":   "A folder that already has backups cannot be changed. To move it, switch this one off and add the new folder as a new monitored folder.",
		"settings.minutes_error": "The interval must be a whole number of minutes between 1 and 43200.",
		"settings.storage":       "Backup storage", "settings.repository": "Backup folder (repository)", "settings.restore_root": "Restore folder",
		"settings.free_space":   "%d%% free",
		"settings.storage_hint": "Both folders are set in the service settings (TIMEVAULT_REPOSITORY_PATH and TIMEVAULT_RESTORE_ROOT in the .env file or the environment) and change only when the service is restarted.",
		"settings.service":      "Service", "settings.version": "Version", "settings.listen": "Listening on", "settings.database": "Database",
		"settings.directory": "User accounts (Redis)", "settings.local_admin": "Local administrator", "settings.search": "Search (Everything)",
		"settings.session_ttl": "Sign-in lasts", "settings.secure_cookie": "Secure cookie", "settings.env_file": "Settings file",
		"settings.not_configured": "not configured", "settings.none": "none", "settings.yes": "yes", "settings.no": "no",
		"settings.env_hint": "Everything in this card comes from the .env file or the environment and is read when the service starts.",

		"points.kind": "Type", "points.state": "State", "points.captured": "Captured", "points.files": "Files", "points.size": "Size",
		"points.changes": "Changes", "points.not_captured": "Not captured", "points.integrity": "Integrity", "points.browse": "Browse",
		"points.verify": "Verify", "points.verify_hint": "Re-read every stored object and check its checksum",
		"kind.full": "Full", "kind.incremental": "Incremental",
		"state.queued": "Queued", "state.running": "Running", "state.succeeded": "Succeeded", "state.failed": "Failed", "state.canceled": "Canceled",
		"state.building": "Building", "state.committed": "Committed", "state.aborted": "Aborted",
		"phase.": "-", "phase.snapshot_preparing": "Preparing", "phase.scanning": "Scanning and copying", "phase.manifest_validating": "Validating",
		"phase.committing": "Committing", "phase.committed": "Committed", "phase.verifying": "Verifying", "phase.planning": "Planning",
		"phase.restoring": "Restoring", "phase.done": "Done",
		"jobkind.backup": "Backup", "jobkind.verify": "Verification", "jobkind.restore": "Restore",
		"jobs.kind": "Type", "jobs.state": "State", "jobs.created": "Created", "jobs.duration": "Duration", "jobs.by": "Requested by",

		"job.title": "Job", "job.heading": "Job #%d", "job.cancel": "Cancel job", "job.point": "Recovery point", "job.phase": "Phase",
		"job.error": "Error", "job.restore_details": "Restore details", "job.items": "Items", "job.all": "All", "job.failed_only": "Failed only",
		"job.action": "Action", "job.result": "Result", "job.message": "Message", "job.no_items": "No item results to show.",
		"job.log": "Log", "job.no_log": "Nothing logged yet.", "job.sign_in_for_details": "It failed. Sign in to see the reason and the log.",
		"counter.files": "Files", "counter.dirs": "Folders", "counter.added": "Added", "counter.modified": "Modified", "counter.deleted": "Deleted",
		"counter.skipped": "Skipped", "counter.new_objects": "New objects", "counter.bytes_hashed": "Data read", "counter.items": "Items",
		"counter.created": "Created", "counter.overwritten": "Overwritten", "counter.renamed": "Renamed", "counter.failed": "Failed",
		"counter.bytes_written": "Data written",

		"restore.title": "Restore preview", "restore.intro": "Nothing has been written yet. Review what a restore would do, then confirm.",
		"restore.destination": "Destination", "restore.policy": "If a file already exists", "restore.selection": "Selection",
		"restore.summary": "Summary", "restore.files": "Files", "restore.folders": "Folders", "restore.total_size": "Data",
		"restore.skipped": "Skipped", "restore.blocked": "Blocked",
		"restore.missing_objects":   "%d file(s) have no content in the repository and cannot be restored.",
		"restore.not_captured":      "%d file(s) were never captured and cannot be restored.",
		"restore.overwrite_warning": "%d existing file(s) in the destination will be replaced.",
		"restore.problems":          "Problems", "restore.options": "Options", "restore.dest_name": "Destination folder name",
		"restore.dest_hint":      "A folder inside the restore location; it is created if it does not exist. Only letters, digits, spaces and . _ - are sensible here.",
		"restore.allow_partial":  "Restore what is possible even if some items are blocked",
		"restore.update_preview": "Update preview", "restore.changes": "Planned changes", "restore.sample_hint": "First %d items",
		"restore.confirm_title": "Confirm restore", "restore.confirm_text": "Restore %d file(s) and %d folder(s), %s in total.",
		"restore.confirm_overwrite": "I understand that %d existing file(s) will be replaced.",
		"restore.start":             "Start restore", "restore.cannot_start": "Blocked items prevent the restore. Allow a partial restore or choose a different recovery point.",
		"policy.skip": "Keep the existing file (skip)", "policy.rename": "Keep both (restore under a new name)", "policy.overwrite": "Replace the existing file",
		"action.mkdir": "Create folder", "action.dir_exists": "Folder exists", "action.create": "Create", "action.overwrite": "Replace",
		"action.rename": "Restore as copy", "action.skip_same": "Already identical", "action.skip_exists": "Keep existing", "action.blocked": "Blocked",
		"item.ok": "OK", "item.skipped": "Skipped", "item.failed": "Failed",

		"audit.title": "Audit log", "audit.filter": "Filter by action", "audit.all_actions": "All actions", "audit.when": "When", "audit.actor": "User",
		"audit.action": "Action", "audit.target": "Target", "audit.result": "Result", "audit.from": "From", "audit.details": "Details",
		"audit.empty":            "No audit events.",
		"auditaction.auth.login": "Signed in", "auditaction.auth.login_failed": "Sign-in failed", "auditaction.auth.logout": "Signed out",
		"auditaction.source.create": "Monitored folder added", "auditaction.source.update": "Monitored folder changed",
		"auditaction.backup.request": "Backup requested", "auditaction.backup.finish": "Backup finished",
		"auditaction.verify.request": "Verification requested", "auditaction.verify.finish": "Verification finished",
		"auditaction.restore.preview": "Restore previewed", "auditaction.restore.request": "Restore requested",
		"auditaction.restore.finish": "Restore finished", "auditaction.job.cancel": "Job canceled", "auditaction.access.denied": "Access denied",
		"auditresult.ok": "OK", "auditresult.denied": "Denied", "auditresult.failed": "Failed",

		"status.title": "Service status", "status.checked": "Checked at", "status.component": "Component", "status.state": "State", "status.detail": "Detail",
		"status.mvp_note":                "This MVP reads files from the live disk. VSS snapshots and NTFS USN change tracking are planned, not implemented.",
		"status.detail.db_down":          "The database does not answer.",
		"status.detail.repo_free":        "%s — %d%% free",
		"status.detail.search_disabled":  "No search engine is configured.",
		"status.detail.restore_disabled": "No restore folder is configured.",
		"status.detail.vss_disabled":     "Not implemented in the MVP: recovery points are live scans of the file system.",
		"status.detail.usn_disabled":     "Not implemented in the MVP: every backup walks the whole folder.",
		"status.detail.jobs_detail":      "%d running, %d queued",
		"status.detail.directory_ok":     "reachable at %s",
		"status.detail.directory_down":   "Does not answer. Browsing and the local administrator still work; other accounts cannot sign in.",
		"status.detail.directory_off":    "Not configured: only the local administrator can sign in.",
		"component.database":             "Database", "component.repository": "Repository", "component.search": "Search (Everything)",
		"component.directory": "User accounts (Redis)",
		"component.restore":   "Restore", "component.jobs": "Job queue", "component.vss": "VSS snapshots", "component.usn": "USN journal",
		"cstate.ok": "OK", "cstate.degraded": "Degraded", "cstate.disabled": "Not enabled", "cstate.failed": "Failed",

		"flash.source_added": "Monitored folder added.", "flash.source_updated": "Changes saved.",
		"flash.backup_queued": "Backup queued.", "flash.backup_already": "A backup of this folder is already queued or running.",
		"flash.verify_queued": "Verification queued.", "flash.verify_already": "A verification of this point is already queued or running.",
		"flash.job_cancel_requested": "Cancellation requested.", "flash.restore_queued": "Restore queued.",
	},
	"zh": {
		"nav.label": "主导航", "nav.explorer": "文件浏览", "nav.history": "时间机器", "nav.backups": "备份",
		"nav.audit": "审计", "nav.status": "状态", "nav.logout": "退出登录", "nav.settings": "设置", "nav.login": "登录",
		"theme.toggle": "切换浅色 / 深色主题", "lang.label": "语言", "footer.powered": "由 TimeVault 提供支持",

		"login.title": "登录", "login.heading": "TimeVault", "login.subtitle": "企业文件备份、搜索与时间回溯",
		"login.username": "账号（工号、姓名或邮箱）", "login.password": "密码", "login.submit": "登录",
		"login.invalid": "账号或密码错误。", "login.throttled": "失败次数过多，请等待几分钟后再试。",
		"login.unavailable": "暂时无法登录：无法连接用户账号服务。仍可正常浏览。",
		"login.hint":        "任何人都可以浏览。只有备份、恢复或修改设置时才需要登录。",

		"errors.title": "出错了", "errors.generic": "无法完成该请求。", "errors.internal": "服务器发生内部错误。",
		"errors.not_found": "未找到该页面、文件夹或恢复点。", "errors.bad_path": "路径无效。",
		"errors.csrf":               "表单已过期或无效，请刷新页面后重试。",
		"errors.forbidden":          "你没有权限执行此操作。",
		"errors.restore_disabled":   "恢复功能未启用：尚未配置恢复目录。",
		"errors.not_restorable":     "无法从该恢复点恢复（它未通过完整性校验）。",
		"errors.stale_preview":      "预览已过期。请查看新的预览并重新确认。",
		"errors.not_confirmed":      "确认信息缺失或无效。请查看预览并重新确认。",
		"errors.blocked":            "部分项目无法恢复。请先解决问题，或允许部分恢复。",
		"errors.source_unavailable": "当前无法读取监控文件夹。",
		"errors.source_disabled":    "该监控文件夹已停用。管理员可以在“设置”中重新启用。",
		"errors.source_busy":        "该监控文件夹正在备份。请在备份完成后再试。",
		"errors.reference":          "参考编号", "errors.home": "返回首页",

		"common.source": "监控文件夹", "common.go": "确定", "common.breadcrumb": "当前位置", "common.load_more": "加载更多",
		"common.back": "返回", "common.cancel": "取消", "common.select_all": "全选",

		"explorer.title": "文件浏览", "explorer.no_sources_title": "还没有监控文件夹",
		"explorer.no_sources_text": "由管理员在“设置”中添加文件夹后，就可以在这里浏览并回到过去。",
		"explorer.go_backups":      "前往备份页", "explorer.search_placeholder": "在此文件夹中搜索文件…", "explorer.search": "搜索",
		"explorer.source_down": "当前无法读取该文件夹，但时间机器中的历史记录仍可使用。",
		"explorer.last_backup": "最近的恢复点", "explorer.open_history": "在时间机器中打开",
		"explorer.no_backup_yet": "该文件夹尚未备份。",
		"explorer.results_for":   "“%s”的搜索结果", "explorer.results_count": "共 %d 个匹配", "explorer.clear_search": "清除搜索",
		"explorer.no_results": "没有匹配的结果。", "explorer.empty_folder": "此文件夹为空。",
		"explorer.hidden_items":  "有 %d 个项目的名称无法被备份，因此未显示。",
		"explorer.deleted_title": "自上次备份以来已删除（%d）",
		"explorer.deleted_hint":  "这些项目存在于最新的恢复点，但磁盘上已不存在。打开其中一项即可恢复。",
		"explorer.deleted_more":  "已删除的项目多于此处列出的数量。",
		"search.unavailable":     "搜索暂时不可用（搜索引擎没有响应）。浏览和历史记录仍可使用。",
		"search.invalid":         "该搜索无效。最多使用 16 个词，且不要包含控制字符。",

		"list.name": "名称", "list.folder": "所在文件夹", "list.size": "大小", "list.modified": "修改时间",
		"list.backup_status": "备份状态", "list.vs_now": "与当前对比",
		"live.protected": "已保护", "live.changed": "已变更", "live.new": "新增", "live.not_captured": "未能备份", "live.deleted": "已删除",
		"now.same": "未变化", "now.modified": "之后被修改", "now.deleted": "之后被删除",
		"verify.pending": "待处理", "verify.validated": "已验证提交", "verify.verified": "已完整校验", "verify.failed": "校验失败",
		"consistency.live_scan": "实时扫描", "consistency.vss": "VSS 快照",

		"history.title": "时间机器", "history.no_points_title": "还没有恢复点",
		"history.no_points_text": "运行第一次备份，创建一个可以回溯的恢复点。",
		"history.timeline":       "恢复点", "history.point_title": "恢复点 #%d：%s 个文件，%s",
		"history.now": "现在", "history.now_hint": "实时文件", "history.viewing": "正在查看恢复点 #%d，采集于 %s",
		"history.live_scan_hint": "文件读取自实时磁盘，而非 VSS 快照。扫描期间发生变化的文件已被重新读取或跳过。",
		"history.older_point":    "较早的恢复点", "history.not_restorable": "不可恢复",
		"history.restore_selected": "恢复所选项…", "history.restore_folder": "恢复此文件夹…", "history.restore_all": "恢复全部内容…",
		"history.restore_disabled":   "由于未配置恢复目录，恢复功能已禁用。",
		"history.sign_in_to_restore": "登录后可恢复文件",

		"versions.title": "文件版本", "versions.intro": "共记录 %d 个版本。每一个都是由恢复点保存的真实、可恢复的状态。",
		"versions.captured": "采集时间", "versions.until": "有效至", "versions.checksum": "SHA-256", "versions.state": "内容",
		"versions.current": "当前版本", "versions.folder": "文件夹", "versions.content_missing": "内容缺失", "versions.available": "可用",
		"versions.restore": "恢复…", "versions.browse_point": "浏览该恢复点",

		"backups.title": "备份", "backups.no_sources_title": "添加第一个监控文件夹",
		"backups.no_sources_text": "监控文件夹是 TimeVault 要备份的本地文件夹，由管理员在“设置”页面添加。",
		"backups.manage_sources":  "管理监控文件夹", "backups.sign_in_to_run": "登录后可备份",
		"backups.disabled": "已停用", "backups.schedule": "定时备份", "backups.every_minutes": "每 %d 分钟",
		"backups.next_run": "下次备份", "backups.last_run": "上次运行", "backups.run_soon": "即将开始",
		"backups.run": "立即备份", "backups.run_rehash": "完整重读", "backups.rehash_hint": "重新读取每个文件，而不是仅依据大小和日期判断",
		"backups.running_job": "备份任务 #%d 正在进行", "backups.latest": "最新恢复点", "backups.never": "从未备份",
		"backups.excludes": "排除项", "backups.add_title": "添加监控文件夹", "backups.field_name": "名称", "backups.field_path": "文件夹路径",
		"backups.field_excludes": "排除规则（每行一条）", "backups.excludes_hint": "不区分大小写；* 和 ? 可匹配名称中的任意字符。",
		"backups.add":          "添加监控文件夹",
		"backups.source_rules": "文件夹必须位于本机，不能与仓库、恢复目录或其他监控文件夹重叠，并且只会被读取，绝不会被修改。",
		"backups.jobs":         "最近的任务", "backups.no_jobs": "暂无任务。", "backups.restore_settings": "恢复",
		"backups.restore_root": "恢复结果写入", "backups.restore_hint": "恢复始终写入此位置下的文件夹；被备份的文件夹本身绝不会被修改。",

		"settings.title": "设置", "settings.sources": "监控文件夹",
		"settings.sources_intro": "TimeVault 会备份这些文件夹。任何人都可以浏览它们；只有管理员可以修改这个列表。",
		"settings.admin_only":    "只有管理员可以修改监控文件夹。",
		"settings.edit":          "编辑", "settings.save": "保存更改", "settings.enabled": "已启用", "settings.disabled": "已停用",
		"settings.enabled_label": "备份此文件夹（取消勾选即停用；它的历史会保留）",
		"settings.schedule":      "自动备份", "settings.manual_only": "仅手动", "settings.every_minutes": "每 %d 分钟",
		"settings.next_run": "下次备份", "settings.next_soon": "即将开始", "settings.last_run": "上次运行",
		"settings.schedule_on": "自动备份，间隔", "settings.minutes": "分钟（1 到 43200）", "settings.minutes_label": "间隔（分钟）",
		"settings.schedule_hint": "间隔从上一次备份结束时开始计算。自动备份如果没有发现任何变化，则不会产生新的恢复点。",
		"settings.path_locked":   "已有备份的文件夹不能再更改路径。如需迁移，请停用此项，并把新文件夹作为新的监控文件夹添加。",
		"settings.minutes_error": "间隔必须是 1 到 43200 之间的整数分钟。",
		"settings.storage":       "备份存储", "settings.repository": "备份文件夹（仓库）", "settings.restore_root": "恢复文件夹",
		"settings.free_space":   "剩余 %d%%",
		"settings.storage_hint": "这两个文件夹在服务设置中配置（.env 文件或环境变量中的 TIMEVAULT_REPOSITORY_PATH 和 TIMEVAULT_RESTORE_ROOT），重启服务后才会生效。",
		"settings.service":      "服务", "settings.version": "版本", "settings.listen": "监听地址", "settings.database": "数据库",
		"settings.directory": "用户账号（Redis）", "settings.local_admin": "本地管理员", "settings.search": "搜索（Everything）",
		"settings.session_ttl": "登录有效期", "settings.secure_cookie": "安全 Cookie", "settings.env_file": "设置文件",
		"settings.not_configured": "未配置", "settings.none": "无", "settings.yes": "是", "settings.no": "否",
		"settings.env_hint": "此卡片中的内容全部来自 .env 文件或环境变量，在服务启动时读取。",

		"points.kind": "类型", "points.state": "状态", "points.captured": "采集时间", "points.files": "文件数", "points.size": "大小",
		"points.changes": "变化", "points.not_captured": "未能备份", "points.integrity": "完整性", "points.browse": "浏览",
		"points.verify": "校验", "points.verify_hint": "重新读取每个已存储的对象并核对校验值",
		"kind.full": "全量", "kind.incremental": "增量",
		"state.queued": "排队中", "state.running": "运行中", "state.succeeded": "成功", "state.failed": "失败", "state.canceled": "已取消",
		"state.building": "构建中", "state.committed": "已提交", "state.aborted": "已中止",
		"phase.": "-", "phase.snapshot_preparing": "准备中", "phase.scanning": "扫描并复制", "phase.manifest_validating": "校验清单",
		"phase.committing": "提交中", "phase.committed": "已提交", "phase.verifying": "校验中", "phase.planning": "规划中",
		"phase.restoring": "恢复中", "phase.done": "完成",
		"jobkind.backup": "备份", "jobkind.verify": "校验", "jobkind.restore": "恢复",
		"jobs.kind": "类型", "jobs.state": "状态", "jobs.created": "创建时间", "jobs.duration": "耗时", "jobs.by": "发起人",

		"job.title": "任务", "job.heading": "任务 #%d", "job.cancel": "取消任务", "job.point": "恢复点", "job.phase": "阶段",
		"job.error": "错误", "job.restore_details": "恢复详情", "job.items": "项目", "job.all": "全部", "job.failed_only": "仅失败项",
		"job.action": "操作", "job.result": "结果", "job.message": "说明", "job.no_items": "没有可显示的项目结果。",
		"job.log": "日志", "job.no_log": "暂无日志。", "job.sign_in_for_details": "任务失败。登录后可查看原因和日志。",
		"counter.files": "文件", "counter.dirs": "文件夹", "counter.added": "新增", "counter.modified": "修改", "counter.deleted": "删除",
		"counter.skipped": "跳过", "counter.new_objects": "新对象", "counter.bytes_hashed": "已读取数据", "counter.items": "项目",
		"counter.created": "已创建", "counter.overwritten": "已替换", "counter.renamed": "已另存", "counter.failed": "失败",
		"counter.bytes_written": "已写入数据",

		"restore.title": "恢复预览", "restore.intro": "目前尚未写入任何内容。请先查看恢复将执行的操作，然后确认。",
		"restore.destination": "目标位置", "restore.policy": "如果文件已存在", "restore.selection": "所选内容",
		"restore.summary": "概要", "restore.files": "文件", "restore.folders": "文件夹", "restore.total_size": "数据量",
		"restore.skipped": "跳过", "restore.blocked": "受阻",
		"restore.missing_objects":   "有 %d 个文件在仓库中没有内容，无法恢复。",
		"restore.not_captured":      "有 %d 个文件从未被成功备份，无法恢复。",
		"restore.overwrite_warning": "目标位置中已有的 %d 个文件将被替换。",
		"restore.problems":          "问题", "restore.options": "选项", "restore.dest_name": "目标文件夹名称",
		"restore.dest_hint":      "恢复位置下的文件夹，不存在时会自动创建。建议只使用字母、数字、空格以及 . _ -。",
		"restore.allow_partial":  "即使部分项目受阻，也恢复其余可恢复的内容",
		"restore.update_preview": "更新预览", "restore.changes": "计划的变更", "restore.sample_hint": "前 %d 项",
		"restore.confirm_title": "确认恢复", "restore.confirm_text": "将恢复 %d 个文件和 %d 个文件夹，共 %s。",
		"restore.confirm_overwrite": "我明白目标位置中已有的 %d 个文件将被替换。",
		"restore.start":             "开始恢复", "restore.cannot_start": "存在受阻项目，无法恢复。请允许部分恢复，或选择其他恢复点。",
		"policy.skip": "保留已有文件（跳过）", "policy.rename": "两者都保留（以新名称恢复）", "policy.overwrite": "替换已有文件",
		"action.mkdir": "创建文件夹", "action.dir_exists": "文件夹已存在", "action.create": "创建", "action.overwrite": "替换",
		"action.rename": "另存为副本", "action.skip_same": "已完全一致", "action.skip_exists": "保留已有", "action.blocked": "受阻",
		"item.ok": "成功", "item.skipped": "已跳过", "item.failed": "失败",

		"audit.title": "审计日志", "audit.filter": "按操作筛选", "audit.all_actions": "全部操作", "audit.when": "时间", "audit.actor": "用户",
		"audit.action": "操作", "audit.target": "对象", "audit.result": "结果", "audit.from": "来源", "audit.details": "详情",
		"audit.empty":            "暂无审计记录。",
		"auditaction.auth.login": "登录", "auditaction.auth.login_failed": "登录失败", "auditaction.auth.logout": "退出登录",
		"auditaction.source.create": "添加监控文件夹", "auditaction.source.update": "修改监控文件夹",
		"auditaction.backup.request": "请求备份", "auditaction.backup.finish": "备份结束",
		"auditaction.verify.request": "请求校验", "auditaction.verify.finish": "校验结束",
		"auditaction.restore.preview": "恢复预览", "auditaction.restore.request": "请求恢复",
		"auditaction.restore.finish": "恢复结束", "auditaction.job.cancel": "取消任务", "auditaction.access.denied": "访问被拒绝",
		"auditresult.ok": "成功", "auditresult.denied": "已拒绝", "auditresult.failed": "失败",

		"status.title": "服务状态", "status.checked": "检查时间", "status.component": "组件", "status.state": "状态", "status.detail": "详情",
		"status.mvp_note":                "此 MVP 从实时磁盘读取文件。VSS 快照和 NTFS USN 变更跟踪属于后续计划，尚未实现。",
		"status.detail.db_down":          "数据库没有响应。",
		"status.detail.repo_free":        "%s —— 剩余 %d%%",
		"status.detail.search_disabled":  "未配置搜索引擎。",
		"status.detail.restore_disabled": "未配置恢复目录。",
		"status.detail.vss_disabled":     "MVP 未实现：恢复点是对文件系统的实时扫描。",
		"status.detail.usn_disabled":     "MVP 未实现：每次备份都会遍历整个监控文件夹。",
		"status.detail.jobs_detail":      "%d 个运行中，%d 个排队中",
		"status.detail.directory_ok":     "已连接 %s",
		"status.detail.directory_down":   "没有响应。浏览和本地管理员仍可使用；其他账号暂时无法登录。",
		"status.detail.directory_off":    "未配置：只有本地管理员可以登录。",
		"component.database":             "数据库", "component.repository": "备份仓库", "component.search": "搜索（Everything）",
		"component.directory": "用户账号（Redis）",
		"component.restore":   "恢复", "component.jobs": "任务队列", "component.vss": "VSS 快照", "component.usn": "USN 日志",
		"cstate.ok": "正常", "cstate.degraded": "降级", "cstate.disabled": "未启用", "cstate.failed": "故障",

		"flash.source_added": "监控文件夹已添加。", "flash.source_updated": "更改已保存。",
		"flash.backup_queued": "备份已加入队列。", "flash.backup_already": "该文件夹的备份已在排队或进行中。",
		"flash.verify_queued": "校验已加入队列。", "flash.verify_already": "该恢复点的校验已在排队或进行中。",
		"flash.job_cancel_requested": "已请求取消。", "flash.restore_queued": "恢复已加入队列。",
	},
}
