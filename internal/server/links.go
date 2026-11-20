package server

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	core "github.com/abjelosevic88/pocket-homelab-core/sdk"
	"github.com/abjelosevic88/pocket-homelab-remote/internal/store"
)

// Pocket Core app links (docs/links.md in pocket-homelab-core), Remote as the
// target: another Pocket app the person allowed in Core may act here for
// them with a pcx_ token, only within these scopes. Every route not listed
// in linkRoutes stays closed to it (403).

var linkOffers = []core.Offer{
	{Scope: "remote:read", Name: "See your computers", Description: "List your computers, whether each one is online right now and your recent remote sessions, for your dashboards, Pocket AI and Day. Never passwords, and never a way in."},
	{Scope: "remote:wake", Name: "Wake your computers", Description: "Send a Wake-on-LAN signal to one of your computers (\"wake my PC\"). It cannot open a session or see the screen."},
}

// portable is what Pocket Move may copy out and bring in for a person
// (docs/move.md in pocket-homelab-core); Register adds move:export/import.
// Passwords and private keys never leave: the export has the computers
// without them, and they are typed in again on the new box.
var portable = &core.Portable{
	Exports: []core.PortableExport{
		{Path: "/api/v1/export.json", File: "remote.json", Kind: core.KindPocket, Title: "Your computers (without their passwords and keys), sessions and file transfers"},
		{Path: "/api/v1/export.csv", File: "sessions.csv", Kind: core.KindCSV, Title: "Your remote sessions as a table"},
	},
	Imports: []core.PortableImport{
		{Path: "/api/v1/import", Kind: core.KindPocket, ContentType: "application/json", MaxBytes: 32 << 20, Title: "Adds the computers, sessions and transfers from another box (sign-in passwords and keys are set again); what you already have is left alone"},
	},
}

type linkRoute struct {
	methods string
	path    *regexp.Regexp
	scope   string // "" = any linked app
}

var linkRoutes = []linkRoute{
	{"GET", regexp.MustCompile(`^/api/v1/auth/me$`), ""},
	{"GET", regexp.MustCompile(`^/api/v1/(overview|machines|sessions)$`), "remote:read"},
	{"GET", regexp.MustCompile(`^/api/v1/machines/\d+$`), "remote:read"},
	{"POST", regexp.MustCompile(`^/api/v1/machines/\d+/check$`), "remote:read"},
	{"POST", regexp.MustCompile(`^/api/v1/machines/\d+/wake$`), "remote:wake"},
}

var errLinkScope = errors.New("outside the link's scope")

// linkAllows reports whether a link token with these scopes may make this request.
func linkAllows(scopes []string, r *http.Request) bool {
	if portable.LinkAllows(scopes, r) {
		return true
	}
	for _, lr := range linkRoutes {
		if !strings.Contains(" "+lr.methods+" ", " "+r.Method+" ") || !lr.path.MatchString(r.URL.Path) {
			continue
		}
		if lr.scope == "" {
			return true
		}
		for _, sc := range scopes {
			if sc == lr.scope {
				return true
			}
		}
	}
	return false
}

// linkUser validates a pcx_ token, checks the route against its scopes and
// maps it to the person it acts for. The second value is the acting app's slug.
func (s *Server) linkUser(ctx context.Context, tok string, r *http.Request) (*store.User, string, error) {
	in, err := s.core.Introspect(ctx, tok)
	if err != nil {
		return nil, "", err
	}
	if !in.Active || !in.Delegated() || in.Audience != "remote" || in.User == nil || in.User.Email == "" || in.App == nil || in.App.Slug == "" {
		return nil, "", http.ErrNoCookie
	}
	if !linkAllows(strings.Fields(in.Scope), r) {
		return nil, "", errLinkScope
	}
	u, err := s.coreIdentityUser(ctx, in)
	return u, in.App.Slug, err
}

const linkAppKey ctxKey = 2

// linkApp is the acting app's slug when the request came in on a link token.
func linkApp(ctx context.Context) string {
	a, _ := ctx.Value(linkAppKey).(string)
	return a
}
