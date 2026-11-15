package redisuser

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

// go-redis writes to stderr when a dial fails; the package must stay silent and only return errors.
func TestNothingIsWrittenToStderr(t *testing.T) {
	if os.Getenv("REDISUSER_STDERR_CHILD") == "1" {
		s, err := Open(Options{URL: "redis://127.0.0.1:1", Timeout: 200 * time.Millisecond}) // nothing listens on port 1
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		for i := 0; i < 6; i++ {
			s.Ping(ctx)
			s.Lookup(ctx, "E1001")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestNothingIsWrittenToStderr$")
	cmd.Env = append(os.Environ(), "REDISUSER_STDERR_CHILD=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if out, err := cmd.Output(); err != nil {
		t.Fatalf("child failed: %v\n%s", err, out)
	}
	if stderr.Len() > 0 {
		t.Errorf("the package wrote to stderr:\n%s", stderr.String())
	}
}

func TestOpenRejectsBadOptions(t *testing.T) {
	cases := []struct {
		name string
		o    Options
	}{
		{"no URL", Options{}},
		{"blank URL", Options{URL: "  "}},
		{"wrong scheme", Options{URL: "http://cache.example.test:6379"}},
		{"unix socket", Options{URL: "unix:///var/run/redis.sock"}},
		{"no host", Options{URL: "redis:///0"}},
		{"URL that does not parse", Options{URL: "redis://:topsecret@cache.example.test:80x/0%zz"}},
		{"database that is not a number", Options{URL: "redis://:topsecret@cache.example.test/abc"}},
		{"unknown URL option", Options{URL: "redis://:topsecret@cache.example.test/0?bogus=1"}},
		{"negative timeout", Options{URL: "redis://cache.example.test", Timeout: -time.Second}},
		{"negative MaxScan", Options{URL: "redis://cache.example.test", MaxScan: -1}},
		{"MaxScan too large", Options{URL: "redis://cache.example.test", MaxScan: 100_001}},
		{"control character in the prefix", Options{URL: "redis://cache.example.test", KeyPrefix: "app\n_"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := Open(c.o)
			if err == nil {
				s.Close()
				t.Fatal("Open succeeded")
			}
			if strings.Contains(err.Error(), "topsecret") {
				t.Errorf("the password is in the error: %v", err)
			}
			if !strings.HasPrefix(err.Error(), "redisuser: ") || err.Error() != strings.ToLower(err.Error()) {
				t.Errorf("errors are lower case and name the package: %q", err)
			}
		})
	}
}

// Open must not connect: the service starts while Redis is down.
func TestOpenDoesNotConnect(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	accepted := make(chan struct{})
	go func() {
		if c, err := l.Accept(); err == nil {
			c.Close()
			close(accepted)
		}
	}()
	s, err := Open(Options{URL: "redis://" + l.Addr().String() + "/0?min_idle_conns=2&max_idle_conns=2"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	select {
	case <-accepted:
		t.Error("Open connected to the server")
	case <-time.After(300 * time.Millisecond):
	}
}

func TestAddrAndOptionsHideCredentials(t *testing.T) {
	cases := []struct{ url, addr string }{
		{"redis://user:topsecret@cache.example.test:6380/2", "cache.example.test:6380/2"},
		{"redis://:topsecret@cache.example.test", "cache.example.test:6379/0"},
		{"rediss://:topsecret@cache.example.test:6381/1", "cache.example.test:6381/1 (tls)"},
		{"redis://:topsecret@[::1]:6379/0", "[::1]:6379/0"},
	}
	for _, c := range cases {
		o := Options{URL: c.url, KeyPrefix: "x_"}
		s := openStore(t, o)
		if got := s.Addr(); got != c.addr {
			t.Errorf("Addr() = %q, want %q", got, c.addr)
		}
		for _, shown := range []string{fmt.Sprintf("%v", o), fmt.Sprintf("%+v", o), fmt.Sprintf("%#v", o), fmt.Sprint(o), s.Addr()} {
			if strings.Contains(shown, "topsecret") || strings.Contains(shown, "user:") {
				t.Errorf("credentials in %q", shown)
			}
		}
	}
	if shown := (Options{URL: "redis://:topsecret@%zz"}).String(); strings.Contains(shown, "topsecret") {
		t.Errorf("credentials in %q", shown)
	}
}

func TestDefaults(t *testing.T) {
	s := openStore(t, Options{URL: "redis://cache.example.test"})
	if s.prefix != "app_user_" || s.maxScan != 200 || s.budget <= 0 {
		t.Errorf("unexpected defaults: %q, %d, %s", s.prefix, s.maxScan, s.budget)
	}
	s = openStore(t, Options{URL: "redis://cache.example.test", KeyPrefix: "dir_", MaxScan: 5, Timeout: time.Second})
	if s.prefix != "dir_" || s.maxScan != 5 || s.budget != 5*time.Second {
		t.Errorf("unexpected settings: %q, %d, %s", s.prefix, s.maxScan, s.budget)
	}
}

// A prefix with wildcard characters is a literal prefix.
func TestPrefixIsLiteral(t *testing.T) {
	mr := miniredis.RunT(t)
	genuine := alice()
	decoy := alice()
	decoy.password, decoy.updated = "decoy-pw", now.Add(time.Hour) // would win if it were a candidate
	seedAs(mr, "x[1]_"+strings.TrimPrefix(genuine.key(), "app_user_"), genuine.hash())
	seedAs(mr, "x1_"+strings.TrimPrefix(decoy.key(), "app_user_"), decoy.hash())
	s := openStore(t, Options{URL: "redis://" + mr.Addr(), KeyPrefix: "x[1]_"})
	rec, err := s.Lookup(ctx, "E1001")
	if err != nil || !rec.CheckPassword("fake-pw-1") {
		t.Fatalf("got %v, %v", rec, err)
	}
	if got, err := s.Get(ctx, "", "E1001"); err != nil || !got.CheckPassword("fake-pw-1") {
		t.Errorf("Get: %v, %v", got, err)
	}
	rep, err := s.Check(ctx)
	if err != nil || rep.Keys != 1 {
		t.Errorf("Check counted %d keys: %v", rep.Keys, err)
	}
}
