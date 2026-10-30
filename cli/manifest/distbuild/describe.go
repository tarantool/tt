package distbuild

import (
	"context"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/tarantool/tt/v3/internal/buildflags"
)

// describeRe splits git describe --tags --long: the tag, the number of
// commits since it and the abbreviated commit.
var describeRe = regexp.MustCompile(`^(.+)-(\d+)-g([0-9a-f]+)$`)

// describe returns the version of the distribution in dir, as the magefile
// of tt tells tt's own: the tag the commit is at or descends from, the
// number of commits since and the commit, with label. A directory that is
// not in a git repository has no version.
func describe(ctx context.Context, dir, label string) versionInfo {
	commit := gitOutput(ctx, dir, "rev-parse", "--short", "HEAD")
	if commit == "" {
		return versionInfo{Tag: "", Commit: "", CommitsSinceTag: 0, Label: ""}
	}

	info := versionInfo{Tag: "", Commit: commit, CommitsSinceTag: 0, Label: label}

	match := describeRe.FindStringSubmatch(gitOutput(ctx, dir, "describe", "--tags", "--long"))
	if match == nil {
		return info
	}

	since, err := strconv.Atoi(match[2])
	if err != nil {
		return info
	}

	info.Tag = match[1]
	info.CommitsSinceTag = since

	if since != 0 {
		// As git describe --tags prints a commit past the tag.
		info.Tag = match[0]
	}

	return info
}

// ldflags returns the linker flags that stamp v into a tt binary as the
// version of its core, or none when v says nothing.
func (v versionInfo) ldflags() []string {
	if !v.known() {
		return nil
	}

	return buildflags.Version{
		Tag:             v.Tag,
		Commit:          v.Commit,
		CommitsSinceTag: strconv.Itoa(v.CommitsSinceTag),
		Label:           v.Label,
	}.Ldflags()
}

// gitOutput runs git with args in dir and returns its trimmed output, or
// nothing when it fails.
func gitOutput(ctx context.Context, dir string, args ...string) string {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)

	out, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
