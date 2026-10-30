package httpapi

import (
	"net/http"
	"strings"
)

// # GET /
//
// The index. It exists because the root was the one path in the service that
// answered with something other than JSON: net/http's own 404, in text/plain,
// to a client asking "what is this, and what can I do with it".
//
// Like /version it reads nothing. The links are built from the same base-path
// expression the route patterns are built from, so the index cannot point at a
// path the mux does not serve — TestRootLinksAreServed pins that by resolving
// every link against the registered mux, at a root and at a prefixed base.

// rootLinks names the public reads advertised at the root.
//
// Every one of these is served without credentials. An index that advertised a
// route answering 401 or 403 would be worse than no index at all, because a
// client reads the link as a promise.
//
// The keys are names rather than the paths themselves, so the index reads as
// links instead of as a path list.
func rootLinks(base string) map[string]string {
	return map[string]string{
		"images":    base + "images",
		"tags":      base + "tags",
		"artists":   base + "artists",
		"banner":    base + "banner",
		"stats":     base + "stats/public",
		"instances": base + "instances",
		"version":   base + "version",
	}
}

// registerRootRoute mounts GET /.
//
// The pattern needs anchoring, because a bare "GET /" is a subtree match in
// net/http: it would answer every GET no other route claimed — every mistyped
// path, every stray asset request — with this document and a 200, and a typo
// would stop looking like a typo. A path with no trailing slash matches exactly
// itself, so a prefixed base needs no anchor; the root does, and {$} is what
// gives it one.
func (s *Server) registerRootRoute(mux *http.ServeMux) {
	base := s.cfg.BasePathOrRoot()

	pattern := "GET " + base
	if strings.HasSuffix(base, "/") {
		pattern += "{$}"
	}

	mux.HandleFunc(pattern, s.handleRoot(base))
}

// handleRoot serves the index.
//
// base is passed in rather than read from s, so the payload still depends on
// nothing the Server holds: it is a closure over the same string the route
// patterns were built from.
func (s *Server) handleRoot(base string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		index := buildIdentity()
		index["links"] = rootLinks(base)

		writeJSON(w, http.StatusOK, index)
	}
}
