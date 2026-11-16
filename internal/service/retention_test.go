package service

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	"timevault/internal/audit"
	"timevault/internal/backup"
	"timevault/internal/jobs"
)

// setLimit changes the version limit of the harness' source and leaves everything else as it is.
func (h *harness) setLimit(svc *Service, limit *int, confirm bool) error {
	h.t.Helper()
	src, err := svc.Source(context.Background(), h.env.Src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	_, err = svc.UpdateSource(context.Background(), admin, src.ID, backup.SourceRequest{
		Name: src.Name, Excludes: src.Excludes, ScheduleMinutes: src.ScheduleMinutes, MaxVersions: limit, ConfirmPrune: confirm,
	}, src.Enabled)
	return err
}

// versionsOf counts the versions the catalog keeps of one file of the source.
func (h *harness) versionsOf(name string) int {
	return h.count(`SELECT count(*) FROM node_versions v JOIN nodes n ON n.id = v.node_id WHERE n.source_id = $1 AND n.name = $2`, h.env.Src.ID, name)
}

// repositoryFiles counts the object files in the repository folder.
func (h *harness) repositoryFiles() int {
	h.t.Helper()
	n := 0
	err := filepath.WalkDir(filepath.Join(h.env.Repo.Root(), "objects"), func(_ string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			n++
		}
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return n
}

// partialPoints lists which of the source's recovery points are flagged partial, by sequence number order.
func (h *harness) partialPoints() []bool {
	h.t.Helper()
	rows, err := h.env.Pool.Query(context.Background(), `SELECT partial FROM recovery_points WHERE source_id = $1 ORDER BY seq`, h.env.Src.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var out []bool
	for rows.Next() {
		var p bool
		if err := rows.Scan(&p); err != nil {
			h.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

func (h *harness) backupNow(svc *Service, want int) {
	h.t.Helper()
	if _, _, err := svc.StartBackup(context.Background(), admin, h.env.Src.ID, false); err != nil {
		h.t.Fatal(err)
	}
	eventually(h.t, fmt.Sprintf("backup %d", want), func() bool { return h.points() == want && !h.busy() })
}

func TestTheVersionLimitIsAppliedRightAfterEachBackup(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	h.startJobs()
	two := 2
	if err := h.setLimit(svc, &two, false); err != nil {
		t.Fatalf("a limit on a folder without history needs no confirmation: %v", err)
	}

	h.env.Write("stable.txt", "stable")
	for i := 1; i <= 4; i++ {
		h.env.Write("a.txt", fmt.Sprintf("version %d", i))
		h.backupNow(svc, i)
		if want := min(i, 2); h.versionsOf("a.txt") != want {
			t.Fatalf("after backup %d the file has %d versions, want %d", i, h.versionsOf("a.txt"), want)
		}
	}
	if h.versionsOf("stable.txt") != 1 {
		t.Errorf("a file that never changed lost its version: %d", h.versionsOf("stable.txt"))
	}

	// The two oldest contents are gone from the catalog and from the repository folder; what is still needed stays.
	if objects, files := h.count(`SELECT count(*) FROM objects`), h.repositoryFiles(); objects != 3 || files != 3 {
		t.Fatalf("%d objects and %d object files, want 3 (version 3, version 4 and the stable file)", objects, files)
	}
	// The points in which a deleted version was valid no longer show the whole folder.
	if got := h.partialPoints(); fmt.Sprint(got) != "[true true false false]" {
		t.Fatalf("partial points = %v, want the two oldest only", got)
	}

	events, err := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionRetentionPrune})
	if err != nil || len(events) != 2 {
		t.Fatalf("retention.prune events = %v, %v; one for each backup that went over the limit", events, err)
	}
	for _, e := range events {
		if e.Result != audit.ResultOK || !e.HasDuration() || e.Detail["versions"] != float64(1) || e.Detail["files"] != float64(1) ||
			e.Detail["objects_freed"] != float64(1) || e.Detail["limit"] != float64(2) || e.Detail["since_seq"] == nil {
			t.Errorf("event = %+v", e)
		}
	}

	// A backup that changes nothing over the limit deletes nothing and records nothing.
	h.env.Write("stable.txt", "stable but edited")
	h.backupNow(svc, 5)
	if n := h.count(`SELECT count(*) FROM audit_events WHERE action = $1`, audit.ActionRetentionPrune); n != 2 {
		t.Errorf("%d retention.prune events after a backup that deleted nothing, want 2", n)
	}
}

func TestLoweringTheLimitNeedsConfirmationAndStartsACleanup(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	for i := 1; i <= 4; i++ {
		h.env.Write("a.txt", fmt.Sprintf("version %d", i))
		h.env.Backup()
	}
	if h.versionsOf("a.txt") != 4 {
		t.Fatalf("setup: %d versions", h.versionsOf("a.txt"))
	}

	// Without confirmation nothing changes, and the refusal says what would be deleted.
	two := 2
	err := h.setLimit(svc, &two, false)
	var unconfirmed *PruneNotConfirmedError
	if !errors.As(err, &unconfirmed) || unconfirmed.Files != 1 || unconfirmed.Versions != 2 {
		t.Fatalf("lowering without confirmation = %v, want PruneNotConfirmedError for 2 versions of 1 file", err)
	}
	if src, _ := svc.Source(ctx, h.env.Src.ID); src.MaxVersions != nil {
		t.Fatalf("the refused update was stored: %+v", src)
	}
	if h.versionsOf("a.txt") != 4 || len(h.backupJobs()) != 0 {
		t.Fatal("a refused update must not delete anything")
	}

	// A limit that deletes nothing needs none, and it still queues the cleanup (which finds nothing).
	ten := 10
	if err := h.setLimit(svc, &ten, false); err != nil {
		t.Fatalf("a limit above the number of versions = %v", err)
	}
	// Going down from there is the same question again.
	if err := h.setLimit(svc, &two, false); !errors.As(err, &unconfirmed) {
		t.Fatalf("lowering from 10 to 2 without confirmation = %v", err)
	}

	h.startJobs()
	// The cleanup that the limit of 10 queued has nothing to delete; it must be over before the limit changes again.
	eventually(t, "the first cleanup to end", func() bool {
		return h.count(`SELECT count(*) FROM jobs WHERE kind = 'prune' AND state IN ('queued', 'running')`) == 0
	})
	if err := h.setLimit(svc, &two, true); err != nil {
		t.Fatalf("lowering with confirmation = %v", err)
	}
	src, _ := svc.Source(ctx, h.env.Src.ID)
	if src.MaxVersions == nil || *src.MaxVersions != 2 {
		t.Fatalf("source = %+v, want the new limit", src)
	}
	eventually(t, "the cleanup", func() bool { return h.versionsOf("a.txt") == 2 && h.repositoryFiles() == 2 })
	eventually(t, "the cleanup job to end", func() bool {
		return h.count(`SELECT count(*) FROM jobs WHERE kind = 'prune' AND state IN ('queued', 'running')`) == 0
	})
	if h.count(`SELECT count(*) FROM jobs WHERE kind = 'prune' AND state = 'succeeded'`) == 0 {
		t.Error("no cleanup job succeeded")
	}
	if got := h.partialPoints(); fmt.Sprint(got) != "[true true false false]" {
		t.Errorf("partial points = %v", got)
	}
	events, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionRetentionPrune})
	var sawTwo bool
	for _, e := range events {
		sawTwo = sawTwo || e.Detail["versions"] == float64(2) && e.Detail["since_seq"] == nil
	}
	if !sawTwo {
		t.Errorf("no retention.prune event for the cleanup that deleted two versions: %+v", events)
	}
	changes, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionSourceUpdate})
	if len(changes) == 0 || changes[0].Detail["max_versions"] == nil {
		t.Errorf("the change of the limit is not in the audit trail: %+v", changes)
	}

	// Raising the limit, or removing it, deletes nothing and needs no confirmation.
	three := 3
	if err := h.setLimit(svc, &three, false); err != nil {
		t.Fatal(err)
	}
	if err := h.setLimit(svc, nil, false); err != nil {
		t.Fatal(err)
	}
	if src, _ := svc.Source(ctx, h.env.Src.ID); src.MaxVersions != nil {
		t.Errorf("the limit was not removed: %+v", src)
	}
	if _, _, err := svc.StartPrune(ctx, admin, h.env.Src.ID); err == nil {
		t.Error("a cleanup of a folder without a limit must be refused")
	}
}

func TestRestoreAndVerifyAreTurnedAwayWhileOldVersionsAreDeleted(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil) // the runner is not started, so a queued job stays queued
	ctx := context.Background()
	two := 2
	if err := h.setLimit(svc, &two, false); err != nil { // no history yet, so no cleanup is queued
		t.Fatal(err)
	}
	h.env.Write("a.txt", "one")
	point := h.env.Backup()

	// A folder that is being pruned right now.
	svc.pruning.Store(h.env.Src.ID, true)
	if _, _, err := svc.StartVerify(ctx, admin, h.env.Src.ID, point.ID, false); !errors.Is(err, ErrPruneRunning) {
		t.Fatalf("StartVerify while pruning = %v, want ErrPruneRunning", err)
	}
	// Its limit cannot change meanwhile, the rest of it can: the run that is under way would go on with the old one.
	three := 3
	if err := h.setLimit(svc, &three, false); !errors.Is(err, ErrPruneRunning) {
		t.Fatalf("changing the limit while pruning = %v, want ErrPruneRunning", err)
	}
	src, _ := svc.Source(ctx, h.env.Src.ID)
	if _, err := svc.UpdateSource(ctx, admin, src.ID, backup.SourceRequest{Name: "renamed", Excludes: src.Excludes, ScheduleMinutes: src.ScheduleMinutes, MaxVersions: &two}, src.Enabled); err != nil {
		t.Errorf("a change that leaves the limit alone = %v", err)
	}
	svc.pruning.Delete(h.env.Src.ID)
	job, created, err := svc.StartVerify(ctx, admin, h.env.Src.ID, point.ID, false)
	if err != nil || !created {
		t.Fatalf("StartVerify = %+v %v %v", job, created, err)
	}
	if _, err := h.env.Pool.Exec(ctx, `UPDATE jobs SET state = 'succeeded' WHERE id = $1`, job.ID); err != nil {
		t.Fatal(err)
	}

	// A cleanup that is only queued turns nobody away: it can wait for hours for a backup or a verification to end.
	if _, created, err := svc.StartPrune(ctx, admin, h.env.Src.ID); err != nil || !created {
		t.Fatalf("StartPrune = %v %v", created, err)
	}
	queued, created, err := svc.StartVerify(ctx, admin, h.env.Src.ID, point.ID, false)
	if err != nil || !created {
		t.Fatalf("StartVerify with a cleanup queued = %+v %v %v, want it accepted", queued, created, err)
	}
	if _, err := h.env.Pool.Exec(ctx, `UPDATE jobs SET state = 'succeeded' WHERE id = $1`, queued.ID); err != nil {
		t.Fatal(err)
	}

	// A cleanup that is running does.
	if _, err := h.env.Pool.Exec(ctx, `UPDATE jobs SET state = 'running' WHERE kind = 'prune'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.StartVerify(ctx, admin, h.env.Src.ID, point.ID, false); !errors.Is(err, ErrPruneRunning) {
		t.Fatalf("StartVerify with a cleanup running = %v, want ErrPruneRunning", err)
	}
	// A second cleanup request returns the active one.
	if again, created, err := svc.StartPrune(ctx, admin, h.env.Src.ID); err != nil || created || again.Kind != jobs.KindPrune {
		t.Errorf("a second cleanup request = %+v %v %v, want the active one", again, created, err)
	}
}

// A restore or verification that was queued before a deletion of old versions began does not start on the versions
// that are about to go: it waits for the deletion to end.
func TestAVerificationQueuedBeforeADeletionWaitsForItToEnd(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	h.env.Write("a.txt", "one")
	point := h.env.Backup()

	job, _, err := svc.StartVerify(ctx, admin, h.env.Src.ID, point.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	svc.pruning.Store(h.env.Src.ID, true) // the deletion begins while the verification is queued
	h.startJobs()

	time.Sleep(600 * time.Millisecond)
	if got, err := jobs.Get(ctx, h.env.Pool, job.ID); err != nil || got.State != jobs.StateRunning || h.auditCount(audit.ActionVerifyFinish) != 0 {
		t.Fatalf("job = %+v (%v), %d finished verifications; it must wait while the deletion runs", got, err, h.auditCount(audit.ActionVerifyFinish))
	}
	svc.pruning.Delete(h.env.Src.ID)
	eventually(t, "the verification", func() bool { return h.auditCount(audit.ActionVerifyFinish) == 1 })
	eventually(t, "the job to end", func() bool {
		got, err := jobs.Get(ctx, h.env.Pool, job.ID)
		return err == nil && got.State == jobs.StateSucceeded
	})
}

// A cleanup that cannot run after a backup, because a verification of the folder is running, is not forgotten: the
// whole folder is cleaned up by a job that waits in the queue, holding no worker, and starts when the folder is quiet.
func TestACleanupSkippedAfterABackupRunsLaterAsAJob(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	h.startJobs()
	two := 2
	if err := h.setLimit(svc, &two, false); err != nil {
		t.Fatal(err)
	}
	h.env.Write("a.txt", "version 1")
	h.backupNow(svc, 1)
	h.env.Write("a.txt", "version 2")
	h.backupNow(svc, 2)

	// A verification of the folder is running (a stand-in row: the runner leaves it alone).
	var verifyID int64
	if err := h.env.Pool.QueryRow(ctx,
		`INSERT INTO jobs (kind, source_id, state, requested_by, started_at) VALUES ('verify', $1, 'running', 'tester', now()) RETURNING id`,
		h.env.Src.ID).Scan(&verifyID); err != nil {
		t.Fatal(err)
	}

	h.env.Write("a.txt", "version 3")
	h.backupNow(svc, 3) // the backup does not wait for the verification, and does not fail
	if h.versionsOf("a.txt") != 3 {
		t.Fatalf("%d versions after the backup, want the file over its limit for now", h.versionsOf("a.txt"))
	}
	if n := h.count(`SELECT count(*) FROM jobs WHERE kind = 'prune' AND state = 'queued'`); n != 1 {
		t.Fatalf("%d cleanups queued, want one for the whole folder", n)
	}
	if n := h.auditCount(audit.ActionRetentionPrune); n != 0 {
		t.Fatalf("%d retention.prune records; putting a cleanup off is not a failure", n)
	}
	events, err := h.env.Pool.Query(ctx, `SELECT message FROM job_events e JOIN jobs j ON j.id = e.job_id WHERE j.kind = 'backup' AND e.message LIKE '%cleanup of the whole folder is queued%'`)
	if err != nil {
		t.Fatal(err)
	}
	logged := events.Next()
	events.Close()
	if !logged {
		t.Error("the log of the backup does not say that a cleanup was queued")
	}

	// It waits while the verification runs, and goes when it is over.
	time.Sleep(500 * time.Millisecond)
	if h.versionsOf("a.txt") != 3 || h.count(`SELECT count(*) FROM jobs WHERE kind = 'prune' AND state = 'queued'`) != 1 {
		t.Fatal("the cleanup must wait for the verification")
	}
	if _, err := h.env.Pool.Exec(ctx, `UPDATE jobs SET state = 'succeeded', finished_at = now() WHERE id = $1`, verifyID); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the cleanup", func() bool { return h.versionsOf("a.txt") == 2 })
	eventually(t, "the cleanup job to succeed", func() bool {
		return h.count(`SELECT count(*) FROM jobs WHERE kind = 'prune' AND state = 'succeeded'`) == 1
	})
	ev, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionRetentionPrune})
	if len(ev) != 1 || ev[0].Result != audit.ResultOK || ev[0].Detail["versions"] != float64(1) || ev[0].Detail["since_seq"] != nil {
		t.Errorf("retention.prune events = %+v, want one whole-folder cleanup that deleted a version", ev)
	}
}

// Backups of a folder wait in the queue while the old versions of the folder are deleted. They do not fail, and a
// scheduled one does not use up its interval.
func TestBackupsWaitInTheQueueWhileTheOldVersionsOfTheirFolderAreDeleted(t *testing.T) {
	cases := []struct {
		name  string
		start func(svc *Service, id int64) (jobs.Job, bool, error)
	}{
		{"by hand", func(svc *Service, id int64) (jobs.Job, bool, error) {
			return svc.StartBackup(context.Background(), admin, id, false)
		}},
		{"scheduled", func(svc *Service, id int64) (jobs.Job, bool, error) {
			return svc.StartScheduledBackup(context.Background(), id)
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			svc := h.service(nil)
			ctx := context.Background()
			h.startJobs()
			h.env.Write("a.txt", "one")

			// A cleanup of the folder is running (a stand-in row: the runner leaves it alone).
			var pruneID int64
			if err := h.env.Pool.QueryRow(ctx,
				`INSERT INTO jobs (kind, source_id, state, requested_by, started_at) VALUES ('prune', $1, 'running', 'tester', now()) RETURNING id`,
				h.env.Src.ID).Scan(&pruneID); err != nil {
				t.Fatal(err)
			}
			job, created, err := c.start(svc, h.env.Src.ID)
			if err != nil || !created {
				t.Fatalf("start = %+v %v %v", job, created, err)
			}
			time.Sleep(500 * time.Millisecond)
			if got, err := jobs.Get(ctx, h.env.Pool, job.ID); err != nil || got.State != jobs.StateQueued || h.attempts() != 0 || h.ranRecently() {
				t.Fatalf("job = %+v (%v), %d attempts, ran recently %v; the backup must wait in the queue", got, err, h.attempts(), h.ranRecently())
			}

			if _, err := h.env.Pool.Exec(ctx, `UPDATE jobs SET state = 'succeeded', finished_at = now() WHERE id = $1`, pruneID); err != nil {
				t.Fatal(err)
			}
			eventually(t, "the backup", func() bool { return h.points() == 1 })
			eventually(t, "the job to succeed", func() bool {
				got, err := jobs.Get(ctx, h.env.Pool, job.ID)
				return err == nil && got.State == jobs.StateSucceeded
			})
		})
	}
}

func TestPartialPointsStillVerifyAndTheirNewerNeighboursToo(t *testing.T) {
	h := newHarness(t)
	svc := h.service(nil)
	ctx := context.Background()
	h.startJobs()
	two := 2
	if err := h.setLimit(svc, &two, false); err != nil {
		t.Fatal(err)
	}
	h.env.Write("stable.txt", "stable")
	for i := 1; i <= 3; i++ {
		h.env.Write("a.txt", fmt.Sprintf("version %d", i))
		h.backupNow(svc, i)
	}
	if got := h.partialPoints(); fmt.Sprint(got) != "[true false false]" {
		t.Fatalf("partial points = %v", got)
	}

	for seq := 1; seq <= 3; seq++ {
		var pointID int64
		if err := h.env.Pool.QueryRow(ctx, `SELECT id FROM recovery_points WHERE source_id = $1 AND seq = $2`, h.env.Src.ID, seq).Scan(&pointID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := svc.StartVerify(ctx, admin, h.env.Src.ID, pointID, true); err != nil {
			t.Fatal(err)
		}
		want := seq
		eventually(t, fmt.Sprintf("verification %d", seq), func() bool {
			ev, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionVerifyFinish})
			return len(ev) == want
		})
		ev, _ := svc.AuditEvents(ctx, audit.Filter{Action: audit.ActionVerifyFinish})
		if ev[0].Result != audit.ResultOK {
			t.Errorf("verification of point %d = %+v; a partial point is verified by its remaining content, a whole one by its manifest", seq, ev[0])
		}
	}
}
