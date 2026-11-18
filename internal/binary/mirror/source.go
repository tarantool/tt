package mirror

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/untar"
)

// Source is a mirror as a source of prebuilt archives: the archives its index
// lists, read from disk, with no network. It has the products the index has
// archives of, Community and Enterprise alike; an SDK archive of tarantool-ee
// answers for tarantool-sdk too, as the customer zone does.
type Source struct {
	// dir is the mirror, the directory that holds DirName.
	dir   string
	index Index
}

var _ bundle.Source = (*Source)(nil)

// Open returns the mirror in dir as a source. found is false when dir has
// no index of binaries.
func Open(dir string) (*Source, bool, error) {
	index, found, err := ReadIndex(dir)
	if err != nil || !found {
		return nil, found, err
	}

	return &Source{dir: dir, index: index}, true, nil
}

// Dir returns the directory of the mirror.
func (s *Source) Dir() string {
	return s.dir
}

// Name implements bundle.Source.
func (s *Source) Name() string {
	return "mirror " + s.dir
}

// Products implements bundle.Source.
func (s *Source) Products() []string {
	var products []string

	for _, entry := range s.index.Binaries {
		names := []string{entry.Product}
		if entry.Product == binary.ProgramEe.String() {
			names = append(names, binary.ProgramSDK.String())
		}

		for _, name := range names {
			if !slices.Contains(products, name) {
				products = append(products, name)
			}
		}
	}

	return products
}

// List implements bundle.Source. The mirror keeps releases only, so a query
// of development builds has none.
func (s *Source) List(_ context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	if query.Channel == bundle.ChannelDev {
		return nil, nil
	}

	var artifacts []bundle.Artifact

	for _, entry := range s.index.Binaries {
		if !answers(entry.Product, query.Product) || entry.OS != query.OS ||
			entry.Arch != query.Arch || !onLine(query.Version, entry.Version) {
			continue
		}

		artifacts = append(artifacts, s.artifact(entry))
	}

	return artifacts, nil
}

// answers reports whether an archive of product answers a query of
// queried: its own product, and tarantool-sdk of an SDK archive of
// tarantool-ee.
func answers(product, queried string) bool {
	return product == queried ||
		product == binary.ProgramEe.String() && queried == binary.ProgramSDK.String()
}

// onLine reports whether ver is of the release line, as bundle.Query.Version
// names it: the whole version, or a prefix of it - "3" and "3.9" of 3.9.0.
// An empty line has every version; a leading v is ignored on both.
func onLine(line, ver string) bool {
	line = strings.TrimPrefix(line, "v")
	ver = strings.TrimPrefix(ver, "v")

	return line == "" || ver == line || strings.HasPrefix(ver, line+".") ||
		strings.HasPrefix(ver, line+"-")
}

// Fetch implements bundle.Source. The bytes are checked against the digest
// of the index.
func (s *Source) Fetch(_ context.Context, artifact bundle.Artifact, dst io.Writer) error {
	file, err := os.Open(artifact.Locator)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", bundle.ErrNotFound, artifact.Name, err)
	}

	defer func() { _ = file.Close() }()

	digest := sha256.New()

	_, err = io.Copy(io.MultiWriter(dst, digest), file)
	if err != nil {
		return fmt.Errorf("reading %s: %w", artifact.Locator, err)
	}

	actual := hex.EncodeToString(digest.Sum(nil))
	if artifact.SHA256 != "" && !strings.EqualFold(actual, artifact.SHA256) {
		return fmt.Errorf("%w: %s has SHA-256 %s, %s lists %s", errChecksum,
			artifact.Locator, actual, IndexPath(s.dir), artifact.SHA256)
	}

	return nil
}

// Find returns the archive of the build of program for goos/goarch that tt
// binary installs as installation, the name of a version as tt binary names
// it: the pin of a lock, say. ok is false when the mirror has none.
func (s *Source) Find(program binary.Program, installation, goos, goarch string) (
	bundle.Artifact, bool,
) {
	for _, entry := range s.index.Binaries {
		if entry.Product == program.String() && entry.OS == goos && entry.Arch == goarch &&
			InstallationOf(program, entry.Version, entry.Flavor) == installation {
			return s.artifact(entry), true
		}
	}

	return bundle.Artifact{
		Version: "", Flavor: nil, Name: "", Size: 0, SHA256: "", Locator: "",
	}, false
}

// Extract unpacks the archive of artifact, checked against the digest of the
// index, into dir, which it creates. An archive keeps the modes of its files,
// the executable ones among them, and holds no links.
func (s *Source) Extract(ctx context.Context, artifact bundle.Artifact, dir string) error {
	err := os.MkdirAll(dir, dirMode)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	temp, err := os.CreateTemp(filepath.Dir(dir), ".archive-*")
	if err != nil {
		return fmt.Errorf("unpacking %s: %w", artifact.Name, err)
	}

	defer func() { _ = os.Remove(temp.Name()) }()

	err = s.Fetch(ctx, artifact, temp)

	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = untar.ExtractTarGz(temp.Name(), dir, untar.Options{
			Symlinks: false, EntryModes: true,
		})
	}

	if err != nil {
		return fmt.Errorf("unpacking %s: %w", artifact.Locator, err)
	}

	return nil
}

// Matches reports whether the mirror has a build of program for goos/goarch
// that answers requested, a version tt binary install is asked for: any
// build for none, else the one installed as requested, the one of that
// archive version, flavours included, or one of that release line.
func (s *Source) Matches(program binary.Program, requested, goos, goarch string) bool {
	for _, entry := range s.index.Binaries {
		if !answers(entry.Product, program.String()) || entry.OS != goos || entry.Arch != goarch {
			continue
		}

		installation := InstallationOf(program, entry.Version, entry.Flavor)
		archive := strings.Join(append(slices.Clone(entry.Flavor), entry.Version), "-")

		if requested == "" || requested == installation || requested == archive ||
			installation != "" && onLine(requested, installation) {
			return true
		}
	}

	return false
}

// artifact is the archive of entry.
func (s *Source) artifact(entry Entry) bundle.Artifact {
	return bundle.Artifact{
		Version: entry.Version,
		Flavor:  slices.Clone(entry.Flavor),
		Name:    entry.File,
		Size:    entry.Size,
		SHA256:  entry.SHA256,
		Locator: filepath.Join(s.dir, DirName, entry.File),
	}
}
