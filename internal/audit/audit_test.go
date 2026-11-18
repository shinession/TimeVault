package audit_test

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"timevault/internal/audit"
	"timevault/internal/db/dbtest"
)

func addSource(t *testing.T, pool *pgxpool.Pool, name string) int64 {
	t.Helper()
	var id int64
	err := pool.QueryRow(context.Background(),
		`INSERT INTO sources (name, root_path, root_key, created_by) VALUES ($1, $1, $1, 'tester') RETURNING id`, name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Whoever may see only some folders is shown the events of those folders and the events that belong to none (sign-ins
// and the like). A cancellation that an older version recorded without its folder belongs to the folder of its job.
func TestListCanBeLimitedToSomeSources(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.New(t)
	log := audit.New(pool, slog.New(slog.DiscardHandler))
	a, b := addSource(t, pool, "a"), addSource(t, pool, "b")
	var jobOfA int64
	if err := pool.QueryRow(ctx,
		`INSERT INTO jobs (kind, source_id, state, params, requested_by) VALUES ('backup', $1, 'succeeded', '{}', 'tester') RETURNING id`, a).Scan(&jobOfA); err != nil {
		t.Fatal(err)
	}
	record := func(e audit.Event) {
		t.Helper()
		e.Actor = "tester"
		if err := log.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	record(audit.Event{Action: audit.ActionLogin, TargetType: "user", TargetID: "tester"})
	record(audit.Event{Action: audit.ActionBackupRequest, TargetType: "source", TargetID: "a", SourceID: &a})
	record(audit.Event{Action: audit.ActionBackupRequest, TargetType: "source", TargetID: "b", SourceID: &b})
	record(audit.Event{Action: audit.ActionJobCancel, TargetType: "job", TargetID: fmt.Sprint(jobOfA)})
	record(audit.Event{Action: audit.ActionJobCancel, TargetType: "job", TargetID: "987654"})
	record(audit.Event{Action: audit.ActionJobCancel, TargetType: "job", TargetID: "not a number"})

	// what lists the events oldest first as "action:target".
	what := func(f audit.Filter) string {
		t.Helper()
		events, err := log.List(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for i := len(events) - 1; i >= 0; i-- {
			out = append(out, events[i].Action+":"+events[i].TargetID)
		}
		return strings.Join(out, " ")
	}
	login := "auth.login:tester"
	reqA, reqB := "backup.request:a", "backup.request:b"
	cancelA := "job.cancel:" + fmt.Sprint(jobOfA)
	for _, tc := range []struct {
		name string
		f    audit.Filter
		want string
	}{
		{"everybody sees everything", audit.Filter{}, strings.Join([]string{login, reqA, reqB, cancelA, "job.cancel:987654", "job.cancel:not a number"}, " ")},
		{"a", audit.Filter{OnlySources: true, SourceIDs: []int64{a}}, strings.Join([]string{login, reqA, cancelA}, " ")},
		{"b", audit.Filter{OnlySources: true, SourceIDs: []int64{b}}, strings.Join([]string{login, reqB}, " ")},
		{"a and b", audit.Filter{OnlySources: true, SourceIDs: []int64{a, b}}, strings.Join([]string{login, reqA, reqB, cancelA}, " ")},
		{"no folder", audit.Filter{OnlySources: true}, login},
		{"a folder that is not there", audit.Filter{OnlySources: true, SourceIDs: []int64{987654}}, login},
		{"a, cancellations only", audit.Filter{OnlySources: true, SourceIDs: []int64{a}, Action: audit.ActionJobCancel}, cancelA},
		{"b, cancellations only", audit.Filter{OnlySources: true, SourceIDs: []int64{b}, Action: audit.ActionJobCancel}, ""},
	} {
		if got := what(tc.f); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
