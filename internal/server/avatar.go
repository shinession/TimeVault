package server

import (
	"net/url"
	"strings"

	"timevault/internal/access"
	"timevault/internal/audit"
)

// systemActors are the names the service itself puts in the audit log and on jobs. They are not accounts, so they
// have no picture to look for.
var systemActors = []string{"scheduler", audit.ActorUpdater, "system"}

// pictureSource is what the content security policy needs for the pictures: the source to add to img-src when they
// are read from another host (an absolute address), and whether they are at all. A path is on the host of the site,
// which 'self' covers. The source is the host as it is written in the address, with its port.
//
// A host name with an underscore, such as prod_eng.example.local, cannot be written in a policy: the browser ignores
// the whole source (the grammar has letters, digits and hyphens only). The domain around it can be, with a wildcard,
// so *.example.local stands for it. When no domain with at least two labels is left, the source is "" and the
// pictures only show when the site is opened by the address of the pictures' host.
func pictureSource(address string) (source string, external bool) {
	l := strings.ToLower(address)
	if !strings.HasPrefix(l, "http://") && !strings.HasPrefix(l, "https://") {
		return "", false
	}
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" {
		return "", true
	}
	host := strings.ToLower(u.Hostname())
	if !policyHost(host) {
		if host = parentDomain(host); host == "" {
			return "", true
		}
		host = "*." + host
	}
	source = u.Scheme + "://" + host
	if port := u.Port(); port != "" {
		source += ":" + port
	}
	return source, true
}

// policyHost reports whether a host name can be written in a content security policy.
func policyHost(host string) bool {
	if host == "" {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// parentDomain is the nearest domain around the first labels of a host name that a policy can name and that still has
// two labels, "" when there is none: co.example.local for prod_eng.co.example.local, nothing for prod_eng.local.
func parentDomain(host string) string {
	for {
		_, rest, ok := strings.Cut(host, ".")
		if !ok || !strings.Contains(rest, ".") {
			return ""
		}
		if policyHost(rest) {
			return rest
		}
		host = rest
	}
}

// contentSecurityPolicy is the policy of every response. The pictures of the accounts are the only thing that is
// read from another host, and only when their address is one (see pictureSource).
func contentSecurityPolicy(picture string) string {
	img := "img-src 'self' data:"
	if picture != "" {
		img += " " + picture
	}
	return "default-src 'self'; " + img + "; style-src 'self'; script-src 'self'; " +
		"base-uri 'none'; form-action 'self'; frame-ancestors 'none'; object-src 'none'"
}

// avatarID is the employee ID to ask for the picture of, or "" when the name has none: pictures are off, the name is
// not an employee ID, or it is one of the service's own names or the local administrator.
func (s *Server) avatarID(name string) string {
	if s.opts.AvatarURL == "" {
		return ""
	}
	id, ok := access.ValidAccount(name)
	if !ok {
		return ""
	}
	if admin := s.opts.Deployment.LocalAdmin; admin != "" && strings.EqualFold(id, admin) {
		return ""
	}
	for _, actor := range systemActors {
		if strings.EqualFold(id, actor) {
			return ""
		}
	}
	return id
}

// AvatarID is avatarID for a template: the ID the page asks the picture of, "" for no picture. The sign-in of a
// service that runs without accounts is nobody's.
func (p *Page) AvatarID(name string) string {
	if p.avatar == nil || (p.AuthDisabled && name == p.User) {
		return ""
	}
	return p.avatar(name)
}
