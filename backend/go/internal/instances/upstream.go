package instances

import (
	"bufio"
	_ "embed"
	"strconv"
	"strings"
	"time"
)

//go:embed upstream_commits.txt
var upstreamCommitLog string

// # Upstream resolution
//
// The question the index asks about provenance is one bit: does the commit this
// instance reports exist in this repository's history? Either it does, and the
// commit's age is free, or it does not, and every field derived from a commit is
// absent.
//
// There is no third answer of "behind". A fork — or a clean reimplementation —
// has a hash that resolves in exactly one history, and there is no shared
// ancestry against which "how far behind" could be measured. Computing a number
// anyway would produce a figure with nothing behind it.
//
// # Where the history comes from
//
// The build cannot read git. The build context is backend/go and .git lives at
// the repository root, so no VCS metadata is present; that is a structural
// property of where the Dockerfile is run from, not a missing flag.
//
// So the history is bundled: tools/commits writes the reachable commits and
// their dates into upstream_commits.txt, and this file reads them back. The
// bundle is a build input like any other committed artefact.
//
// # What that costs
//
// The bundle is a snapshot. A commit made after it was generated does not
// resolve, so an instance running our own newest code is reported as "we could
// not tell" rather than as upstream — a false negative, and the safe direction
// of the two. Regenerating it is one command, and CI checks that it is current.

// keyLength is the number of hex characters per commit in the bundle. It is 12
// because 48 bits is enough to make a collision inside one history implausible,
// and because it is the longest abbreviation people habitually read.
const keyLength = 12

type upstreamCommit struct {
	when time.Time
}

// upstream resolves commit hashes reported by instances.
//
// It is keyed on the unambiguous short form — the first 12 hex characters —
// because that is what /version reports and because an instance may abbreviate
// to seven. Seven is not enough to key on: 12 hex characters is 48 bits, and the
// birthday bound at a few thousand commits makes a collision in this repository
// implausible rather than merely unlikely. A shorter report is matched by
// prefix scan, which is safe because it is checked against the whole list.
func upstream() map[string]upstreamCommit {
	resolved := make(map[string]upstreamCommit)

	scanner := bufio.NewScanner(strings.NewReader(upstreamCommitLog))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "\t")
		if len(parts) != 2 {
			continue
		}

		seconds, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}

		resolved[parts[0]] = upstreamCommit{when: time.Unix(seconds, 0).UTC()}
	}
	return resolved
}

// upstreamIndex is the parsed bundle, built once.
//
// Parsing is cheap but the file is read on every probe, and a prober that
// re-parses a few hundred lines per instance per day is doing work for nothing.
var upstreamIndex = upstream()

// ResolveCommit reports the commit's date when it exists in this repository's
// history.
//
// The returned bool says whether the commit resolved. A false return leaves
// Upstream and CommitAgeDays absent on the caller: absent means we could not
// tell, which is a different claim from "behind", and both are different from
// "forked" — the index cannot distinguish a fork from an instance running a
// commit newer than the bundle.
func ResolveCommit(commit string, now time.Time) (bool, int) {
	commit = strings.TrimSpace(strings.ToLower(commit))
	if len(commit) < 7 {
		return false, 0
	}

	// A full 40-character hash is reduced to the bundle's key length rather than
	// rejected: an instance built with a different abbreviation setting reports
	// the whole thing, and comparing it against 12-character keys without
	// truncating would report this repository's own commits as unresolvable.
	if len(commit) > keyLength {
		commit = commit[:keyLength]
	}

	if found, ok := upstreamIndex[commit]; ok {
		return true, ageInDays(found.when, now)
	}

	// A report shorter than the bundle's key length is matched by prefix. The
	// shortest prefix accepted is 7 characters, which is the shortest git
	// abbreviates to by default and is enough to be unique in any real history.
	if len(commit) < keyLength {
		for hash, found := range upstreamIndex {
			if strings.HasPrefix(hash, commit) {
				return true, ageInDays(found.when, now)
			}
		}
	}

	return false, 0
}

// ageInDays is whole days between a commit and now, never negative.
func ageInDays(then, now time.Time) int {
	days := int(now.Sub(then).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}
