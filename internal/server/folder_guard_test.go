package server

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every handler reaches a monitored folder through the checks of access.go. A handler that read folders from the
// service directly would show a hidden folder to somebody who may not see it. This test cannot prove that every
// handler checks the right level, but it fails when somebody bypasses the checks, which is how a hole would get in.
func TestHandlersReachFoldersThroughTheAccessChecks(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no source files: %v", err)
	}
	direct := []string{"svc.Source(", "svc.Sources(", ".cat().GetSource(", ".cat().ListSources(", "Catalog().GetSource(", "Catalog().ListSources("}
	// What lists the jobs or the audit events of many folders has to be limited to the folders the visitor may see.
	limited := []string{"svc.Jobs(", "svc.AuditEvents("}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") || name == "access.go" {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		for _, call := range direct {
			if strings.Contains(text, call) {
				t.Errorf("%s calls %s: a handler must use loadSource, source or visibleSources from access.go", name, call)
			}
		}
		for _, call := range limited {
			if strings.Contains(text, call) && !strings.Contains(text, "restrictTo(") {
				t.Errorf("%s calls %s without limiting it to the visible folders (restrictTo)", name, call)
			}
		}
	}
}
