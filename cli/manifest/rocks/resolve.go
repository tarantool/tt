package rocks

import (
	"context"
	"fmt"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/go-luarocks/remote"
)

// ResolvedRock is a rock chosen for a dependency: the name, the version picked
// from the winning server, and the URL of its resource on that server.
type ResolvedRock struct {
	// Name is the rock name.
	Name string
	// Version is the chosen version.
	Version luarocks.Version
	// URL is the .rock / .rockspec resource on the originating server.
	URL string
	// SHA256 is the sha256 in hex the server publishes for the resource at
	// URL; empty for a server that publishes none (a classic manifest).
	SHA256 string
	// SpecURL and SpecSHA256 are the rockspec the server publishes apart
	// from its rocks, and its sha256 in hex, as a tt-rocks-index.json lists
	// it: Metadata reads it instead of downloading the rock. Empty
	// otherwise.
	SpecURL    string
	SpecSHA256 string
	// Artifacts are every file the server offers for the version, each
	// with its sha256 - the rock of each platform and the rockspec - which
	// the lock records; nil for a server that publishes no digests.
	Artifacts []luarocks.Artifact
}

// serverIndex builds the ordered first-found-wins index over servers. Each
// entry is dispatched by remote.NewIndex on its form, so a bare directory path
// or a file:// URL is read off disk as a rock server while an http(s) server
// goes over the network; both compose in one ordered list.
func serverIndex(servers, insecure []string) luarocks.RemoteIndex {
	return remote.NewOrderedIndex(remote.NewIndexes(servers, remote.IndexOptions{
		InsecureServers: insecure,
		UserAgent:       "",
		LuaVersion:      "",
		Arch:            "",
	})...)
}

// Resolve finds the newest version of name that satisfies constraintExpr,
// querying servers in order (first-found-wins). A non-empty registry overrides
// the server list with that single server. An empty constraintExpr matches any
// version.
func (a *Adapter) Resolve(
	ctx context.Context, name, constraintExpr, registry string,
) (ResolvedRock, error) {
	index := a.index
	if registry != "" {
		index = serverIndex([]string{registry}, a.cfg.InsecureServers)
	}

	return resolveWith(ctx, index, name, constraintExpr)
}

// resolveWith is the index-agnostic core of Resolve, separated so tests can
// drive it with a fake index.
func resolveWith(
	ctx context.Context, index luarocks.RemoteIndex, name, constraintExpr string,
) (ResolvedRock, error) {
	candidates, err := index.Query(ctx, name, "")
	if err != nil {
		return ResolvedRock{}, fmt.Errorf("rocks: resolve %q: %w", name, err)
	}

	if len(candidates) == 0 {
		return ResolvedRock{}, fmt.Errorf("%q: %w", name, ErrNotFound)
	}

	constraints, err := deps.ParseConstraints(constraintExpr)
	if err != nil {
		return ResolvedRock{}, fmt.Errorf("rocks: parse constraints %q: %w", constraintExpr, err)
	}

	best, ok := pickNewest(candidates, constraints)
	if !ok {
		return ResolvedRock{}, fmt.Errorf("%q %q: %w%s", name, constraintExpr, ErrNoMatch,
			yankedNote(candidates, constraints))
	}

	return ResolvedRock{
		Name:       best.Name,
		Version:    best.Version,
		URL:        best.URL,
		SHA256:     best.SHA256,
		SpecURL:    best.SpecURL,
		SpecSHA256: best.SpecSHA256,
		Artifacts:  best.Artifacts,
	}, nil
}

// pickNewest returns the highest-versioned candidate satisfying every
// constraint. An empty constraint list matches all. ok is false when nothing
// matches.
//
// Released versions win over moving ones (Moving). LuaRocks orders scm and dev
// above every number, so an ordinary lower bound like ">=3.0.0" would otherwise
// select the development branch and pin a lock to something that changes
// underneath it. A moving version is chosen only when the constraints name one
// outright, or when the rock has no released version at all. A version tt
// made from a commit is a release here: it names the commit.
//
// The fallback deliberately turns on the rock's publication history rather than
// on whether the stable pass matched: were it the latter, a constraint no
// release satisfies would quietly be answered with the branch, which is the
// same surprise by a narrower door.
func pickNewest(
	candidates []luarocks.VersionedRock, constraints []luarocks.VersionConstraint,
) (luarocks.VersionedRock, bool) {
	stableOnly := hasRelease(candidates) && !wantsMoving(constraints)

	return pickNewestAmong(candidates, constraints, stableOnly)
}

// hasRelease reports whether any candidate names a release, as opposed to the
// rock being published only as a branch.
func hasRelease(candidates []luarocks.VersionedRock) bool {
	for _, candidate := range candidates {
		if !Moving(candidate.Version) {
			return true
		}
	}

	return false
}

// wantsMoving reports whether the constraints name a moving version outright,
// which is how a project asks for the development branch on purpose.
func wantsMoving(constraints []luarocks.VersionConstraint) bool {
	for _, constraint := range constraints {
		if Moving(constraint.Version) {
			return true
		}
	}

	return false
}

// pickNewestAmong is pickNewest over one class of candidates: stableOnly skips
// the moving versions, otherwise every candidate is considered.
//
// A version the server yanked is passed over unless an == constraint pins it
// (deps.MatchRock), as a lock's pin does: a fresh resolve does not choose a
// withdrawn version, while a lock that recorded it keeps resolving to it and
// installs it. A yanked release still counts as one for hasRelease, so a rock
// whose only release is yanked is not answered with its development branch.
func pickNewestAmong(
	candidates []luarocks.VersionedRock, constraints []luarocks.VersionConstraint,
	stableOnly bool,
) (luarocks.VersionedRock, bool) {
	var best luarocks.VersionedRock

	found := false

	for _, candidate := range candidates {
		if stableOnly && Moving(candidate.Version) {
			continue
		}

		if !deps.MatchRock(candidate, constraints) {
			continue
		}

		if !found || deps.Compare(candidate.Version, best.Version) > 0 {
			best = candidate
			found = true
		}
	}

	return best, found
}

// yankedNote is what a resolve that found no version adds to ErrNoMatch when
// versions the constraints allow were there but yanked, and "" otherwise.
func yankedNote(
	candidates []luarocks.VersionedRock, constraints []luarocks.VersionConstraint,
) string {
	for _, candidate := range candidates {
		if candidate.Yanked && deps.Match(candidate.Version, constraints) {
			return " (the versions that match are yanked; pin one with == to use it)"
		}
	}

	return ""
}
