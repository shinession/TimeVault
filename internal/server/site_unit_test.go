package server

import (
	"reflect"
	"testing"

	"timevault/internal/config"
)

// Every Site setting has its label and its explanation in both languages, and every group its title and hint.
func TestEverySiteSettingHasItsTexts(t *testing.T) {
	for _, lang := range []string{"en", "zh"} {
		for _, s := range config.SiteSettings() {
			for _, prefix := range []string{"site.f.", "site.h."} {
				if messages[lang][prefix+s.Key] == "" {
					t.Errorf("%s: no text %s%s", lang, prefix, s.Key)
				}
			}
		}
		for _, g := range config.SiteGroups() {
			for _, prefix := range []string{"site.g.", "site.gh."} {
				if messages[lang][prefix+g] == "" {
					t.Errorf("%s: no text %s%s", lang, prefix, g)
				}
			}
		}
	}
}

func TestSiteChangeStoresOnlyWhatDiffersFromTheDefault(t *testing.T) {
	stored := map[string]string{"log_level": "debug", "job_workers": "4", "not_a_setting": "x"}
	posted := map[string]string{"log_level": "info", "job_workers": " 6 ", "cookie_secure": "TRUE", "base_path": ""}
	next, typed, problems := siteChange(stored, func(key string) (string, bool) { v, ok := posted[key]; return v, ok })
	if len(problems) != 0 {
		t.Fatalf("problems: %v", problems)
	}
	want := map[string]string{"job_workers": "6", "cookie_secure": "true"}
	if !reflect.DeepEqual(next, want) {
		t.Errorf("next = %v, want %v (log_level went back to its default, the key that is no setting is dropped)", next, want)
	}
	if typed["job_workers"] != "6" || typed["cookie_secure"] != "TRUE" || len(typed) != 4 {
		t.Errorf("typed = %v", typed)
	}
	set, reset := diffSite(knownSite(stored), next)
	if !reflect.DeepEqual(set, map[string]string{"job_workers": "6", "cookie_secure": "true"}) || !reflect.DeepEqual(reset, []string{"log_level"}) {
		t.Errorf("diff = %v, %v", set, reset)
	}

	_, _, problems = siteChange(nil, func(key string) (string, bool) {
		if key == "job_workers" {
			return "many", true
		}
		return "", false
	})
	if len(problems) != 1 {
		t.Errorf("a value that is not a number: problems = %v", problems)
	}
}
