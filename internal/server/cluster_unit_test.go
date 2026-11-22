package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"timevault/internal/cluster"
)

// Every state of a server of the group has its words, in both languages: the label and the hint that the Servers page and
// the tabs show are looked up by the state.
func TestEveryStateOfAServerIsTranslated(t *testing.T) {
	for _, state := range []cluster.State{cluster.StateOnline, cluster.StateOffline, cluster.StateWaiting, cluster.StateIncompatible} {
		for _, prefix := range []string{"servers.state.", "servers.hint."} {
			for _, lang := range []string{"en", "zh"} {
				if messages[lang][prefix+string(state)] == "" {
					t.Errorf("no %s message for %q", lang, prefix+string(state))
				}
			}
		}
	}
}

// A request names a folder either with the server and the number apart, or as server:number, which is what the folder
// selector of a page sends. A number alone is a folder of this server.
func TestFolderRefReadsBothSpellings(t *testing.T) {
	cases := []struct {
		query  string
		server string
		id     int64
	}{
		{"source=5", "", 5},
		{"source=5&server=beta", "beta", 5},
		{"source=beta:7", "beta", 7},
		{"source=beta:7&server=gamma", "beta", 7},
		{"source=%20beta%20:%209%20", "beta", 9},
		{"server=beta", "beta", 0},
		{"source=beta:", "beta", 0},
		{"source=abc", "", 0},
		{"", "", 0},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/history?"+c.query, nil)
		server, id := folderRef(r)
		if server != c.server || id != c.id {
			t.Errorf("folderRef(%q) = %q, %d; want %q, %d", c.query, server, id, c.server, c.id)
		}
	}
}
