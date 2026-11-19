package server

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"timevault/internal/auth"
	"timevault/internal/weblog"
	"timevault/web"
)

type unitSink struct {
	mu  sync.Mutex
	got []weblog.Entry
}

func (u *unitSink) Write(e weblog.Entry) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.got = append(u.got, e)
}

func (u *unitSink) entries() []weblog.Entry {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]weblog.Entry(nil), u.got...)
}

// loggingServer is a server with the middleware of the application and a few routes that fail, and nothing else. Sign-in
// is off, so every request is made by the local administrator.
func loggingServer(t *testing.T, sink weblog.Sink) (http.Handler, *bytes.Buffer) {
	t.Helper()
	logs := &bytes.Buffer{}
	am, err := auth.NewManager(auth.Options{Disabled: true})
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		log: slog.New(slog.NewTextHandler(logs, nil)), auth: am, mux: http.NewServeMux(),
		opts: Options{Location: time.UTC, WebLog: sink},
	}
	if s.pages, err = parseTemplates(web.FS, s.funcs()); err != nil {
		t.Fatal(err)
	}
	s.mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) { panic("kaboom at D:\\secret\\folder") })
	s.mux.HandleFunc("GET /fail", func(w http.ResponseWriter, r *http.Request) {
		s.handleErr(w, r, errors.New(`disk D:\secret\folder is on fire`))
	})
	s.mux.HandleFunc("GET /api/v1/fail", func(w http.ResponseWriter, r *http.Request) {
		s.handleErr(w, r, errors.New(`disk D:\secret\folder is on fire`))
	})
	return s.Handler(), logs
}

func get(h http.Handler, target, addr string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = addr + ":50000"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// reference returns the id= of the log line that holds msg.
func reference(t *testing.T, logs, msg string) string {
	t.Helper()
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, msg) {
			_, rest, ok := strings.Cut(line, " id=")
			if !ok {
				t.Fatalf("no id= in %q", line)
			}
			return strings.Fields(rest)[0]
		}
	}
	t.Fatalf("no %q in the log:\n%s", msg, logs)
	return ""
}

func TestAPanicIsLoggedAnsweredAndReportedWithoutItsText(t *testing.T) {
	sink := &unitSink{}
	h, logs := loggingServer(t, sink)
	rec := get(h, "/boom", "10.1.2.3")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	log := logs.String()
	id := reference(t, log, "panic while handling a request")
	if !strings.Contains(rec.Body.String(), id) {
		t.Errorf("the error page does not show the reference %s that the log holds", id)
	}
	for _, want := range []string{"ip=10.1.2.3", "user=local-admin", "path=/boom", "method=GET"} {
		if line := lineOf(log, "panic while handling a request"); !strings.Contains(line, want) {
			t.Errorf("the panic is logged as %q, want %q in it", line, want)
		}
	}
	if line := lineOf(log, "msg=request"); !strings.Contains(line, "status=500") || !strings.Contains(line, "ip=10.1.2.3") || !strings.Contains(line, "user=local-admin") {
		t.Errorf("the request that panicked is logged as %q, want a normal request line with the status 500", line)
	}

	got := sink.entries()
	if len(got) != 1 {
		t.Fatalf("%d lines in the web log, want 1: %+v", len(got), got)
	}
	e := got[0]
	if e.Message != "TimeVault: error /boom user=local-admin ref="+id || e.Status != 500 || e.Level != slog.LevelError || e.IP != "10.1.2.3" || e.Class != weblog.ClassEvent {
		t.Errorf("web log line = %+v", e)
	}
	if strings.Contains(e.Message, "secret") || strings.Contains(e.Message, "kaboom") {
		t.Errorf("the text of the panic is in the web log: %q", e.Message)
	}
}

func TestAFailedRequestCarriesItsReferenceToTheWebLog(t *testing.T) {
	for _, target := range []string{"/fail", "/api/v1/fail"} {
		sink := &unitSink{}
		h, logs := loggingServer(t, sink)
		rec := get(h, target, "10.1.2.3")
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("%s: status = %d, want 500", target, rec.Code)
		}
		id := reference(t, logs.String(), "request failed")
		got := sink.entries()
		if len(got) != 1 || got[0].Message != "TimeVault: error "+target+" user=local-admin ref="+id || got[0].Status != 500 {
			t.Errorf("%s: web log = %+v, want one error line with the reference %s", target, got, id)
		}
		if line := lineOf(logs.String(), "request failed"); !strings.Contains(line, "ip=10.1.2.3") || !strings.Contains(line, "user=local-admin") || !strings.Contains(line, "disk") {
			t.Errorf("%s: the failure is logged as %q: the console has the address, the user and the error text", target, line)
		}
		if strings.Contains(got[0].Message, "secret") || strings.Contains(got[0].Message, "fire") {
			t.Errorf("%s: the error text is in the web log: %q", target, got[0].Message)
		}
	}
}

func TestWithoutAWebLogTheRequestLogStillWorks(t *testing.T) {
	h, logs := loggingServer(t, nil)
	get(h, "/fail", "10.1.2.3")
	if line := lineOf(logs.String(), "msg=request"); !strings.Contains(line, "status=500") {
		t.Errorf("request line = %q", line)
	}
}

func lineOf(logs, part string) string {
	for _, line := range strings.Split(logs, "\n") {
		if strings.Contains(line, part) {
			return line
		}
	}
	return ""
}
