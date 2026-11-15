package search

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

func TestSanitize(t *testing.T) {
	tests := []struct {
		name string
		text string
		want []string
	}{
		{"single word", "report", []string{"report"}},
		{"words", "annual  report\u3000final", []string{"annual", "report", "final"}},
		{"phrase", `"annual report" 2024`, []string{"annual report", "2024"}},
		{"phrase keeps inner spaces", `"  a   b "`, []string{"a   b"}},
		{"unterminated phrase takes the rest", `a "b c`, []string{"a", "b c"}},
		{"embedded quotes are stripped", `ab"cd"ef`, []string{"ab", "cd", "ef"}},
		{"empty phrase is skipped", `"" a ""`, []string{"a"}},
		{"unicode", "报告 café 😀", []string{"报告", "café", "😀"}},
		{"or operator stays one literal term", "a|b", []string{"a|b"}},
		{"not operator", "!x", []string{"!x"}},
		{"group operator", "<a b>", []string{"<a", "b>"}},
		{"size function", "size:>1", []string{"size:>1"}},
		{"parent path", `..\..`, []string{`..\..`}},
		{"drive", `D:\`, []string{`D:\`}},
		{"regex function", "regex:.*", []string{"regex:."}},
		{"extension wildcard", "*.docx", []string{".docx"}},
		{"inner wildcard", "nee*le", []string{"nee", "le"}},
		{"question wildcard", "a?c", []string{"a", "c"}},
		{"wildcard inside phrase", `"a*b c"`, []string{"a", "b c"}},
		{"exactly MaxTerms", strings.Repeat("a ", MaxTerms), repeatTerm("a", MaxTerms)},
		{"exactly MaxTextLength", strings.Repeat("a", MaxTextLength), []string{strings.Repeat("a", MaxTextLength)}},
		{"MaxTextLength in multi-byte characters", strings.Repeat("é", MaxTextLength/2), []string{strings.Repeat("é", MaxTextLength/2)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Sanitize(tt.text)
			if err != nil {
				t.Fatalf("Sanitize(%q) error = %v", tt.text, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Sanitize(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}

func TestSanitizeRejects(t *testing.T) {
	tests := []struct {
		name string
		text string
	}{
		{"empty", ""},
		{"blank", "   \u3000 "},
		{"only empty quotes", `"" ""`},
		{"only wildcards", "* ? **"},
		{"tab", "a\tb"},
		{"newline", "a\nb"},
		{"carriage return", "a\rb"},
		{"nul", "a\x00b"},
		{"delete", "a\x7fb"},
		{"c1 control", "a\u0085b"},
		{"invalid utf-8", "a\xffb"},
		{"too long", strings.Repeat("a", MaxTextLength+1)},
		{"too long in multi-byte characters", strings.Repeat("é", MaxTextLength/2+1)},
		{"too many terms", strings.Repeat("a ", MaxTerms+1)},
		{"too many terms through wildcards", strings.Repeat("a*", MaxTerms+1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Sanitize(tt.text)
			if !errors.Is(err, ErrBadQuery) {
				t.Fatalf("Sanitize(%q) = %q, %v; want ErrBadQuery", tt.text, got, err)
			}
		})
	}
}

func repeatTerm(s string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = s
	}
	return out
}

func TestBuildSearch(t *testing.T) {
	tests := []struct {
		name  string
		root  string
		terms []string
		want  string
	}{
		{"plain", `D:\data\src`, []string{"a", "b"}, `path:"D:\data\src\" "a" "b"`},
		{"trailing backslash", `D:\data\src\`, []string{"a"}, `path:"D:\data\src\" "a"`},
		{"forward slashes", `D:/data/src/`, []string{"a"}, `path:"D:\data\src\" "a"`},
		{"lower-case drive", `d:\data`, []string{"a"}, `path:"D:\data\" "a"`},
		{"spaces and unicode", `D:\数据 files\报告 café`, []string{"x y"}, `path:"D:\数据 files\报告 café\" "x y"`},
		{"odd but legal characters", `D:\odd !;,'^&()[]{}$#@+=~ %TEMP%`, []string{"a"}, `path:"D:\odd !;,'^&()[]{}$#@+=~ %TEMP%\" "a"`},
		{"drive root", `D:\`, []string{"a"}, `path:"D:\" "a"`},
		{"unc", `\\srv\share\dir`, []string{"a"}, `path:"\\srv\share\dir\" "a"`},
		{"unc share root", `\\srv\share`, []string{"a"}, `path:"\\srv\share\" "a"`},
		{"quotes cannot close a term", `D:\r`, []string{`a" "b`, `"`}, `path:"D:\r\" "a b"`},
		{"every term is quoted", `D:\r`, []string{"a|b", "!x", "<a", "b>", "regex:.", "size:>1", `..\..`, `D:\`, "x:"}, `path:"D:\r\" "a|b" "!x" "<a" "b>" "regex:." "size:>1" "..\.." "D:\" "x:"`},
		{"term ending in a backslash", `D:\r`, []string{`docs\`, "n"}, `path:"D:\r\" "docs\" "n"`},
		{"no terms", `D:\r`, nil, `path:"D:\r\"`},
		{"empty root matches nothing", ``, []string{"a"}, `path:"|" "a"`},
		{"relative root matches nothing", `data\src`, []string{"a"}, `path:"|" "a"`},
		{"bare drive matches nothing", `D:`, []string{"a"}, `path:"|" "a"`},
		{"traversal root matches nothing", `D:\a\..\b`, []string{"a"}, `path:"|" "a"`},
		{"quote in root matches nothing", `D:\a"b`, []string{"a"}, `path:"|" "a"`},
		{"wildcard in root matches nothing", `D:\a*`, []string{"a"}, `path:"|" "a"`},
		{"device prefix matches nothing", `\\?\D:\a`, []string{"a"}, `path:"|" "a"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BuildSearch(tt.root, tt.terms); got != tt.want {
				t.Errorf("BuildSearch(%q, %q)\n got  %s\n want %s", tt.root, tt.terms, got, tt.want)
			}
		})
	}
}

func TestParseRoot(t *testing.T) {
	valid := map[string]string{
		`D:\data\src`:                          `D:\data\src\`,
		`d:\Data\Src\`:                         `D:\Data\Src\`,
		`D:/data/src/`:                         `D:\data\src\`,
		`D:\`:                                  `D:\`,
		`D:/`:                                  `D:\`,
		`\\srv\share`:                          `\\srv\share\`,
		`\\srv\share\`:                         `\\srv\share\`,
		`\\srv\share\a b\c`:                    `\\srv\share\a b\c\`,
		`D:\odd !;,'^&()[]{}$#@+=~ %TEMP% dir`: `D:\odd !;,'^&()[]{}$#@+=~ %TEMP% dir\`,
	}
	for in, want := range valid {
		r, err := parseRoot(in)
		if err != nil {
			t.Errorf("parseRoot(%q) error = %v", in, err)
			continue
		}
		if got := r.scope(); got != want {
			t.Errorf("parseRoot(%q).scope() = %q, want %q", in, got, want)
		}
	}

	invalid := []string{
		"", "   ", "D:", `D`, `data\src`, `\data\src`, `.\src`, `..\src`, `D:data`,
		`D:\a\..\b`, `D:\a\.\b`, `D:\a\..`, `D:\a\\b`, `D:\\`, `D:\a\\`, `D://a`,
		`\\?\D:\a`, `\\.\D:\a`, `\\srv`, `\\srv\`, `\\\share`, `\\srv\\x`,
		`D:\a"b`, `D:\a<b`, `D:\a>b`, `D:\a|b`, `D:\a?b`, `D:\a*b`, `D:\a:b`, `D:\a:stream`,
		"D:\\a\x00b", "D:\\a\nb", `D:\a.`, `D:\a `, ` D:\a`, `D:\a\ `,
		`\\srv:1\share\a`, `\\srv\sh*re\a`,
		`http://x/y`, `D:\` + strings.Repeat("a", maxRootLength),
	}
	for _, in := range invalid {
		if r, err := parseRoot(in); !errors.Is(err, ErrBadQuery) {
			t.Errorf("parseRoot(%q) = %+v, %v; want ErrBadQuery", in, r, err)
		}
	}
}

func TestRootContains(t *testing.T) {
	tests := []struct {
		root string
		path string
		want bool
	}{
		{`D:\data\src`, `D:\data\src\x.txt`, true},
		{`D:\data\src`, `D:\data\src\sub\deep\x.txt`, true},
		{`D:\data\src`, `d:\DATA\Src\sub\x.txt`, true},
		{`D:\data\src\`, `D:\data\src\x.txt`, true},
		{`D:/data/src/`, `D:\data\src\x.txt`, true},
		{`D:\data\src`, `D:/data/src/x.txt`, true},
		{`D:\data\src`, `D:\data\src2\x.txt`, false},
		{`D:\data\src`, `D:\data\srcx`, false},
		{`D:\data\src`, `D:\data\src`, false},
		{`D:\data\src`, `D:\data\src\`, false},
		{`D:\data\src`, `D:\data`, false},
		{`D:\data\src`, `D:\data\sr\x.txt`, false},
		{`D:\data\src`, `E:\data\src\x.txt`, false},
		{`D:\data\src`, `D:\other\data\src\x.txt`, false},
		{`D:\data\src`, `D:\data\src\..\other\x.txt`, false},
		{`D:\data\src`, `D:\data\src\.\x.txt`, false},
		{`D:\data\src`, `D:\data\src\\x.txt`, false},
		{`D:\data\src`, `data\src\x.txt`, false},
		{`D:\data\src`, `x.txt`, false},
		{`D:\data\src`, ``, false},
		{`D:\data\src`, `\\srv\share\data\src\x.txt`, false},
		{`D:\data\src`, `\\?\D:\data\src\x.txt`, false},
		{`D:\`, `D:\x.txt`, true},
		{`D:\`, `d:\a\b.txt`, true},
		{`D:\`, `D:\`, false},
		{`D:\`, `E:\x.txt`, false},
		{`\\srv\share\dir`, `\\SRV\Share\Dir\f.txt`, true},
		{`\\srv\share\dir`, `\\srv\share\dir2\f.txt`, false},
		{`\\srv\share\dir`, `\\srv2\share\dir\f.txt`, false},
		{`\\srv\share`, `\\srv\share\f.txt`, true},
		{`\\srv\share`, `D:\f.txt`, false},
	}
	for _, tt := range tests {
		r, err := parseRoot(tt.root)
		if err != nil {
			t.Fatalf("parseRoot(%q) error = %v", tt.root, err)
		}
		if got := r.contains(tt.path); got != tt.want {
			t.Errorf("root %q contains %q = %v, want %v", tt.root, tt.path, got, tt.want)
		}
	}
}

func TestNewRequestLimits(t *testing.T) {
	base := Query{Text: "a", Root: `D:\r`}
	ok := []struct {
		offset, limit int
		wantLimit     int
	}{
		{0, 0, DefaultLimit},
		{0, 1, 1},
		{12, MaxLimit, MaxLimit},
		{1<<31 - 1, 10, 10},
	}
	for _, tt := range ok {
		q := base
		q.Offset, q.Limit = tt.offset, tt.limit
		r, err := newRequest(q)
		if err != nil {
			t.Errorf("newRequest(offset %d, limit %d) error = %v", tt.offset, tt.limit, err)
			continue
		}
		if r.limit != tt.wantLimit || r.offset != tt.offset {
			t.Errorf("newRequest(offset %d, limit %d) = offset %d limit %d", tt.offset, tt.limit, r.offset, r.limit)
		}
	}
	bad := []struct{ offset, limit int }{{-1, 10}, {0, -1}, {0, MaxLimit + 1}, {1 << 31, 10}}
	for _, tt := range bad {
		q := base
		q.Offset, q.Limit = tt.offset, tt.limit
		if _, err := newRequest(q); !errors.Is(err, ErrBadQuery) {
			t.Errorf("newRequest(offset %d, limit %d) error = %v, want ErrBadQuery", tt.offset, tt.limit, err)
		}
	}
}

func FuzzQueryStaysQuoted(f *testing.F) {
	for _, seed := range []string{`a|b`, `!x`, `<a b>`, `regex:.*`, `size:>1`, `..\..`, `D:\`, `"a" "b`, `a"b`, `x\" "y`, "报告 \"phrase here\" *", `%TEMP% ;,'`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		terms, err := Sanitize(text)
		if err != nil {
			if !errors.Is(err, ErrBadQuery) {
				t.Fatalf("Sanitize error %v is not ErrBadQuery", err)
			}
			return
		}
		if len(terms) == 0 || len(terms) > MaxTerms {
			t.Fatalf("Sanitize(%q) returned %d terms", text, len(terms))
		}
		for _, term := range terms {
			if strings.TrimSpace(term) == "" || strings.ContainsAny(term, `"*?`) {
				t.Fatalf("Sanitize(%q) returned unsafe term %q", text, term)
			}
			for _, r := range term {
				if unicode.IsControl(r) {
					t.Fatalf("Sanitize(%q) returned a control character in %q", text, term)
				}
			}
		}
		search := BuildSearch(`D:\root`, terms)
		if !strings.HasPrefix(search, `path:"D:\root\" "`) {
			t.Fatalf("search %q is not confined to the root", search)
		}
		if got, want := strings.Count(search, `"`), 2*(1+len(terms)); got != want {
			t.Fatalf("search %q has %d quotes, want %d: a term escaped its quotes", search, got, want)
		}
	})
}
