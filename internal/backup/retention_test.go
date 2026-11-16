package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"timevault/internal/catalog"
)

func contentHash(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// hashes returns the object names of the given contents, sorted.
func hashes(contents ...string) []string {
	out := make([]string, len(contents))
	for i, c := range contents {
		out[i] = contentHash(c)
	}
	slices.Sort(out)
	return out
}

// objectFiles lists the object files that are in the repository directory.
func (e *testEnv) objectFiles() []string {
	e.t.Helper()
	var out []string
	err := filepath.WalkDir(filepath.Join(e.repo.Root(), "objects"), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			out = append(out, d.Name())
		}
		return err
	})
	if err != nil {
		e.t.Fatal(err)
	}
	slices.Sort(out)
	return out
}

// objectRecords lists the objects the catalog has a record of.
func (e *testEnv) objectRecords() []string {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT encode(sha256, 'hex') FROM objects ORDER BY 1`)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, h)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	return out
}

// pointAt returns the committed point with a sequence number.
func (e *testEnv) pointAt(seq int) catalog.Point {
	e.t.Helper()
	pts, err := e.cat.ListPoints(context.Background(), e.src.ID, 100, 0)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, p := range pts {
		if p.Seq == seq {
			return p
		}
	}
	e.t.Fatalf("there is no committed point #%d", seq)
	return catalog.Point{}
}

// leaveUnreferenced backs up a file in the given versions and prunes it to the last one, which leaves the
// content of the others in the repository with nothing referring to it.
func (e *testEnv) leaveUnreferenced(name string, versions ...string) {
	e.t.Helper()
	for _, v := range versions {
		e.write(name, v)
		e.backup()
	}
	if _, err := e.eng.Prune(context.Background(), e.src.ID, catalog.PruneOptions{Max: 1}, nil); err != nil {
		e.t.Fatal(err)
	}
}

func TestPruneAndSweepFreeOnlyWhatNoVersionNeeds(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("twin.txt", "shared content")
	e.write("cold.txt", "never changes")
	e.write("gone.txt", "short lived")
	e.write("hot.txt", "hot v1")
	e.backup()                           // #1
	e.write("hot.txt", "shared content") // the same bytes twin.txt keeps for good
	e.backup()                           // #2
	e.write("hot.txt", "hot v3")
	e.remove("gone.txt")
	e.backup() // #3
	for _, v := range []string{"hot v4", "hot v5", "hot v6"} {
		e.write("hot.txt", v)
		e.backup() // #4 to #6
	}
	everything := hashes("shared content", "never changes", "short lived", "hot v1", "hot v3", "hot v4", "hot v5", "hot v6")
	eq(t, "object files", e.objectFiles(), everything)
	eq(t, "object records", e.objectRecords(), everything)

	rec := &recorder{}
	res, err := e.eng.Prune(ctx, e.src.ID, catalog.PruneOptions{Max: 2}, rec)
	if err != nil || res.Files != 1 || res.Versions != 4 || res.PointsMarked != 4 {
		t.Fatalf("Prune = %+v, %v; want hot.txt to lose its 4 oldest versions", res, err)
	}
	if fmt.Sprint(rec.phases) != fmt.Sprint([]string{PhasePruning}) || !rec.has("deleted 4 old version(s) of 1 file(s)") {
		t.Fatalf("phases %v, events %v", rec.phases, rec.events)
	}
	// Pruning is a catalog operation; the repository keeps everything until it is swept.
	eq(t, "object files after the prune", e.objectFiles(), everything)

	rec = &recorder{}
	swept, err := e.eng.SweepObjects(ctx, rec)
	want := SweepStats{Objects: 3, Bytes: int64(len("hot v1") + len("hot v3") + len("hot v4"))}
	if err != nil || swept != want {
		t.Fatalf("SweepObjects = %+v, %v; want %+v", swept, err, want)
	}
	if fmt.Sprint(rec.phases) != fmt.Sprint([]string{PhaseSweeping}) || !rec.has("removed 3 unreferenced object(s)") {
		t.Fatalf("phases %v, events %v", rec.phases, rec.events)
	}
	// The content of hot v2 is also twin.txt's, so it stays.
	kept := hashes("shared content", "never changes", "short lived", "hot v5", "hot v6")
	eq(t, "object files after the sweep", e.objectFiles(), kept)
	eq(t, "object records after the sweep", e.objectRecords(), kept)

	// A point that lost versions is partial and its manifest is not compared; the others verify as before.
	objectsAt := []int{0, 3, 3, 2, 2, 3, 3}
	for seq := 1; seq <= 6; seq++ {
		p := e.pointAt(seq)
		partial := seq <= 4
		if p.Partial != partial {
			t.Errorf("point #%d: partial = %v, want %v", seq, p.Partial, partial)
		}
		v, err := e.eng.Verify(ctx, p.ID, true, nil)
		if err != nil || !v.OK() || v.ManifestSkipped != partial || v.ManifestOK == partial || v.Objects != int64(objectsAt[seq]) {
			t.Errorf("point #%d: Verify = %+v, %v; want OK, manifest skipped = %v, %d objects", seq, v, err, partial, objectsAt[seq])
		}
		if got := e.pointAt(seq); got.Verification != catalog.VerificationVerified || !got.Restorable() {
			t.Errorf("point #%d after a deep check = %+v", seq, got)
		}
	}
	e.assertManifest(e.pointAt(6))
	if got := e.content(e.entry(6, "hot.txt")); got != "hot v6" {
		t.Fatalf("hot.txt at #6 = %q", got)
	}
	if got := e.content(e.entry(5, "hot.txt")); got != "hot v5" {
		t.Fatalf("hot.txt at #5 = %q", got)
	}
	if got := e.content(e.entry(2, "twin.txt")); got != "shared content" {
		t.Fatalf("twin.txt at #2 = %q", got)
	}
	if _, err := e.cat.ResolveAt(ctx, e.src.ID, 2, "hot.txt"); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("hot.txt at #2 = %v; its version was pruned", err)
	}

	// Nothing is left to do, and the next backup carries on from the newest point as usual.
	if res, err := e.eng.Prune(ctx, e.src.ID, catalog.PruneOptions{Max: 2}, nil); err != nil || res != (catalog.PruneResult{}) {
		t.Fatalf("second Prune = %+v, %v", res, err)
	}
	if swept, err := e.eng.SweepObjects(ctx, nil); err != nil || swept != (SweepStats{}) {
		t.Fatalf("second SweepObjects = %+v, %v", swept, err)
	}
	e.write("cold.txt", "cold changed")
	next := e.backup()
	if next.ModifiedCount != 1 || next.AddedCount != 0 || next.DeletedCount != 0 {
		t.Fatalf("the backup after the prune saw %+v; only cold.txt changed", next)
	}
	if res, err := e.eng.Prune(ctx, e.src.ID, catalog.PruneOptions{Max: 2, SinceSeq: next.Seq}, nil); err != nil || res != (catalog.PruneResult{}) {
		t.Fatalf("Prune after the backup = %+v, %v; no file has more than 2 versions", res, err)
	}
	if v, err := e.eng.Verify(ctx, next.ID, true, nil); err != nil || !v.OK() || !v.ManifestOK {
		t.Fatalf("Verify of the new point = %+v, %v", v, err)
	}
}

func TestPruneNeedsTheLockOfTheSource(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("a.txt", "alpha")
	e.backup()
	lock, err := e.cat.LockSource(ctx, e.src.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	if _, err := e.eng.Prune(ctx, e.src.ID, catalog.PruneOptions{Max: 1}, rec); !errors.Is(err, catalog.ErrBusy) || len(rec.phases) != 0 {
		t.Fatalf("Prune while a backup holds the source = %v (phases %v), want ErrBusy", err, rec.phases)
	}
	lock.Unlock()
	if _, err := e.eng.Prune(ctx, e.src.ID, catalog.PruneOptions{Max: 0}, nil); err == nil {
		t.Fatal("Prune accepted a limit of 0")
	}
	if res, err := e.eng.Prune(ctx, e.src.ID, catalog.PruneOptions{Max: 1}, rec); err != nil || res != (catalog.PruneResult{}) || !rec.has("no old versions") {
		t.Fatalf("Prune with nothing to prune = %+v, %v, events %v", res, err, rec.events)
	}
	// The lock is released again.
	e.backup()
}

// Failed and canceled backups register the objects they wrote before they failed; nothing refers to
// them afterwards, and the sweep takes them out of the repository.
func TestSweepReclaimsWhatACanceledBackupLeftBehind(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("a.txt", "alpha")
	e.write("sub/b.txt", "bravo")
	first := e.backup()

	e.write("a.txt", "alpha changed") // recorded in the catalog before the cancellation
	e.write("sub/b.txt", "bravo changed")
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.eng.hook = func(event, rel string) {
		if event == "opened" && rel == "sub/b.txt" {
			cancel()
		}
	}
	if _, err := e.eng.Run(cancelCtx, e.src.ID, RunOptions{}, nil); err == nil {
		t.Fatal("a canceled backup must fail")
	}
	e.eng.hook = nil
	eq(t, "object files before the sweep", e.objectFiles(), hashes("alpha", "bravo", "alpha changed"))

	swept, err := e.eng.SweepObjects(ctx, nil)
	if err != nil || swept.Objects != 1 || swept.Bytes != int64(len("alpha changed")) || swept.Failed != 0 || swept.More {
		t.Fatalf("SweepObjects = %+v, %v; want the one object the canceled backup left", swept, err)
	}
	eq(t, "object files after the sweep", e.objectFiles(), hashes("alpha", "bravo"))
	eq(t, "object records after the sweep", e.objectRecords(), hashes("alpha", "bravo"))
	if v, err := e.eng.Verify(ctx, first.ID, true, nil); err != nil || !v.OK() {
		t.Fatalf("Verify = %+v, %v", v, err)
	}
	// The next backup writes the content again.
	retry := e.backup()
	if v, err := e.eng.Verify(ctx, retry.ID, true, nil); err != nil || !v.OK() || retry.ModifiedCount != 2 {
		t.Fatalf("the backup after the sweep: %+v; Verify = %+v, %v", retry, v, err)
	}
}

func TestSweepStopsAtItsTimeLimitAndTheNextSweepFinishes(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.leaveUnreferenced("a.txt", "v1", "v2", "v3", "v4")
	eq(t, "object files", e.objectFiles(), hashes("v1", "v2", "v3", "v4"))

	// The limit passes while the first object is being handled.
	clock := time.Now()
	e.eng.now = func() time.Time { return clock }
	e.eng.sweepBatch = 2
	handled := 0
	e.eng.hook = func(event, rel string) {
		if event == "sweeping" {
			if handled++; handled == 1 {
				clock = clock.Add(time.Hour)
			}
		}
	}
	rec := &recorder{}
	first, err := e.eng.SweepObjects(ctx, rec)
	if err != nil || first.Objects != 1 || !first.More || first.Failed != 0 {
		t.Fatalf("the first sweep = %+v, %v; want one object removed and More", first, err)
	}
	if !rec.has("more are left") {
		t.Fatalf("events = %v", rec.events)
	}
	if got := e.objectFiles(); len(got) != 3 {
		t.Fatalf("%d object files left, want 3", len(got))
	}

	// The next sweep starts over and walks past the objects that are gone; with batches of two it fetches more than once.
	e.eng.hook = nil
	second, err := e.eng.SweepObjects(ctx, nil)
	if err != nil || second.Objects != 2 || second.More || second.Failed != 0 {
		t.Fatalf("the second sweep = %+v, %v; want the other two objects", second, err)
	}
	eq(t, "object files", e.objectFiles(), hashes("v4"))
	eq(t, "object records", e.objectRecords(), hashes("v4"))

	// A canceled sweep reports what it did and why it stopped.
	e.leaveUnreferenced("a.txt", "v5", "v6")
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if st, err := e.eng.SweepObjects(canceled, nil); !errors.Is(err, context.Canceled) || st.Objects != 0 {
		t.Fatalf("SweepObjects with a canceled context = %+v, %v", st, err)
	}
	if got := e.objectFiles(); len(got) != 3 {
		t.Fatalf("%d object files left after the canceled sweep, want 3 (v4 and v5 are unreferenced)", len(got))
	}
}

// Deleting objects and storing content do not go together: a backup may find the content of a file already in
// the repository and rely on it, and a sweep may be about to delete exactly that.
func TestBackupsAndSweepsExcludeEachOther(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.leaveUnreferenced("a.txt", "alpha one", "alpha two") // "alpha one" is in the repository, unreferenced
	eq(t, "object files", e.objectFiles(), hashes("alpha one", "alpha two"))

	// A sweep is refused while a backup is running, and deletes nothing.
	e.write("b.txt", "bravo")
	var refused error
	e.eng.hook = func(event, rel string) {
		if event == "opened" && rel == "b.txt" {
			_, refused = e.eng.SweepObjects(ctx, nil)
		}
	}
	e.backup()
	e.eng.hook = nil
	if !errors.Is(refused, ErrSweepBusy) {
		t.Fatalf("SweepObjects during a backup = %v, want ErrSweepBusy", refused)
	}
	eq(t, "object files after the refused sweep", e.objectFiles(), hashes("alpha one", "alpha two", "bravo"))

	// A backup that starts during a sweep waits for it. It is about to store "alpha one" again, which the
	// sweep is deleting; it must find the file gone and write it anew instead of relying on the old one.
	e.write("a.txt", "alpha one")
	type outcome struct {
		point catalog.Point
		err   error
	}
	done := make(chan outcome, 1)
	rep := &waitingReporter{waiting: make(chan struct{}, 1)}
	var started sync.Once
	e.eng.hook = func(event, rel string) {
		if event != "sweeping" {
			return
		}
		started.Do(func() {
			go func() {
				p, err := e.eng.Run(ctx, e.src.ID, RunOptions{}, rep)
				done <- outcome{p, err}
			}()
			receive(t, rep.waiting, "the backup to say that it waits for the sweep")
			select {
			case o := <-done:
				t.Errorf("the backup ended during the sweep: %+v", o)
				done <- o
			default:
			}
		})
	}
	swept, err := e.eng.SweepObjects(ctx, nil)
	if err != nil || swept.Objects != 1 {
		t.Fatalf("SweepObjects = %+v, %v", swept, err)
	}
	o := receive(t, done, "the backup to run after the sweep")
	e.eng.hook = nil
	if o.err != nil {
		t.Fatalf("the backup that waited for the sweep failed: %v", o.err)
	}
	if got := e.content(e.entry(o.point.Seq, "a.txt")); got != "alpha one" {
		t.Fatalf("a.txt = %q", got)
	}
	if v, err := e.eng.Verify(ctx, o.point.ID, true, nil); err != nil || !v.OK() {
		t.Fatalf("the backup stored content the sweep had deleted: Verify = %+v, %v", v, err)
	}
	eq(t, "object files", e.objectFiles(), hashes("alpha one", "alpha two", "bravo"))
}

// waitingReporter tells when a run reports that it waits for a sweep.
type waitingReporter struct {
	recorder
	waiting chan struct{}
}

func (r *waitingReporter) Event(level, msg string) {
	r.recorder.Event(level, msg)
	if strings.Contains(msg, "waiting for a sweep") {
		select {
		case r.waiting <- struct{}{}:
		default:
		}
	}
}

// receive waits for a value, and fails the test instead of hanging when it does not come.
func receive[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(30 * time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
	var none T
	return none
}

func TestGateSharesRunsAndGivesASweepTheWholeRepository(t *testing.T) {
	ctx := context.Background()
	var g gate
	if err := g.enter(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.enter(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if g.tryExclusive() {
		t.Fatal("a sweep got the gate while two runs were in it")
	}
	g.leave()
	if g.tryExclusive() {
		t.Fatal("a sweep got the gate while a run was in it")
	}
	g.leave()
	if !g.tryExclusive() {
		t.Fatal("a sweep was refused an idle gate")
	}
	if g.tryExclusive() {
		t.Fatal("two sweeps got the gate")
	}

	// A run waits for the sweep, gives up when its context ends, and goes on when the sweep does.
	waiting := make(chan struct{}, 2)
	told := func() { waiting <- struct{}{} }
	giveUp, cancel := context.WithCancel(ctx)
	errs := make(chan error, 2)
	go func() { errs <- g.enter(giveUp, told) }()
	receive(t, waiting, "the run to wait for the sweep")
	cancel()
	if err := receive(t, errs, "the run to give up"); !errors.Is(err, context.Canceled) {
		t.Fatalf("a run that gave up waiting got %v", err)
	}
	go func() { errs <- g.enter(ctx, told) }()
	receive(t, waiting, "the second run to wait for the sweep")
	select {
	case err := <-errs:
		t.Fatalf("a run got into the gate during a sweep: %v", err)
	default:
	}
	g.release()
	if err := receive(t, errs, "the run to go on after the sweep"); err != nil {
		t.Fatalf("a run that waited for the sweep got %v", err)
	}
	if g.tryExclusive() {
		t.Fatal("a sweep got the gate while the run that waited is in it")
	}
	g.leave()
	if !g.tryExclusive() {
		t.Fatal("the gate is not free after everyone left")
	}
}

// A prune that ends while a point is being checked turns it partial under the check. The check runs again,
// as the partial point it has become, instead of failing the point for a manifest that was right when it started.
func TestVerifyChecksAgainAPointThatWasPrunedWhileItWasRead(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for _, v := range []string{"one", "two", "three", "four"} {
		e.write("a.txt", v)
		e.backup()
	}
	second := e.pointAt(2)

	var once sync.Once
	e.eng.hook = func(event, rel string) {
		if event != "verifying" {
			return
		}
		once.Do(func() {
			if _, err := e.eng.Prune(ctx, e.src.ID, catalog.PruneOptions{Max: 1}, nil); err != nil {
				t.Error(err)
			}
		})
	}
	res, err := e.eng.Verify(ctx, second.ID, true, nil)
	e.eng.hook = nil
	if err != nil || !res.OK() || !res.ManifestSkipped || len(res.Problems) != 0 {
		t.Fatalf("Verify = %+v, %v; want a clean check of the partial point", res, err)
	}
	if got := e.pointAt(2); !got.Partial || got.Verification != catalog.VerificationVerified {
		t.Fatalf("the point afterwards = %+v", got)
	}

	// A real mismatch of a point that is not partial is still a failure.
	if _, err := e.pool.Exec(ctx, `UPDATE node_versions SET size = size + 1 WHERE first_point = 4`); err != nil {
		t.Fatal(err)
	}
	if res, err := e.eng.Verify(ctx, e.pointAt(4).ID, false, nil); err != nil || res.OK() || res.ManifestOK || res.ManifestSkipped {
		t.Fatalf("Verify of a tampered point = %+v, %v", res, err)
	}
}
