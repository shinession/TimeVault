package server_test

import (
	"strings"
	"testing"

	"timevault/internal/server"
)

func withPictureAddress(address string) func(*server.Options) {
	return func(o *server.Options) { o.AvatarURL = address }
}

// The browser reads the pictures from the host that keeps them, so the page says the address and the content security
// policy of every answer lets images come from that host.
func TestThePageTellsWhereThePicturesAreAndThePolicyAllowsThatHost(t *testing.T) {
	for name, c := range map[string]struct{ address, meta, img string }{
		"a host name": {
			"http://pics.example.test/api/static/hr/{id}.webp",
			"http://pics.example.test/api/static/hr/{id}.webp", "img-src 'self' data: http://pics.example.test;",
		},
		"a host name with a port": {
			"https://pics.example.test:8443/hr/{id}.png",
			"https://pics.example.test:8443/hr/{id}.png", "img-src 'self' data: https://pics.example.test:8443;",
		},
		"an address": {
			"http://10.135.36.81/api/static/hr/{id}.webp",
			"http://10.135.36.81/api/static/hr/{id}.webp", "img-src 'self' data: http://10.135.36.81;",
		},
		"a host name with an underscore": {
			"http://prod_eng.co.example.test/api/static/hr/{id}.webp",
			"http://prod_eng.co.example.test/api/static/hr/{id}.webp", "img-src 'self' data: http://*.co.example.test;",
		},
		"a host name that a policy cannot name": {
			"http://prod_eng/api/static/hr/{id}.webp",
			"http://prod_eng/api/static/hr/{id}.webp", "img-src 'self' data:;",
		},
		"a path on the host of the site": {
			"/api/static/hr/{id}.webp",
			"/api/static/hr/{id}.webp", "img-src 'self' data:;",
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarnessOpts(t, nil, withPictureAddress(c.address))
			alice := h.visitor()
			alice.loginAs(userID)
			res, page := alice.get("/explorer")
			if want := `<meta name="tv-avatar" content="` + c.meta + `">`; !strings.Contains(page, want) {
				t.Errorf("the page does not say where the pictures are: lacks %s", want)
			}
			if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, c.img) {
				t.Errorf("the policy %q lacks %q", csp, c.img)
			}
			// Every answer carries the policy, also the sign-in page of somebody who is not signed in.
			res, _ = h.visitor().get("/login")
			if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, c.img) {
				t.Errorf("the policy of the sign-in page %q lacks %q", csp, c.img)
			}
		})
	}
}

func TestWithoutPicturesThePolicyAllowsNoOtherHost(t *testing.T) {
	h := newHarness(t)
	alice := h.visitor()
	alice.loginAs(userID)
	res, page := alice.get("/explorer")
	if csp := res.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src 'self' data:;") || strings.Contains(csp, "http") {
		t.Errorf("the policy of a service without pictures is %q", csp)
	}
	if strings.Contains(page, `name="tv-avatar"`) {
		t.Error("the page says where pictures are although there are none")
	}
}
