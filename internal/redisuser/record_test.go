package redisuser

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"testing"
)

const secretPassword = "S3cret-Pw!-fake"

func TestRecordNeverRevealsThePassword(t *testing.T) {
	u := alice()
	u.password = secretPassword
	s, _ := newDirectory(t, u)
	rec, err := s.Lookup(ctx, "E1001")
	if err != nil || !rec.CheckPassword(secretPassword) {
		t.Fatalf("got %v, %v", rec, err)
	}

	var text, jsonLog bytes.Buffer
	slog.New(slog.NewTextHandler(&text, nil)).Info("login", "user", rec, "pointer", &rec)
	slog.New(slog.NewJSONHandler(&jsonLog, nil)).Info("login", "user", rec)
	marshalled, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var anything any = rec // vet rejects %d for a struct with string fields, which is the point of this case
	outputs := map[string]string{
		"%v":                 fmt.Sprintf("%v", rec),
		"%+v":                fmt.Sprintf("%+v", rec),
		"%#v":                fmt.Sprintf("%#v", rec),
		"%s":                 fmt.Sprintf("%s", rec),
		"%q":                 fmt.Sprintf("%q", rec),
		"%x":                 fmt.Sprintf("%x", rec),
		"%d":                 fmt.Sprintf("%d", anything),
		"%v of a pointer":    fmt.Sprintf("%v", &rec),
		"%+v inside":         fmt.Sprintf("%+v", struct{ R Record }{rec}),
		"%+v unexported":     fmt.Sprintf("%+v", struct{ r Record }{rec}),
		"%#v unexported":     fmt.Sprintf("%#v", struct{ r Record }{rec}),
		"Sprint":             fmt.Sprint(rec, &rec),
		"String":             rec.String(),
		"GoString":           rec.GoString(),
		"text log":           text.String(),
		"JSON log":           jsonLog.String(),
		"JSON":               string(marshalled),
		"error with %v":      fmt.Errorf("lookup: %v", rec).Error(),
		"error with %+v ptr": fmt.Errorf("lookup: %+v", &rec).Error(),
	}
	for name, out := range outputs {
		if strings.Contains(out, secretPassword) || strings.Contains(out, "Secret") {
			t.Errorf("%s shows the password: %s", name, out)
		}
	}
	if !strings.Contains(text.String(), "E1001") {
		t.Errorf("the log value should identify the user: %s", text.String())
	}
}

// The text forms show who the account is and nothing personal.
func TestRecordSummary(t *testing.T) {
	rec := Record{Ref: "app_user_x", ECode: "E1", Name: "Pat Kim", Email: "pat@example.test", Type: "admin", Activated: true}
	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("x", "user", rec)
	for _, out := range []string{rec.String(), rec.GoString(), fmt.Sprintf("%v", rec), fmt.Sprintf("%+v", rec), fmt.Sprintf("%#v", rec), logged.String()} {
		for _, personal := range []string{"Pat", "pat@", "example.test", "app_user_x"} {
			if strings.Contains(out, personal) {
				t.Errorf("%q contains %q", out, personal)
			}
		}
		if !strings.Contains(out, "E1") || !strings.Contains(out, "admin") {
			t.Errorf("%q lacks the ee_code or the type", out)
		}
	}
}

func TestRecordMethods(t *testing.T) {
	var zero Record
	if zero.HasPassword() || zero.CheckPassword("") || zero.CheckPassword("0") || zero.IsAdmin() {
		t.Error("the zero Record has no password and no rights")
	}
	for typ, want := range map[string]bool{"admin": true, "Admin": true, " ADMIN ": true, "user": false, "administrator": false, "": false} {
		if got := (Record{Type: typ}).IsAdmin(); got != want {
			t.Errorf("IsAdmin(%q) = %v", typ, got)
		}
	}
	u := alice()
	rec := decodeHash(u.key(), u.hash(), false).rec
	if !rec.HasPassword() || !rec.CheckPassword("fake-pw-1") || rec.CheckPassword("fake-pw-") || rec.CheckPassword("fake-pw-12") {
		t.Error("CheckPassword must compare the whole password")
	}
	other := decodeHash(u.key(), u.hash(), false).rec
	if rec != other {
		t.Error("the same hash must decode to the same Record")
	}
}
