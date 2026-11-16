package pathutil

import (
	"errors"
	"math/rand"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseUNC(t *testing.T) {
	cases := []struct {
		name, in string
		want     string
		host     string
		share    string
		rest     []string
	}{
		{"share only", `\\sgmeslt595.co.murata.local\TimeVault`, `\\sgmeslt595.co.murata.local\TimeVault`, "sgmeslt595.co.murata.local", "TimeVault", nil},
		{"folders below the share", `\\srv\TimeVault\monitor\Team A`, `\\srv\TimeVault\monitor\Team A`, "srv", "TimeVault", []string{"monitor", "Team A"}},
		{"forward slashes", `//srv/TimeVault/monitor`, `\\srv\TimeVault\monitor`, "srv", "TimeVault", []string{"monitor"}},
		{"mixed slashes", `\\srv/TimeVault\monitor/sub`, `\\srv\TimeVault\monitor\sub`, "srv", "TimeVault", []string{"monitor", "sub"}},
		{"spaces around", "  \\\\srv\\share\\dir  \n", `\\srv\share\dir`, "srv", "share", []string{"dir"}},
		{"one trailing separator", `\\srv\share\dir\`, `\\srv\share\dir`, "srv", "share", []string{"dir"}},
		{"trailing separator after the share", `//srv/share/`, `\\srv\share`, "srv", "share", nil},
		{"case is kept", `\\SRV.Example.COM\Share\Dir`, `\\SRV.Example.COM\Share\Dir`, "SRV.Example.COM", "Share", []string{"Dir"}},
		{"ip address", `\\10.1.2.3\data`, `\\10.1.2.3\data`, "10.1.2.3", "data", nil},
		{"underscore and hyphen in the host", `\\file_srv-01\data`, `\\file_srv-01\data`, "file_srv-01", "data", nil},
		{"non-ASCII names", `\\srv\共享\报告 2024\年度.docx`, `\\srv\共享\报告 2024\年度.docx`, "srv", "共享", []string{"报告 2024", "年度.docx"}},
		{"share of 80 characters", `\\srv\` + strings.Repeat("s", 80), `\\srv\` + strings.Repeat("s", 80), "srv", strings.Repeat("s", 80), nil},
		{"a folder ending in $ is fine", `\\srv\share\archive$`, `\\srv\share\archive$`, "srv", "share", []string{"archive$"}},
	}
	for _, c := range cases {
		got, err := ParseUNC(c.in)
		if err != nil {
			t.Errorf("%s: ParseUNC(%q) = %v", c.name, c.in, err)
			continue
		}
		if got.String() != c.want || got.Host != c.host || got.Share != c.share || strings.Join(got.Rest, "|") != strings.Join(c.rest, "|") {
			t.Errorf("%s: ParseUNC(%q) = %+v (%s), want %s", c.name, c.in, got, got, c.want)
		}
		if again, err := ParseUNC(got.String()); err != nil || again.String() != got.String() {
			t.Errorf("%s: the normalised form %q does not parse to itself: %v", c.name, got, err)
		}
	}
}

func TestParseUNCRejects(t *testing.T) {
	cases := map[string]string{
		"empty":                       "",
		"blank":                       "   ",
		"a drive path":                `D:\TimeVault\monitor`,
		"a relative path":             `TimeVault\monitor`,
		"one leading separator":       `\srv\share`,
		"extended prefix":             `\\?\D:\TimeVault`,
		"extended UNC prefix":         `\\?\UNC\srv\share`,
		"device prefix":               `\\.\pipe\x`,
		"url":                         `https://srv/share`,
		"only the prefix":             `\\`,
		"three separators":            `\\\srv\share`,
		"no share":                    `\\srv`,
		"no share but a separator":    `\\srv\`,
		"empty share":                 `\\srv\\folder`,
		"hidden share":                `\\srv\C$\Users`,
		"administrative share":        `\\srv\ADMIN$`,
		"ipc share":                   `\\srv\IPC$\x`,
		"share too long":              `\\srv\` + strings.Repeat("s", 81),
		"share with a colon":          `\\srv\sh:are`,
		"share with a star":           `\\srv\sh*are`,
		"share ending with a dot":     `\\srv\share.`,
		"reserved share":              `\\srv\NUL`,
		"dot share":                   `\\srv\..`,
		"host with a port":            `\\srv:445\share`,
		"host with a space":           `\\my srv\share`,
		"host with a leading dot":     `\\.srv\share`,
		"host with a trailing dot":    `\\srv.\share`,
		"host with two dots":          `\\srv..example\share`,
		"host with a non-ASCII name":  `\\sérver\share`,
		"host with a star":            `\\srv*\share`,
		"host too long":               `\\` + strings.Repeat("h", 254) + `\share`,
		"empty folder":                `\\srv\share\a\\b`,
		"two trailing separators":     `\\srv\share\a\\`,
		"dot folder":                  `\\srv\share\.\a`,
		"dot dot folder":              `\\srv\share\a\..\b`,
		"dot dot at the end":          `\\srv\share\..`,
		"folder with a star":          `\\srv\share\a*b`,
		"folder with a question mark": `\\srv\share\a?b`,
		"folder with a colon":         `\\srv\share\a:stream`,
		"folder with a quote":         `\\srv\share\"quoted"`,
		"folder with a pipe":          `\\srv\share\a|b`,
		"folder ending with a dot":    `\\srv\share\name.`,
		"folder ending with a space":  `\\srv\share\name \x`,
		"reserved folder":             `\\srv\share\aux\x`,
		"reserved folder with ext":    `\\srv\share\com1.txt`,
		"folder of 256 characters":    `\\srv\share\` + strings.Repeat("a", 256),
		"control character":           "\\\\srv\\share\\a\x07b",
		"tab inside":                  "\\\\srv\\share\\a\tb",
		"newline inside":              "\\\\srv\\share\\a\nb",
		"nul":                         "\\\\srv\\share\\a\x00",
		"invalid UTF-8":               "\\\\srv\\share\\\xff\xfe",
		"over the length limit":       `\\srv\share` + strings.Repeat(`\`+strings.Repeat("d", 50), 9) + `\` + strings.Repeat("d", 50),
		"huge":                        `\\srv\share\` + strings.Repeat("a/", 100000),
	}
	for name, in := range cases {
		got, err := ParseUNC(in)
		if err == nil {
			t.Errorf("%s: ParseUNC(%q) = %+v, want an error", name, shorten(in), got)
			continue
		}
		if !errors.Is(err, ErrInvalidUNC) {
			t.Errorf("%s: error %v does not wrap ErrInvalidUNC", name, err)
		}
	}
}

func shorten(s string) string {
	if len(s) > 60 {
		return s[:60] + "..."
	}
	return s
}

// The text may be a password typed into the wrong box, so the errors describe what is wrong, never what was typed.
func TestParseUNCErrorsNeverEchoTheText(t *testing.T) {
	for _, in := range []string{
		`\\hunter2*host\share`,
		`\\srv\hunter2*share`,
		`\\srv\share\hunter2*`,
		`\\srv\hunter2$`,
		`\\srv\share\hunter2\..\x`,
		`\\srv\share\CON`,
		`hunter2`,
		"\\\\srv\\share\\hunter2\x07",
		`\\` + strings.Repeat("hunter2", 100) + `\share`,
	} {
		_, err := ParseUNC(in)
		if err == nil {
			t.Errorf("%q: want an error", shorten(in))
			continue
		}
		if strings.Contains(err.Error(), "hunter2") || strings.Contains(err.Error(), "CON") {
			t.Errorf("%q: the error repeats the text: %v", shorten(in), err)
		}
	}
}

// 520 characters is the longest path, the same limit as the display_path column.
func TestParseUNCLimit(t *testing.T) {
	path := `\\srv\share` + strings.Repeat(`\`+strings.Repeat("d", 50), 9) + `\` + strings.Repeat("d", 49)
	if n := len([]rune(path)); n != MaxUNCLength {
		t.Fatalf("test path has %d characters, want %d", n, MaxUNCLength)
	}
	if u, err := ParseUNC(path); err != nil || u.String() != path {
		t.Fatalf("a path of %d characters was refused: %v", MaxUNCLength, err)
	}
	if _, err := ParseUNC(path + "e"); err == nil {
		t.Fatalf("a path of %d characters was accepted", MaxUNCLength+1)
	}
	if _, err := ParseUNC(path + `\`); err != nil {
		t.Fatalf("a trailing separator must not count: %v", err)
	}

	// The limit counts characters, not bytes.
	wide := `\\srv\share` + strings.Repeat(`\`+strings.Repeat("文", 100), 5) + `\` + strings.Repeat("文", 3)
	if n := len([]rune(wide)); n != MaxUNCLength || len(wide) <= MaxUNCLength {
		t.Fatalf("test path has %d characters and %d bytes", n, len(wide))
	}
	if _, err := ParseUNC(wide); err != nil {
		t.Fatalf("%d characters in %d bytes were refused: %v", MaxUNCLength, len(wide), err)
	}
	if _, err := ParseUNC(wide + "文"); err == nil {
		t.Fatalf("%d characters were accepted", MaxUNCLength+1)
	}
}

// Whatever ParseUNC accepts is in its normal form: it parses to itself, and nothing the callers rely on can have slipped through.
func TestParseUNCAcceptsOnlyCleanPaths(t *testing.T) {
	pieces := []string{
		`\\`, `//`, `\`, `/`, "srv", "Srv.Example.com", "share", "$", "dir", " ", ".", "..", "a b", "CON", "nul.txt", "*", "?", "文", "\t",
		"x", "-", "_", ":", "é", strings.Repeat("n", 90), "\x00", `"`,
	}
	rng := rand.New(rand.NewSource(7))
	accepted := 0
	for i := 0; i < 50000; i++ {
		var b strings.Builder
		if rng.Intn(4) > 0 {
			b.WriteString(pieces[rng.Intn(2)])
		}
		for n := rng.Intn(10) + 1; n > 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		in := b.String()
		u, err := ParseUNC(in)
		if err != nil {
			continue
		}
		accepted++
		s := u.String()
		again, err := ParseUNC(s)
		if err != nil || again.String() != s || again.Host != u.Host || again.Share != u.Share {
			t.Fatalf("ParseUNC(%q) = %q, which does not parse to itself: %v", in, s, err)
		}
		if n := utf8.RuneCountInString(s); n > MaxUNCLength || strings.ContainsAny(s, "/*?:\"<>|\x00\t\n") || strings.HasSuffix(s, `\`) || strings.HasSuffix(u.Share, "$") {
			t.Fatalf("ParseUNC(%q) accepted %q", in, s)
		}
		if ValidateName(u.Share) != nil || ValidateHost(u.Host) != nil {
			t.Fatalf("ParseUNC(%q) accepted a host or share that is not valid: %+v", in, u)
		}
		for _, folder := range u.Rest {
			if ValidateName(folder) != nil {
				t.Fatalf("ParseUNC(%q) accepted the folder %q", in, folder)
			}
		}
	}
	if accepted < 100 {
		t.Fatalf("only %d of the generated paths were valid, the test checks too little", accepted)
	}
}

func TestUNCStringAndJoin(t *testing.T) {
	u, err := ParseUNC(`//srv/TimeVault/monitor/`)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"":                `\\srv\TimeVault\monitor`,
		"a":               `\\srv\TimeVault\monitor\a`,
		"a/b/c.txt":       `\\srv\TimeVault\monitor\a\b\c.txt`,
		`a\b/c`:           `\\srv\TimeVault\monitor\a\b\c`,
		"/a/b/":           `\\srv\TimeVault\monitor\a\b`,
		"/":               `\\srv\TimeVault\monitor`,
		"报告/2024 年度.docx": `\\srv\TimeVault\monitor\报告\2024 年度.docx`,
	}
	for rel, want := range cases {
		if got := u.Join(rel); got != want {
			t.Errorf("Join(%q) = %q, want %q", rel, got, want)
		}
	}
	shareOnly, _ := ParseUNC(`\\srv\share`)
	if got := shareOnly.Join("x/y"); got != `\\srv\share\x\y` {
		t.Errorf("Join below a share = %q", got)
	}
	if got := shareOnly.Join(""); got != `\\srv\share` {
		t.Errorf("Join(\"\") = %q", got)
	}
}

func TestValidateHost(t *testing.T) {
	for _, ok := range []string{"a", "srv", "SRV01", "sgmeslt595.co.murata.local", "file_server-2", "10.0.0.5", "a.b", strings.Repeat("h", 253)} {
		if err := ValidateHost(ok); err != nil {
			t.Errorf("ValidateHost(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{
		"", " ", "srv ", "my srv", ".srv", "srv.", "a..b", "srv:445", `srv\share`, "srv/share", `\\srv`, "srv@x", "sérver", "服务器",
		"srv*", "http://srv", "srv\x00", "srv\n", strings.Repeat("h", 254),
	} {
		if err := ValidateHost(bad); err == nil {
			t.Errorf("ValidateHost(%q) = nil, want an error", bad)
		} else if len(bad) > 3 && strings.Contains(err.Error(), bad) {
			t.Errorf("ValidateHost(%q) repeats the text: %v", bad, err)
		}
	}
}
