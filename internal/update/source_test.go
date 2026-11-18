package update

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewSourceAddresses(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name, address, want string
		bad                 bool
	}{
		{name: "http file", address: "http://h/rel/timevault.json", want: "http://h/rel/timevault.json"},
		{name: "http folder", address: "http://h/rel/", want: "http://h/rel/timevault.json"},
		{name: "http root", address: "http://h", want: "http://h/timevault.json"},
		{name: "https with a password", address: "https://user:secret@h/x.json", bad: true},
		{name: "http without a host", address: "http:///x", bad: true},
		{name: "relative path", address: `rel` + string(filepath.Separator) + `timevault.json`, bad: true},
		{name: "an existing folder", address: dir, want: filepath.Join(dir, "timevault.json")},
		{name: "a folder with a separator", address: dir + string(filepath.Separator), want: filepath.Join(dir, "timevault.json")},
		{name: "a file", address: filepath.Join(dir, "other.json"), want: filepath.Join(dir, "other.json")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src, err := NewSource(c.address, nil)
			if c.bad {
				if err == nil {
					t.Fatalf("NewSource(%q) was accepted", c.address)
				}
				if strings.Contains(err.Error(), "secret") {
					t.Fatalf("the error shows the password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewSource(%q): %v", c.address, err)
			}
			if src.Address() != c.want {
				t.Fatalf("Address() = %q, want %q", src.Address(), c.want)
			}
		})
	}
}

// releaseServer serves a release folder at /rel/ and a few answers that are not one.
func releaseServer(t *testing.T, manifest string, program []byte) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/rel/timevault.json", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, manifest) })
	mux.HandleFunc("/rel/timevault.exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(program) })
	mux.HandleFunc("/other/pkg.bin", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(program) })
	mux.HandleFunc("/timevault.json", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, manifest) })
	mux.HandleFunc("/timevault.exe", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(program) })
	// the application that serves the static files answers 202 for a missing file
	mux.HandleFunc("/accepted/timevault.json", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, manifest)
	})
	mux.HandleFunc("/huge/timevault.json", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(bytes.Repeat([]byte(" "), MaxManifestBytes+10))
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/loop", http.StatusFound) })
	mux.HandleFunc("/moved/timevault.json", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/rel/timevault.json", http.StatusFound)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPSourceReadsTheManifestAndTheProgram(t *testing.T) {
	program := []byte("the program")
	m := manifestJSON("0.1.8", sum(program), "timevault.exe", int64(len(program)))
	srv := releaseServer(t, m, program)
	ctx := context.Background()

	for _, address := range []string{srv.URL + "/rel/timevault.json", srv.URL + "/rel/", srv.URL + "/moved/timevault.json"} {
		src, err := NewSource(address, nil)
		if err != nil {
			t.Fatalf("NewSource(%q): %v", address, err)
		}
		got, err := src.Manifest(ctx)
		if err != nil || got.LatestVersion != "0.1.8" {
			t.Fatalf("Manifest(%q) = %+v, %v", address, got, err)
		}
	}

	src, _ := NewSource(srv.URL+"/rel/timevault.json", nil)
	got, _ := src.Manifest(ctx)
	rc, size, err := src.Open(ctx, got)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(data, program) || size != int64(len(program)) {
		t.Fatalf("Open gave %q (%d bytes announced)", data, size)
	}

	// a manifest at the root of the server: the program is next to it
	root, _ := NewSource(srv.URL, nil)
	if rootManifest, err := root.Manifest(ctx); err != nil {
		t.Fatalf("Manifest of the server root: %v", err)
	} else if rc, _, err := root.Open(ctx, rootManifest); err != nil {
		t.Fatalf("Open at the server root: %v", err)
	} else {
		_ = rc.Close()
	}

	// a package address that is an absolute path of the same server
	abs := got
	abs.PackageURL = "/other/pkg.bin"
	rc, _, err = src.Open(ctx, abs)
	if err != nil {
		t.Fatalf("Open with an absolute path: %v", err)
	}
	_ = rc.Close()
}

func TestHTTPSourceRefusesWhatIsNotAManifestOrAProgram(t *testing.T) {
	program := []byte("the program")
	m := manifestJSON("0.1.8", sum(program), "timevault.exe", int64(len(program)))
	srv := releaseServer(t, m, program)
	ctx := context.Background()

	for _, c := range []struct{ path, want string }{
		{"/missing/timevault.json", "404"},
		{"/accepted/timevault.json", "202"},
		{"/huge/timevault.json", "longer than"},
		{"/loop", "redirects"},
	} {
		src, err := NewSource(srv.URL+c.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := src.Manifest(ctx); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("Manifest(%s) = %v, want an error with %q", c.path, err, c.want)
		}
	}

	src, _ := NewSource(srv.URL+"/rel/timevault.json", nil)
	good, _ := src.Manifest(ctx)
	for _, bad := range []string{"ftp://h/timevault.exe", "file:///c:/windows/system32/cmd.exe", "http://user:pw@h/x", "/missing.exe"} {
		m := good
		m.PackageURL = bad
		if rc, _, err := src.Open(ctx, m); err == nil {
			_ = rc.Close()
			t.Errorf("Open accepted the package address %q", bad)
		}
	}
}

func TestFileSourceReadsAFolderAndFindsTheProgramNextToIt(t *testing.T) {
	dir := t.TempDir()
	program := []byte("the program")
	write := func(rel string, data []byte) string {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("timevault.json", []byte(manifestJSON("0.1.8", sum(program), "timevault.exe", int64(len(program)))))
	write("timevault.exe", program)
	write("sub/pkg.exe", program)
	absolute := write("elsewhere/pkg.exe", program)
	ctx := context.Background()

	src, err := NewSource(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := src.Manifest(ctx)
	if err != nil {
		t.Fatalf("Manifest: %v", err)
	}
	for name, packageURL := range map[string]string{"next to the manifest": "timevault.exe", "in a subfolder": "sub/pkg.exe", "absolute": absolute} {
		m.PackageURL = packageURL
		rc, size, err := src.Open(ctx, m)
		if err != nil {
			t.Fatalf("Open (%s): %v", name, err)
		}
		data, _ := io.ReadAll(rc)
		_ = rc.Close()
		if !bytes.Equal(data, program) || size != int64(len(program)) {
			t.Fatalf("Open (%s) gave %q, %d", name, data, size)
		}
	}

	for _, bad := range []string{"missing.exe", "sub"} {
		m.PackageURL = bad
		if rc, _, err := src.Open(ctx, m); err == nil {
			_ = rc.Close()
			t.Errorf("Open accepted %q", bad)
		}
	}

	empty, _ := NewSource(filepath.Join(dir, "none"+string(filepath.Separator)), nil)
	if _, err := empty.Manifest(ctx); err == nil {
		t.Error("a folder without a manifest was read")
	}
}

// A share that cannot be reached when the service starts is the folder of the release all the same once it can.
func TestFileSourceFindsAFolderThatAppearsAfterTheStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "release") // no trailing separator, and not there yet
	src, err := NewSource(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := src.Manifest(ctx); err == nil {
		t.Fatal("a folder that is not there was read")
	}

	program := []byte("the program")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "timevault.json"), []byte(manifestJSON("0.1.8", sum(program), "timevault.exe", int64(len(program)))))
	writeFile(t, filepath.Join(dir, "timevault.exe"), program)
	m, err := src.Manifest(ctx)
	if err != nil {
		t.Fatalf("Manifest once the folder is there: %v", err)
	}
	rc, _, err := src.Open(ctx, m)
	if err != nil {
		t.Fatalf("Open once the folder is there: %v", err)
	}
	_ = rc.Close()
}

func TestFileSourceMayPointAtAnHTTPProgram(t *testing.T) {
	program := []byte("the program")
	srv := releaseServer(t, "{}", program)
	dir := t.TempDir()
	src, _ := NewSource(dir, nil)
	m := Manifest{LatestVersion: "0.1.8", SHA256: sum(program), PackageURL: srv.URL + "/rel/timevault.exe", Size: int64(len(program))}
	rc, _, err := src.Open(context.Background(), m)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !bytes.Equal(data, program) {
		t.Fatalf("got %q", data)
	}
	m.PackageURL = "http://user:pw@" + strings.TrimPrefix(srv.URL, "http://") + "/rel/timevault.exe"
	if rc, _, err := src.Open(context.Background(), m); err == nil {
		_ = rc.Close()
		t.Error("an address with a password was accepted")
	}
}
