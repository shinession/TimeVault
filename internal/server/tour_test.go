package server_test

import (
	"net/http"
	"strings"
	"testing"
)

// Every page except the sign-in page has the round buttons, the guide among them, and marks the parts that the guide
// explains. The marks of a page that shows data are only there when the data is.
func TestEveryPageHasItsGuideAndTheRoundButtons(t *testing.T) {
	h := newHarness(t)
	h.login()
	seed(h)
	h.env.Backup()
	h.env.Write("a.txt", "alpha v2")
	h.env.Backup()
	src := itoa(h.env.Src.ID)
	status, out := h.api("POST", "/api/v1/backups", map[string]any{"source_id": h.env.Src.ID})
	if status != http.StatusAccepted {
		t.Fatalf("start backup = %d %v", status, out)
	}
	job := itoa(num(out["job"].(map[string]any)["id"]))
	h.waitJob(num(out["job"].(map[string]any)["id"]))

	always := []string{
		`data-tour-start`, `data-fab-docs`, `href="/help" target="_blank" rel="noopener"`,
		`data-tour="fab"`, `data-tour="nav"`, `data-tour="theme"`, `data-tour="lang"`, `data-tour="account"`,
	}
	pages := []struct {
		path  string
		steps []string
	}{
		{"/explorer?source=" + src, []string{"source", "explorer.search", "crumbs", "explorer.latest", "tools", "explorer.list", "explorer.status"}},
		{"/history?source=" + src, []string{"source", "history.timeline", "history.banner", "crumbs", "tools", "history.actions", "history.list", "history.compare"}},
		{"/versions?source=" + src + "&path=a.txt", []string{"crumbs", "versions.card", "versions.table", "versions.restore", "versions.back"}},
		{"/backups", []string{"backups.manage", "backups.card", "backups.actions", "backups.facts", "backups.points", "backups.verify", "backups.jobs", "backups.restore"}},
		{"/jobs/" + job, []string{"job.card", "job.facts", "job.log"}},
		{"/audit", []string{"audit.filter", "audit.table"}},
		{"/settings", []string{"settings.intro", "settings.folder", "settings.edit", "settings.access", "settings.add", "settings.storage", "settings.service", "settings.updates"}},
		{"/status", []string{"status.table", "status.note"}},
		{"/help", []string{"help.toc", "help.body"}},
	}
	for _, p := range pages {
		res, body := h.get(p.path)
		if res.StatusCode != http.StatusOK {
			t.Errorf("%s = %d", p.path, res.StatusCode)
			continue
		}
		for _, want := range always {
			if !strings.Contains(body, want) {
				t.Errorf("%s has no %s", p.path, want)
			}
		}
		for _, step := range p.steps {
			if !strings.Contains(body, `data-tour="`+step+`"`) {
				t.Errorf("%s has no step %q", p.path, step)
			}
		}
		assertCSPClean(t, p.path, body)
	}
}

func TestAVisitorGetsTheGuideButNotTheAccountStep(t *testing.T) {
	h := newHarness(t)
	_, body := h.visitor().get("/explorer")
	for _, want := range []string{`data-tour-start`, `data-tour="signin"`, `data-tour="nav"`, `data-tour="fab"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page of a visitor has no %s", want)
		}
	}
	if strings.Contains(body, `data-tour="account"`) {
		t.Error("a visitor who is not signed in is told about an account")
	}
	// The sign-in page has no header with buttons and no round buttons.
	_, body = h.visitor().get("/login")
	if strings.Contains(body, "data-tour-start") || strings.Contains(body, "data-fab") {
		t.Error("the sign-in page has the round buttons")
	}
}

func TestTheGuideSpeaksTheLanguageOfThePage(t *testing.T) {
	h := newHarness(t)
	h.login()
	h.env.Backup()
	_, en := h.get("/explorer?source=" + itoa(h.env.Src.ID))
	_, zh := h.get("/explorer?source="+itoa(h.env.Src.ID), map[string]string{"Cookie": "tv_lang=zh"})
	for _, want := range []string{`data-tour-title="Search"`, `title="Documentation: the user guide, in a new tab"`, `data-tour-next="Next"`, `data-tour-step="Step {n} of {total}"`} {
		if !strings.Contains(en, want) {
			t.Errorf("English page has no %s", want)
		}
	}
	for _, want := range []string{`data-tour-title="搜索"`, `title="文档：在新标签页中打开用户手册"`, `data-tour-next="下一步"`, `data-tour-step="第 {n} 步，共 {total} 步"`} {
		if !strings.Contains(zh, want) {
			t.Errorf("Chinese page has no %s", want)
		}
	}
}

// The user guide is for everybody, signed in or not, in the language of the switch; the Chinese version is a version of
// its own and not a leftover of the English one.
func TestTheUserGuideIsOpenAndHasBothLanguages(t *testing.T) {
	h := newHarness(t)
	visitor := h.visitor()
	res, en := visitor.get("/help")
	if res.StatusCode != http.StatusOK || !strings.Contains(en, `lang="en"`) {
		t.Fatalf("GET /help = %d", res.StatusCode)
	}
	_, zh := visitor.get("/help", map[string]string{"Cookie": "tv_lang=zh"})
	if !strings.Contains(zh, `lang="zh"`) || zh == en {
		t.Error("the Chinese user guide is the English one")
	}
	assertCSPClean(t, "help", en)
	assertCSPClean(t, "help (zh)", zh)
	if !strings.Contains(en, `<title>User guide`) || !strings.Contains(zh, `<title>用户手册`) {
		t.Error("the user guide has no title in both languages")
	}
	// The same sections, with the same ids, in both.
	for _, id := range []string{`id="overview"`, `id="restore"`, `id="faq"`} {
		if !strings.Contains(en, id) || !strings.Contains(zh, id) {
			t.Errorf("a version of the user guide has no section %s", id)
		}
	}
	// It is a page of the application: a visitor who follows a link from it ends in the application.
	if res, _ := visitor.get("/help", map[string]string{"X-TV-Partial": "1"}); res.StatusCode != http.StatusOK {
		t.Errorf("partial /help = %d", res.StatusCode)
	}
}
