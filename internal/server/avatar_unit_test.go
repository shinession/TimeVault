package server

import (
	"strings"
	"testing"
)

func TestPictureSourceNamesTheHostOfAnAbsoluteAddress(t *testing.T) {
	for _, c := range []struct {
		address, source string
		external        bool
	}{
		{"http://server.example.test/api/static/hr/{id}.webp", "http://server.example.test", true},
		{"https://Server.Example.Test:8443/hr/{id}.png", "https://server.example.test:8443", true},
		{"HTTP://10.135.36.81/api/static/hr/{id}.webp", "http://10.135.36.81", true},
		{"http://pics-1.example.test/hr?user={id}", "http://pics-1.example.test", true},
		// An underscore cannot be written in a policy; the domain around the host can.
		{"http://prod_eng.co.example.test/api/static/hr/{id}.webp", "http://*.co.example.test", true},
		{"http://prod_eng.co.example.test:8080/hr/{id}.webp", "http://*.co.example.test:8080", true},
		{"http://a_b.c_d.example.test/hr/{id}.webp", "http://*.example.test", true},
		// ... unless no domain of two labels is left to name.
		{"http://prod_eng.local/hr/{id}.webp", "", true},
		{"http://prod_eng/hr/{id}.webp", "", true},
		// A path is on the host of the site, which 'self' covers.
		{"/api/static/hr/{id}.webp", "", false},
		{"", "", false},
	} {
		source, external := pictureSource(c.address)
		if source != c.source || external != c.external {
			t.Errorf("pictureSource(%q) = %q, %v; want %q, %v", c.address, source, external, c.source, c.external)
		}
	}
}

func TestPolicyHostIsWhatThePolicyGrammarAllows(t *testing.T) {
	for host, want := range map[string]bool{
		"server.example.test": true, "10.1.2.3": true, "a-b.example.test": true, "x": true,
		"": false, "prod_eng.example.test": false, ".example.test": false, "example..test": false,
		"example.test.": false, "ex ample.test": false, "a;b.test": false, "a,b.test": false, "[::1]": false, "ex*.test": false,
	} {
		if got := policyHost(host); got != want {
			t.Errorf("policyHost(%q) = %v, want %v", host, got, want)
		}
	}
}

func TestTheContentSecurityPolicyListsThePictureHostOnlyForImages(t *testing.T) {
	const plain = "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"
	if got := contentSecurityPolicy(""); got != plain {
		t.Errorf("without another host the policy is %q", got)
	}
	got := contentSecurityPolicy("http://*.co.example.test:8080")
	if want := strings.Replace(plain, "img-src 'self' data:;", "img-src 'self' data: http://*.co.example.test:8080;", 1); got != want {
		t.Errorf("with a picture host the policy is %q, want %q", got, want)
	}
	// Nothing but img-src changed: the same directives, and the host is not a source of scripts, styles or connections.
	for _, directive := range []string{"default-src 'self'", "style-src 'self'", "script-src 'self'", "frame-ancestors 'none'", "object-src 'none'"} {
		if !strings.Contains(got, directive) {
			t.Errorf("the policy lost %q", directive)
		}
	}
	if strings.Count(got, "example.test") != 1 {
		t.Errorf("the picture host is in more than one directive: %q", got)
	}
}
