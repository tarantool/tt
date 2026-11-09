package customerzone

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

// Dir is a directory of archives as the customer zone names them: what
// --local-dir names. It has no channels and no release lines: every archive
// of the product is listed, but those of tt-ee off the line of the query.
type Dir struct {
	path string
}

var _ bundle.Source = (*Dir)(nil)

// NewDir returns the directory at path as a source.
func NewDir(path string) *Dir {
	return &Dir{path: path}
}

// Name implements bundle.Source.
func (*Dir) Name() string {
	return "local-dir"
}

// Products implements bundle.Source.
func (*Dir) Products() []string {
	return productNames()
}

// List implements bundle.Source. A directory that does not exist has no
// archives; a file named as an archive of the product whose version tt
// cannot read is an error.
func (d *Dir) List(_ context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	program, err := parseProduct(query.Product)
	if err != nil {
		return nil, err
	}

	suffix, err := platformSuffix(program, query.OS, query.Arch)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(d.path)
	if errors.Is(err, fs.ErrNotExist) {
		log.Debugf("Directory not found, cannot search for local archives")

		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	prefix := archivePrefix(program)

	var artifacts []bundle.Artifact

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}

		artifact, ok, err := parseArchive(program, name)
		if err != nil {
			return nil, fmt.Errorf("failed to parse version from file %s: %w", name, err)
		}

		if !ok {
			continue
		}

		if program == binary.ProgramTtEe {
			// The line of tt-ee is matched by version, as in the customer zone.
			parsed, err := version.Parse(artifact.Version)
			if err != nil || !onReleaseLine(query.Version, parsed) {
				continue
			}
		}

		artifact.Locator = filepath.Join(d.path, name)
		artifacts = append(artifacts, artifact)
	}

	if len(artifacts) == 0 {
		log.Debugf("No local archives found for %q", program)
	}

	return artifacts, nil
}

// Fetch implements bundle.Source.
func (*Dir) Fetch(_ context.Context, artifact bundle.Artifact, dst io.Writer) error {
	file, err := os.Open(artifact.Locator)
	if err != nil {
		return fmt.Errorf("local bundle file not found: %w", err)
	}

	defer func() { _ = file.Close() }()

	_, err = io.Copy(dst, file)
	if err != nil {
		return fmt.Errorf("failed to copy local bundle %s: %w", artifact.Locator, err)
	}

	return nil
}
