package weblog

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"timevault/internal/audit"
)

const (
	// messagePrefix starts every message, the way the other applications name themselves (QAQC: lcs).
	messagePrefix = "TimeVault: "
	// Guest is the user of a visitor who has not signed in.
	Guest = "guest"
	// SchedulerUser is the user of what the scheduler does.
	SchedulerUser = "scheduler"
)

func userOf(user string) string {
	if user == "" {
		return Guest
	}
	return user
}

// seconds writes a duration the way the other applications do: 0.112(s).
func seconds(d time.Duration) string { return fmt.Sprintf("%.3f(s)", d.Seconds()) }

// Page is the line for a page that somebody opened: the route that matched (never the address as typed, which may
// hold a path or a search), who opened it, and how long it took to build. The level follows the status: info for a
// page, warning for a refusal or a page that was not there.
func Page(ip, user, route string, status int, took time.Duration) Entry {
	level := slog.LevelInfo
	if status >= 400 {
		level = slog.LevelWarn
	}
	return Entry{
		IP: ip, Status: status, Level: level, Class: ClassPage,
		Message: messagePrefix + route + " user=" + userOf(user) + " " + seconds(took),
	}
}

// Failure is the line for a request that failed on the server (status 500 and up). ref is the reference that the
// visitor was shown with the error page and that the log of the service holds with the details; the error text itself
// may name a path and is not written here.
func Failure(ip, user, route string, status int, ref string) Entry {
	m := messagePrefix + "error " + route + " user=" + userOf(user)
	if ref != "" {
		m += " ref=" + ref
	}
	return Entry{IP: ip, Message: m, Status: status, Level: slog.LevelError}
}

// Audit is the line for an audit event: the action, where it concerns a folder or another target, who did it and how
// long it took. The detail of the event is left out, because it may hold paths and names; only the route of a refusal
// and the stage of a program update are taken from it, and the name that was typed at a failed sign-in is never written.
// The result decides the status and the level: ok is 200 and info, denied is 403 and warning, failed is 500 and error.
func Audit(e audit.Event) Entry {
	var m strings.Builder
	m.WriteString(messagePrefix + e.Action)
	switch e.Action {
	case audit.ActionAccessDenied:
		for _, k := range []string{"method", "path"} {
			if v, _ := e.Detail[k].(string); v != "" {
				name := k
				if k == "path" {
					name = "route"
				}
				m.WriteString(" " + name + "=" + v)
			}
		}
	case audit.ActionAppUpdate:
		for _, k := range []string{"stage", "from", "to"} {
			if v, _ := e.Detail[k].(string); v != "" {
				m.WriteString(" " + k + "=" + v)
			}
		}
	}
	if e.SourceID != nil {
		fmt.Fprintf(&m, " source=%d", *e.SourceID)
	}
	if e.TargetType != "" && e.TargetType != "source" && e.TargetID != "" {
		m.WriteString(" " + e.TargetType + "=" + e.TargetID)
	}
	actor := e.Actor
	switch {
	case e.Action == audit.ActionLoginFailed:
		actor = "-"
	case actor == "":
		actor = Guest
	}
	m.WriteString(" user=" + actor)
	if e.HasDuration() {
		m.WriteString(" " + seconds(e.Duration))
	}
	status, level := 200, slog.LevelInfo
	switch e.Result {
	case audit.ResultDenied:
		status, level = 403, slog.LevelWarn
	case audit.ResultFailed:
		status, level = 500, slog.LevelError
	}
	return Entry{IP: e.RemoteAddr, Message: m.String(), Status: status, Level: level}
}

// Started is the line written when the service starts.
func Started(version string) Entry {
	return Entry{Message: messagePrefix + "started version=" + version, Level: slog.LevelInfo}
}

// Stopped is the line written when the service stops.
func Stopped() Entry { return Entry{Message: messagePrefix + "stopped", Level: slog.LevelInfo} }

// Scheduler is a line about what the scheduler did, such as "scheduler.queued", with the monitored folder it concerns
// (sourceID, left out when zero). The status is 500 for a level of warning and above.
func Scheduler(what string, sourceID int64, level slog.Level) Entry {
	status := 200
	if level >= slog.LevelWarn {
		status = 500
	}
	m := messagePrefix + what
	if sourceID > 0 {
		m += fmt.Sprintf(" source=%d", sourceID)
	}
	return Entry{Message: m + " user=" + SchedulerUser, Status: status, Level: level}
}
