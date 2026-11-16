package service

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"timevault/internal/catalog"
)

const (
	monitorDisplay = `\\sgmeslt595.co.murata.local\TimeVault\monitor`
	restoreDisplay = `\\sgmeslt595.co.murata.local\TimeVault\restores`
	archiveDisplay = `\\sgmeslt595.co.murata.local\Archive`
)

// pathMapFixture is the layout of a real server: D:\TimeVault is published as the share TimeVault, one
// source has no display path, one is a whole drive, and the repository is elsewhere.
func pathMapFixture() *PathMap {
	return NewPathMap(
		[]catalog.Source{
			{ID: 1, Name: "Monitor", RootPath: `D:\TimeVault\monitor`, DisplayPath: monitorDisplay},
			{ID: 2, Name: "Engineering", RootPath: `E:\Data\Eng`},
			{ID: 3, Name: "Archive", RootPath: `F:\`, DisplayPath: archiveDisplay},
		},
		`D:\Backups\TimeVault`, `D:\TimeVault\restores`, restoreDisplay,
	)
}

func TestMask(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"the folder itself", `D:\TimeVault\monitor`, monitorDisplay},
		{"a file below it", `D:\TimeVault\monitor\Team A\plan.xlsx`, monitorDisplay + `\Team A\plan.xlsx`},
		{"another case", `d:\TIMEVAULT\Monitor\Plan.XLSX`, monitorDisplay + `\Plan.XLSX`},
		{"forward slashes", `D:/TimeVault/monitor/sub/a.txt`, monitorDisplay + `\sub\a.txt`},
		{"mixed separators", `D:\TimeVault/monitor\sub/a.txt`, monitorDisplay + `\sub\a.txt`},
		{"separators after the folder become backslashes", `D:\TimeVault\monitor/x/y/z`, monitorDisplay + `\x\y\z`},
		{"doubled backslashes", `D:\\TimeVault\\monitor\\a.txt`, monitorDisplay + `\\a.txt`},
		{"extended prefix", `\\?\D:\TimeVault\monitor\sub`, monitorDisplay + `\sub`},
		{"extended prefix of the folder itself", `\\?\D:\TimeVault\monitor`, monitorDisplay},
		{"extended prefix in other case", `\\?\d:\timevault\MONITOR\x`, monitorDisplay + `\x`},
		{"extended prefix with forward slashes", `//?/D:/TimeVault/monitor/x`, monitorDisplay + `\x`},
		{"extended prefix that is not followed by a folder", `\\?\C:\Windows`, `\\?\C:\Windows`},
		{"in a sentence", `cannot read D:\TimeVault\monitor\a.txt: Access is denied.`, `cannot read ` + monitorDisplay + `\a.txt: Access is denied.`},
		{"folder before a colon", `open D:\TimeVault\monitor: Access is denied.`, `open ` + monitorDisplay + `: Access is denied.`},
		{"folder at the end of a sentence", `the folder D:\TimeVault\monitor.`, `the folder ` + monitorDisplay + `.`},
		{"folder before an ellipsis", `scanning D:\TimeVault\monitor...`, `scanning ` + monitorDisplay + `...`},
		{"quoted", `open "D:\TimeVault\monitor\a b.txt" failed`, `open "` + monitorDisplay + `\a b.txt" failed`},
		{"single quoted", `open 'D:\TimeVault\monitor'`, `open '` + monitorDisplay + `'`},
		{"in a list", `[D:\TimeVault\monitor, D:\Backups\TimeVault; (D:\TimeVault\restores)]`, `[` + monitorDisplay + `, [backup folder]; (` + restoreDisplay + `)]`},
		{"several times", `copy D:\TimeVault\monitor\a to D:\TimeVault\restores\b and D:\TimeVault\monitor`, `copy ` + monitorDisplay + `\a to ` + restoreDisplay + `\b and ` + monitorDisplay},
		{"a source without a display path", `E:\Data\Eng\x.txt`, `[Engineering]\x.txt`},
		{"the repository", `D:\Backups\TimeVault\objects\ab\cdef`, `[backup folder]\objects\ab\cdef`},
		{"the restore folder", `D:\TimeVault\restores\restore-1\a.txt`, restoreDisplay + `\restore-1\a.txt`},
		{"a whole drive", `F:\docs\a.txt`, archiveDisplay + `\docs\a.txt`},
		{"a whole drive in other case", `f:/docs`, archiveDisplay + `\docs`},
		{"a longer name is another folder", `D:\TimeVault\monitor2\a`, `D:\TimeVault\monitor2\a`},
		{"a longer name at the end", `D:\TimeVault\monitor2`, `D:\TimeVault\monitor2`},
		{"a dotted name", `D:\TimeVault\monitor.bak\a`, `D:\TimeVault\monitor.bak\a`},
		{"a name with a dash", `D:\TimeVault\monitor-old`, `D:\TimeVault\monitor-old`},
		{"a parent is not hidden", `D:\TimeVault`, `D:\TimeVault`},
		{"a sibling is not hidden", `D:\TimeVault\other\a`, `D:\TimeVault\other\a`},
		{"an unrelated path", `C:\Windows\System32\cmd.exe`, `C:\Windows\System32\cmd.exe`},
		{"a drive letter in prose", `ID: 5, drive F: is full, F:`, `ID: 5, drive F: is full, F:`},
		{"no separator", `nothing to hide`, `nothing to hide`},
		{"empty", ``, ``},
		{"a url", `see http://example.com/a/b`, `see http://example.com/a/b`},
		{"text that is already masked", monitorDisplay + `\x and [backup folder]`, monitorDisplay + `\x and [backup folder]`},
	}
	m := pathMapFixture()
	for _, c := range cases {
		got := m.Mask(c.in)
		if got != c.want {
			t.Errorf("%s: Mask(%q)\n got  %q\n want %q", c.name, c.in, got, c.want)
		}
		if again := m.Mask(got); again != got {
			t.Errorf("%s: masking is not stable: %q became %q", c.name, got, again)
		}
	}
}

func TestMaskEndsAtTheBoundaries(t *testing.T) {
	m := pathMapFixture()
	for _, after := range []string{"", `\x`, `/x`, " x", "\tx", "\nx", `"`, `'`, "`", ",", ";", ")", "]", "}", ":", "!", "?", "*", "|", "<", ">", ".", "..", ". x", `.\x`, "，", "。", "）", "”", "\u00a0x"} {
		in := `D:\TimeVault\monitor` + after
		want := monitorDisplay + strings.ReplaceAll(after, "/", `\`)
		if got := m.Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
	for _, after := range []string{"x", "2", "_", "-", "~1", ".x", "..x", ".bak", "文", "é", "(", "[", "{", "&", "#", "$", "@", "+", "="} {
		in := `D:\TimeVault\monitor` + after
		if got := m.Mask(in); got != in {
			t.Errorf("Mask(%q) = %q; the name goes on, so it is another folder", in, got)
		}
	}
}

func TestMaskPrefersTheLongestFolder(t *testing.T) {
	outer := catalog.Source{ID: 1, Name: "Outer", RootPath: `D:\Data`, DisplayPath: `\\h\Outer`}
	inner := catalog.Source{ID: 2, Name: "Inner", RootPath: `D:\Data\Inner`, DisplayPath: `\\h\Inner`}
	for name, sources := range map[string][]catalog.Source{"outer first": {outer, inner}, "inner first": {inner, outer}} {
		m := NewPathMap(sources, "", "", "")
		for in, want := range map[string]string{
			`D:\Data\Inner\x`:                        `\\h\Inner\x`,
			`D:\Data\Inner`:                          `\\h\Inner`,
			`D:\Data\Other\x`:                        `\\h\Outer\Other\x`,
			`D:\Data\InnerX\y`:                       `\\h\Outer\InnerX\y`,
			`D:\Data`:                                `\\h\Outer`,
			`from D:\Data\Inner to D:\Data\Inner2\f`: `from \\h\Inner to \\h\Outer\Inner2\f`,
		} {
			if got := m.Mask(in); got != want {
				t.Errorf("%s: Mask(%q) = %q, want %q", name, in, got, want)
			}
		}
	}
}

func TestMaskIgnoresFoldersThatAreNotPaths(t *testing.T) {
	nothing := NewPathMap(nil, "", "", "")
	m := NewPathMap([]catalog.Source{
		{ID: 1, Name: "no folder", RootPath: ""},
		{ID: 2, Name: "blank", RootPath: "  "},
		{ID: 3, Name: "root", RootPath: `\`},
		{ID: 4, Name: "slash", RootPath: `/`},
		{ID: 5, Name: "a bare name", RootPath: `data`},
		{ID: 6, Name: "the prefix only", RootPath: `\\?\`},
	}, " ", "", `\\h\s`)
	for _, in := range []string{`D:\x`, `a/b and data`, `\\h\s\x`, `\`, `/`, `//`, ``, `data`} {
		if got := nothing.Mask(in); got != in {
			t.Errorf("a map without folders changed %q into %q", in, got)
		}
		if got := m.Mask(in); got != in {
			t.Errorf("a map of folders that are not paths changed %q into %q", in, got)
		}
	}
}

func TestMaskSpellingsOfTheFolder(t *testing.T) {
	for _, root := range []string{`D:\Long\Path`, `D:\Long\Path\`, `D:/Long/Path`, `d:\LONG\path`, `\\?\D:\Long\Path`, ` D:\Long\Path `, `D:\\Long\\Path`} {
		m := NewPathMap([]catalog.Source{{ID: 1, Name: "S", RootPath: root, DisplayPath: `\\h\s`}}, "", "", "")
		for in, want := range map[string]string{
			`D:\Long\Path\x`:     `\\h\s\x`,
			`\\?\D:\Long\Path\x`: `\\h\s\x`,
			`d:/long/path/X`:     `\\h\s\X`,
			`D:\Long\Path2`:      `D:\Long\Path2`,
		} {
			if got := m.Mask(in); got != want {
				t.Errorf("root %q: Mask(%q) = %q, want %q", root, in, got, want)
			}
		}
	}
}

func TestMaskUnicodeAndCase(t *testing.T) {
	m := NewPathMap([]catalog.Source{
		{ID: 1, Name: "数据", RootPath: `D:\数据\Бэкап`, DisplayPath: `\\h\共享\备份`},
		{ID: 2, Name: "K", RootPath: `K:\Ünïcode`},
	}, "", "", "")
	for in, want := range map[string]string{
		`d:\数据\бэкап\файл.txt`: `\\h\共享\备份\файл.txt`,
		`D:\数据\БЭКАП`:          `\\h\共享\备份`,
		`D:\数据\Бэкап文件`:        `D:\数据\Бэкап文件`,
		`k:\ÜNÏCODE\x`:         `[K]\x`,
		`报告 D:\数据\Бэкап。`:      `报告 \\h\共享\备份。`,
	} {
		if got := m.Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

// A label that would still contain a folder to hide must not be put into the text.
func TestMaskNeverLeaksThroughALabel(t *testing.T) {
	named := NewPathMap([]catalog.Source{
		{ID: 7, Name: `D:\Data`, RootPath: `D:\Data`},
		{ID: 8, Name: `copy of D:\Data\x`, RootPath: `E:\Other`},
		{ID: 9, Name: `E:\Shown`, RootPath: `E:\Shown`, DisplayPath: `\\h\shown`},
	}, `\\nas\backups\tv`, `\\nas\restores`, `\\nas\restores`)
	for in, want := range map[string]string{
		`D:\Data\x`:                   `[monitored folder 7]\x`,
		`E:\Other\y`:                  `[monitored folder 8]\y`,
		`E:\Shown\z`:                  `\\h\shown\z`,
		`\\nas\backups\tv\objects\ab`: `[backup folder]\objects\ab`,
		`\\nas\restores\r1`:           `[restore folder]\r1`,
		`/nas/restores/r1`:            `[restore folder]\r1`,
	} {
		if got := named.Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
}

// Replacing one folder can put another one together; the result must still be free of folders to hide.
func TestMaskRunsUntilNothingIsLeft(t *testing.T) {
	chain := NewPathMap([]catalog.Source{{ID: 1, Name: "A", RootPath: `D:\a`, DisplayPath: `\\h\s`}}, `\\h\s\x`, "", "")
	if got, want := chain.Mask(`D:\a\x\f.txt`), `[backup folder]\f.txt`; got != want {
		t.Errorf("a display path followed by the rest of a repository path = %q, want %q", got, want)
	}
	m := NewPathMap([]catalog.Source{
		{ID: 1, Name: "A", RootPath: `D:\a`, DisplayPath: `\\h\a`},
		{ID: 2, Name: "B", RootPath: `E:\b`, DisplayPath: `\\h\b`},
	}, "", "", "")
	// D:\a is followed by E here, so it is only found once E:\b has been replaced.
	if got, want := m.Mask(`D:\aE:\b`), `\\h\a\\h\b`; got != want {
		t.Errorf("two folders written together = %q, want %q", got, want)
	}
}

// Whatever the text, no folder to hide survives, and masking again changes nothing.
func TestMaskNeverLeavesAFolderBehind(t *testing.T) {
	m := pathMapFixture()
	pieces := []string{
		`D:\TimeVault\monitor`, `d:/timevault/MONITOR`, `\\?\D:\TimeVault\monitor`, `E:\Data\Eng`, `e:\data\eng\`, `D:\Backups\TimeVault`,
		`D:\TimeVault\restores`, `F:\`, `F:`, `D:\TimeVault`, monitorDisplay, restoreDisplay, `[Engineering]`, `[backup folder]`,
		`\`, `/`, `\\`, `//`, `\\?\`, `.`, `..`, ` `, "\n", `x`, `2`, `:`, `"`, `,`, `[`, `]`, `Docs`, `é`, `文`, `E:`, `D:\a`,
	}
	rng := rand.New(rand.NewSource(20260611))
	for i := 0; i < 30000; i++ {
		var b strings.Builder
		for n := rng.Intn(9) + 1; n > 0; n-- {
			b.WriteString(pieces[rng.Intn(len(pieces))])
		}
		in := b.String()
		out := m.Mask(in)
		if m.contains(out) {
			t.Fatalf("Mask(%q) = %q still holds a folder to hide", in, out)
		}
		if again := m.Mask(out); again != out {
			t.Fatalf("Mask(%q) = %q is not stable, it becomes %q", in, out, again)
		}
	}
}

func TestMaskLeavesInvalidUTF8AloneWhenThereIsNothingToHide(t *testing.T) {
	m := pathMapFixture()
	in := "C:\\x\\\xff\xfe\\y"
	if got := m.Mask(in); got != in {
		t.Errorf("Mask(%q) = %q", in, got)
	}
}

func TestMaskValue(t *testing.T) {
	m := pathMapFixture()
	type other struct{ Path string }
	in := map[string]any{
		"root":                 `D:\TimeVault\monitor`,
		"dest":                 `D:\TimeVault\restores\r1`,
		"paths":                []any{`D:\TimeVault\monitor\a`, 12, true, nil, 1.5, map[string]any{"deep": `E:\Data\Eng\x`, "list": []string{`D:\Backups\TimeVault\o`}}},
		"list":                 []string{`D:\Backups\TimeVault\o`, "plain"},
		"n":                    float64(3),
		"nil":                  nil,
		"nomap":                map[string]any(nil),
		"nolist":               []string(nil),
		"noany":                []any(nil),
		"struct":               other{`D:\TimeVault\monitor`},
		"ints":                 []int{1, 2},
		`D:\TimeVault\monitor`: "keys are kept",
	}
	snapshot := fmt.Sprintf("%v", in)

	got, ok := m.MaskValue(in).(map[string]any)
	if !ok {
		t.Fatalf("MaskValue of a map gave %T", m.MaskValue(in))
	}
	if fmt.Sprintf("%v", in) != snapshot {
		t.Fatal("MaskValue changed its argument")
	}
	want := map[string]any{
		"root":                 monitorDisplay,
		"dest":                 restoreDisplay + `\r1`,
		"paths":                []any{monitorDisplay + `\a`, 12, true, nil, 1.5, map[string]any{"deep": `[Engineering]\x`, "list": []string{`[backup folder]\o`}}},
		"list":                 []string{`[backup folder]\o`, "plain"},
		"n":                    float64(3),
		"nil":                  nil,
		"nomap":                map[string]any(nil),
		"nolist":               []string(nil),
		"noany":                []any(nil),
		"struct":               other{`D:\TimeVault\monitor`}, // other types are not touched
		"ints":                 []int{1, 2},
		`D:\TimeVault\monitor`: "keys are kept",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MaskValue =\n %#v\nwant\n %#v", got, want)
	}

	// The result is a copy: changing it leaves the argument alone.
	got["list"].([]string)[1] = "changed"
	got["paths"].([]any)[1] = "changed"
	if in["list"].([]string)[1] != "plain" || in["paths"].([]any)[1] != 12 {
		t.Error("the result shares its slices with the argument")
	}

	if s := m.MaskValue(`D:\TimeVault\monitor\x`); s != monitorDisplay+`\x` {
		t.Errorf("MaskValue of a string = %v", s)
	}
	if v := m.MaskValue(nil); v != nil {
		t.Errorf("MaskValue(nil) = %v", v)
	}
	if v := m.MaskValue(42); v != 42 {
		t.Errorf("MaskValue(42) = %v", v)
	}
}

func TestPathMapDisplayPaths(t *testing.T) {
	m := pathMapFixture()
	for _, c := range []struct {
		id   int64
		rel  string
		want string
	}{
		{1, "", monitorDisplay},
		{1, "a", monitorDisplay + `\a`},
		{1, "a/b/c.txt", monitorDisplay + `\a\b\c.txt`},
		{1, `a\b/c`, monitorDisplay + `\a\b\c`},
		{1, "/a/b/", monitorDisplay + `\a\b`},
		{3, "docs", archiveDisplay + `\docs`},
		{2, "x", ""},  // no display path
		{99, "x", ""}, // unknown source
	} {
		if got := m.SourceDisplay(c.id, c.rel); got != c.want {
			t.Errorf("SourceDisplay(%d, %q) = %q, want %q", c.id, c.rel, got, c.want)
		}
	}
	for id, want := range map[int64]string{1: monitorDisplay, 2: "Engineering", 3: archiveDisplay, 99: ""} {
		if got := m.SourceLabel(id); got != want {
			t.Errorf("SourceLabel(%d) = %q, want %q", id, got, want)
		}
	}
	for dest, want := range map[string]string{"": restoreDisplay, "restore-1": restoreDisplay + `\restore-1`, "a/b": restoreDisplay + `\a\b`} {
		if got := m.RestoreDisplay(dest); got != want {
			t.Errorf("RestoreDisplay(%q) = %q, want %q", dest, got, want)
		}
	}

	// Without a display root, or with a stored text that is not a network path, there is nothing to show.
	bare := NewPathMap([]catalog.Source{{ID: 1, Name: "Broken", RootPath: `D:\x`, DisplayPath: "garbage"}}, `D:\r`, `D:\restores`, "")
	if got := bare.RestoreDisplay("r1"); got != "" {
		t.Errorf("RestoreDisplay without a display root = %q", got)
	}
	if got := bare.SourceDisplay(1, "a"); got != "" {
		t.Errorf("SourceDisplay of a stored text that is not a network path = %q", got)
	}
	if got := bare.SourceLabel(1); got != "Broken" {
		t.Errorf("SourceLabel = %q, want the name", got)
	}
	for in, want := range map[string]string{`D:\x\a`: `[Broken]\a`, `D:\restores\r1`: `[restore folder]\r1`, `D:\r\o`: `[backup folder]\o`} {
		if got := bare.Mask(in); got != want {
			t.Errorf("Mask(%q) = %q, want %q", in, got, want)
		}
	}
	if got := NewPathMap(nil, "", "", "garbage").RestoreDisplay("r1"); got != "" {
		t.Errorf("RestoreDisplay with a display root that is not a network path = %q", got)
	}
}
