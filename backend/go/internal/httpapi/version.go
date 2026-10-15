package httpapi

import (
	"net/http"
	"strings"
)

// # GET /version
//
// The handshake an instance answers so it can be recognised as running this
// software. It is what a checker probes and what the public index is built from.
//
// The governing constraint is that it must answer **with the database switched
// off**. An instance that is only a CDN in front of a bucket, with no API behind
// it, has no database to ping, and failing to be recognised for that reason is a
// failure of the checker rather than of the instance. So the handler reads
// nothing: not the repository, not the connection pool, not the configuration.
// TestVersionNeedsNoDependencies pins that by calling it on a zero-value Server.

// softwareName identifies this project in the handshake.
//
// It is deliberately not the repository name: the protocol is "this software",
// and a fork or a reimplementation answers it too. Naming the software rather
// than the instance is what makes the check compatible rather than proprietary.
const softwareName = "waifu-im"

// Build identity, stamped at link time.
//
// -X takes an import path and a variable name, so these must live in the package
// that reads them: `main.version` only resolves for a var in package main, and
// the flag naming a symbol that does not exist is silently ignored by the
// linker. That is why this file exists rather than a var in cmd/server.
//
// The defaults are sentinels rather than empty strings, because -X with an empty
// value overwrites the default with "". "unknown" is the honest answer for a
// deployment whose builder supplied no commit; "" reads like a bug.
var (
	// BuildVersion is the release this binary was cut from.
	BuildVersion = "0.0.0-dev"

	// BuildCommit is the source revision, or "unknown".
	//
	// It cannot be derived from the build context: the context is backend/go
	// and .git lives at the repository root, so it is never present. Whoever
	// builds the image has to pass it.
	BuildCommit = "unknown"

	// BuildAt is when the image was built, RFC 3339, or "unknown".
	//
	// This is the staleness signal that survives forks. A commit says which
	// code an instance runs; the build date says how long it has been left
	// standing still, which is true regardless of whose code it is.
	BuildAt = "unknown"
)

// buildIdentity is the handshake payload.
//
// `version` is reported but is not a trust input: it is a string the instance
// says about itself, and a fork can bump it freely. Only `builtAt` and the
// existence of `commit` carry weight elsewhere.
func buildIdentity() map[string]any {
	return map[string]any{
		"software": softwareName,
		"version":  BuildVersion,
		"commit":   BuildCommit,
		"builtAt":  BuildAt,
	}
}

// handleVersion serves the handshake.
//
// Unauthenticated, like /ping and /healthz, because the whole point is that
// something with no credentials can ask. It reads nothing from the Server — see
// the file comment — so the Server being zero-valued is not a test artefact but
// the contract.
func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, buildIdentity())
}

// Stamped reports whether the binary carries a real build identity rather than
// the link-time defaults.
//
// All three must be present. A deployment whose builder supplied only a version
// is genuinely unstamped on the commit, and saying so in the log once at startup
// is how the operator learns the build args were not passed — which is
// information this repository does not otherwise have, because the build context
// does not contain .git.
//
// main warns when it is false. The CI image smoke test asserts the opposite of
// what this asserts here: a build that lost its -ldflags would serve
// "0.0.0-dev" from /version forever, which is a deployment that reports looking
// current while being nothing of the sort.
func Stamped() bool {
	return BuildVersion != "0.0.0-dev" &&
		!strings.EqualFold(BuildCommit, "unknown") &&
		!strings.EqualFold(BuildAt, "unknown")
}
