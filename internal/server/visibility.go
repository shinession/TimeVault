package server

import (
	"context"
	"fmt"
	"path/filepath"

	"timevault/internal/access"
	"timevault/internal/audit"
	"timevault/internal/auth"
	"timevault/internal/catalog"
	"timevault/internal/jobs"
	"timevault/internal/pathutil"
	"timevault/internal/restore"
	"timevault/internal/service"
)

// restoreFolderLabel stands for the restore folder in a path that cannot show its display root.
const restoreFolderLabel = "[restore folder]"

// viewer decides what a visitor may see of the server's own folders. People reach a monitored folder over the
// network, so they know it by its display folder (\\server\share\folder); the folder on the server's disk is for
// administrators. For everybody else, texts that quote a real folder (error messages, logs, audit details, health
// details) have it replaced by the display folder, and the backup folder is not named at all.
//
// The map of the folders is read from the database on first use, so a page without such texts costs nothing. A
// viewer belongs to one request and is not safe for concurrent use.
type viewer struct {
	physical bool
	s        *Server
	ctx      context.Context
	sess     auth.Session
	paths    *service.PathMap
	loaded   bool
	// full is the folders the visitor has full control of, read on first use (fullLoaded).
	full       map[int64]bool
	fullLoaded bool
}

func (s *Server) viewerFor(ctx context.Context, sess auth.Session) *viewer {
	return &viewer{physical: sess.Can(auth.PermManage), s: s, ctx: ctx, sess: sess}
}

// pathMap is nil when the map could not be built; the texts that need it are then left out rather than shown. It
// knows only the folders the visitor may browse (and the backup and restore folders): a text that quotes the real
// folder of any other is not one the visitor is shown, and the map must not be a way to learn that such a folder
// exists or what it is called.
func (v *viewer) pathMap() *service.PathMap {
	if !v.loaded {
		v.loaded = true
		paths, err := v.s.svc.PathMapFor(v.ctx, func(src catalog.Source) bool { return levelOn(v.sess, src) >= access.Browse })
		if err != nil {
			v.s.log.Warn("the map of the server's folders could not be built; texts that may quote them are left out", "error", err)
			return nil
		}
		v.paths = paths
	}
	return v.paths
}

// text hides the real folders in a message.
func (v *viewer) text(s string) string {
	if v.physical || s == "" {
		return s
	}
	paths := v.pathMap()
	if paths == nil {
		return ""
	}
	return paths.Mask(s)
}

// locate says where the folder or file rel of a source is, for the location line of a page: the display folder
// for everybody, and for administrators the folder on the server's disk too (alone when there is no display folder).
func (p *Page) locate(src catalog.Source, rel string) {
	if u, err := pathutil.ParseUNC(src.DisplayPath); err == nil {
		p.Location = u.Join(rel)
	}
	if !p.ShowPhysical() {
		return
	}
	real := filepath.Join(src.RootPath, filepath.FromSlash(rel))
	if p.Location == "" {
		p.Location = real
		return
	}
	p.LocationReal = real
}

// restoreRoot is the restore folder as the visitor may see it: the display root for everybody (empty when there is
// none, so the page tells people to ask an administrator), and the real folder for administrators.
func (v *viewer) restoreRoot() (shown, real string) {
	root, display := v.s.svc.RestoreRoot(), v.s.svc.RestoreDisplayRoot()
	switch {
	case root == "":
		return "", ""
	case display != "" && v.physical:
		return display, root
	case display != "":
		return display, ""
	case v.physical:
		return root, ""
	}
	return "", ""
}

// destination is where a restore into the folder name below the restore folder writes: shown to everybody and,
// for administrators who also see a display folder, the folder on the server's disk as well.
func (v *viewer) destination(name string) (shown, real string) {
	physical := filepath.Join(v.s.svc.RestoreRoot(), name)
	display := ""
	if v.s.svc.RestoreDisplayRoot() != "" {
		if paths := v.pathMap(); paths != nil {
			display = paths.RestoreDisplay(name)
		}
	}
	switch {
	case display != "" && v.physical:
		return display, physical
	case display != "":
		return display, ""
	case v.physical:
		return physical, ""
	}
	return restoreFolderLabel + `\` + name, ""
}

func (v *viewer) job(j jobs.Job) jobs.Job {
	j.Error = v.text(j.Error)
	return j
}

func (v *viewer) events(in []jobs.Event) []jobs.Event {
	if v.physical {
		return in
	}
	out := make([]jobs.Event, len(in))
	for i, e := range in {
		e.Message = v.text(e.Message)
		out[i] = e
	}
	return out
}

func (v *viewer) point(p catalog.Point) catalog.Point {
	p.FailureReason = v.text(p.FailureReason)
	return p
}

// entries hides the real folders in the reasons files were not captured.
func (v *viewer) entries(in []catalog.Entry) {
	if v.physical {
		return
	}
	for i := range in {
		in[i].SkipReason = v.text(in[i].SkipReason)
	}
}

func (v *viewer) versions(in []catalog.VersionInfo) {
	if v.physical {
		return
	}
	for i := range in {
		in[i].SkipReason = v.text(in[i].SkipReason)
	}
}

func (v *viewer) restoreItems(in []restore.StoredItem) []restore.StoredItem {
	if v.physical {
		return in
	}
	out := make([]restore.StoredItem, len(in))
	for i, it := range in {
		it.Message = v.text(it.Message)
		out[i] = it
	}
	return out
}

// problems hides the real folders in the reasons a restore preview gives for the items it cannot restore, and in the
// paths of renamed items.
func (v *viewer) problems(in []restore.Item) []restore.Item {
	if v.physical {
		return in
	}
	out := make([]restore.Item, len(in))
	for i, it := range in {
		it.Problem = v.text(it.Problem)
		it.Target = v.text(it.Target)
		out[i] = it
	}
	return out
}

func (v *viewer) audit(in []audit.Event) []audit.Event {
	if v.physical {
		return in
	}
	out := make([]audit.Event, len(in))
	for i, e := range in {
		if len(e.Detail) > 0 {
			e.Detail = v.detail(e.Detail)
			if _, has := e.Detail[accessDetail]; has && !v.mayReadAccess(e.SourceID) {
				e.Detail[accessDetail] = accessHidden
			}
		}
		out[i] = e
	}
	return out
}

// accessDetail is the key of an audit detail that holds who may do what with a folder (the lists of a new folder, the
// old and the new lists of a change), and accessHidden what is shown in its place to those who may not read it.
const (
	accessDetail = "access"
	accessHidden = "[hidden]"
)

// mayReadAccess reports whether the visitor may read who may do what with the folder of an audit event: as on the
// settings page and in the API, only those who have full control of it. The folders are read the first time it
// is asked; when they cannot be read nobody is told.
func (v *viewer) mayReadAccess(source *int64) bool {
	if source == nil {
		return false
	}
	if !v.fullLoaded {
		v.fullLoaded = true
		visible, err := v.s.visibleSources(v.ctx, v.sess)
		if err != nil {
			v.s.log.Warn("the folders of the visitor could not be read; who may do what is left out of the audit details", "error", err)
			return false
		}
		v.full = map[int64]bool{}
		for _, src := range visible {
			if levelOn(v.sess, src.Source) >= access.Full {
				v.full[src.ID] = true
			}
		}
	}
	return v.full[*source]
}

// detail masks every string of an audit detail. Without the folder map nothing of it is shown.
func (v *viewer) detail(in map[string]any) map[string]any {
	paths := v.pathMap()
	if paths == nil {
		return nil
	}
	out, _ := paths.MaskValue(in).(map[string]any)
	return out
}

// health hides the backup folder and the real restore folder in the status of the components. The free space of the
// repository stays: it tells everybody whether backups can go on. The queued and running jobs are those of the
// folders the visitor may browse: the count of all of them would tell that something is going on in a folder that
// is not theirs.
func (v *viewer) health(rep service.HealthReport) service.HealthReport {
	if v.physical {
		return rep
	}
	comps := make([]service.Component, len(rep.Components))
	for i, c := range rep.Components {
		switch {
		case c.Name == "repository" && c.Key == "repo_free" && len(c.Args) == 2:
			c.Key, c.Args = "repo_free_pct", c.Args[1:]
			c.Detail = fmt.Sprintf("%d%% free", c.Args[0])
		case c.Name == "jobs" && c.Key == "jobs_detail" && len(c.Args) == 2:
			queued, running := v.visibleJobCounts()
			rep.Queued, rep.Running = queued, running
			c.Args = []any{running, queued}
			c.Detail = fmt.Sprintf("%d running, %d queued", running, queued)
		default:
			c.Detail = v.text(c.Detail)
		}
		comps[i] = c
	}
	rep.Components = comps
	return rep
}

// visibleJobCounts counts the queued and running jobs of the folders the visitor may browse. When they cannot be
// counted, no job is claimed: the numbers are not worth a wrong answer that shows more than the visitor may see.
func (v *viewer) visibleJobCounts() (queued, running int) {
	visible, err := v.s.visibleSources(v.ctx, v.sess)
	if err != nil {
		v.s.log.Warn("the jobs of the visible folders could not be counted", "error", err)
		return 0, 0
	}
	only, ids := restrictTo(v.sess, visible)
	q, r, err := v.s.svc.JobCounts(v.ctx, jobs.Filter{OnlySources: only, SourceIDs: ids})
	if err != nil {
		v.s.log.Warn("the jobs of the visible folders could not be counted", "error", err)
		return 0, 0
	}
	return q, r
}
