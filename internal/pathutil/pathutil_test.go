package pathutil

import (
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateName(t *testing.T) {
	good := []string{"a.txt", "报告 2024 v2.docx", "name with spaces", ".hidden", "COM10", "console.txt", "a..b", strings.Repeat("x", 255)}
	for _, n := range good {
		if err := ValidateName(n); err != nil {
			t.Errorf("ValidateName(%q) = %v, want ok", n, err)
		}
	}
	bad := []string{
		"", ".", "..", "a/b", `a\b`, "a:b", "a*b", "a?b", `a"b`, "a<b", "a>b", "a|b",
		"trailing.", "trailing ", "tab\tname", "nul\x00name", "bell\x07",
		"CON", "con", "NUL.txt", "com1", "LPT9.log", "Aux .txt", "\xff\xfe",
		"CONIN$", "conout$.txt", "COM\u00b9", "lpt\u00b3.log",
		strings.Repeat("x", 256),
	}
	for _, n := range bad {
		err := ValidateName(n)
		if err == nil {
			t.Errorf("ValidateName(%q) = nil, want error", n)
		} else if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("ValidateName(%q) error %v does not wrap ErrUnsafePath", n, err)
		}
	}
}

func TestCleanRel(t *testing.T) {
	good := map[string]string{
		"":                 "",
		"/":                "",
		".":                "",
		"a":                "a",
		"/a/b/":            "a/b",
		`\a\b\c.txt`:       "a/b/c.txt",
		"a//b/./c":         "a/b/c",
		"报告/2024 年度.docx":  "报告/2024 年度.docx",
		"//server/share/x": "server/share/x",
	}
	for in, want := range good {
		if got, err := CleanRel(in); err != nil || got != want {
			t.Errorf("CleanRel(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	bad := []string{
		"x/../../etc", "C:/Windows", "a/b/..", "a/nul", "a/b/c?", "ok/\x00bad",
		"a/../b", `..\..\secret`, strings.Repeat("a/", 300),
	}
	for _, in := range bad {
		if got, err := CleanRel(in); err == nil {
			t.Errorf("CleanRel(%q) = %q, want error", in, got)
		} else if !errors.Is(err, ErrUnsafePath) {
			t.Errorf("CleanRel(%q) error %v does not wrap ErrUnsafePath", in, err)
		}
	}
}

func TestRelHelpers(t *testing.T) {
	if got := JoinRel("", "a"); got != "a" {
		t.Errorf("JoinRel root = %q", got)
	}
	if got := JoinRel("a/b", "c"); got != "a/b/c" {
		t.Errorf("JoinRel = %q", got)
	}
	if got := ParentRel("a/b/c"); got != "a/b" {
		t.Errorf("ParentRel = %q", got)
	}
	if got := ParentRel("a"); got != "" {
		t.Errorf("ParentRel(a) = %q", got)
	}
	if got := BaseRel("a/b/c"); got != "c" {
		t.Errorf("BaseRel = %q", got)
	}
	if got := NameKey("ÄbC.TXT"); got != "äbc.txt" {
		t.Errorf("NameKey = %q", got)
	}
}

func TestWithinAndOverlaps(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("uses Windows style absolute paths")
	}
	within := [][2]string{
		{`D:\data\src`, `D:\data\src`},
		{`D:\data\src`, `D:\data\src\a\b.txt`},
		{`D:\data\src\`, `D:\DATA\Src\a`},
		{`D:\`, `D:\anything`},
		{`D:\data\src`, `\\?\D:\data\src\x`},
	}
	for _, c := range within {
		if !Within(c[0], c[1]) {
			t.Errorf("Within(%q, %q) = false, want true", c[0], c[1])
		}
	}
	outside := [][2]string{
		{`D:\data\src`, `D:\data\src2\x.txt`},
		{`D:\data\src`, `D:\data`},
		{`D:\data\src`, `E:\data\src\x`},
		{`D:\data\src`, `relative\path`},
		{`relative`, `D:\data\src`},
	}
	for _, c := range outside {
		if Within(c[0], c[1]) {
			t.Errorf("Within(%q, %q) = true, want false", c[0], c[1])
		}
	}
	if !Overlaps(`D:\a`, `D:\a\b`) || !Overlaps(`D:\a\b`, `D:\a`) || Overlaps(`D:\a`, `D:\b`) {
		t.Error("Overlaps gave an unexpected answer")
	}
}

func TestRealResolvesRedirections(t *testing.T) {
	dir := t.TempDir()
	got, err := Real(filepath.Join(dir, "."))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := filepath.EvalSymlinks(dir)
	if !strings.EqualFold(got, want) {
		t.Fatalf("Real = %q, want %q", got, want)
	}
}
