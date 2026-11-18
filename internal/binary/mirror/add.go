package mirror

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	// ErrNoArchive reports a program Add found no archive of: no
	// installation of the version to repack and no source that has it.
	ErrNoArchive = errors.New("no archive of the version")
	// ErrContents reports an archive that lacks what it must carry: the
	// executable, and the headers of a tarantool.
	ErrContents = errors.New("incomplete archive")
	// errFileChanged reports a file of an installation that changed while it
	// was repacked.
	errFileChanged = errors.New("file changed while it was archived")
)

// licenseNames are the license files tt keeps at the root of an installation
// and of the archive of a Community product.
//
//nolint:gochecknoglobals // A constant list.
var licenseNames = []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING", "AUTHORS"}

const (
	// headersDir and shareDir are the trees of a tarantool: its headers and
	// its Lua files, the EmmyLua stubs of its built-in modules among them.
	headersDir = "include/tarantool"
	shareDir   = "share/tarantool"
	stubsDir   = shareDir + "/emmylua"
	moduleH    = headersDir + "/module.h"
	// sdkRoot is the directory an SDK archive of the customer zone holds
	// tarantool-ee in.
	sdkRoot = "tarantool-enterprise"
	// legacyTcm is the executable of older TCM archives.
	legacyTcm = "tarantool-cluster-manager"
	// stubsSince is the first release of Tarantool with EmmyLua stubs.
	stubsSinceMajor, stubsSinceMinor = 3, 9
	executableMode                   = 0o755
)

// Request is a program to put into a mirror.
type Request struct {
	// Program is the product.
	Program binary.Program
	// Version is the version, as tt binary names the installation of it: the
	// pin of a lock.
	Version string
	// Installation is the tt binary installation of exactly that version,
	// repacked into the archive when the product can be (Repackable); empty
	// for none.
	Installation string
	// Source supplies the archive otherwise; nil for none.
	Source bundle.Source
	// OS and Arch are the platform of the build.
	OS, Arch string
}

// Result is what Add put into the mirror.
type Result struct {
	// Entry is the entry of the archive in the index.
	Entry Entry
	// Path is the archive.
	Path string
	// From says where it came from: the installation, or the source.
	From string
	// Warnings are what the archive lacks that it should have: the EmmyLua
	// stubs of a tarantool that has them.
	Warnings []string
}

// Repackable reports whether an installation of program can be repacked into
// the archive the program is installed from: the Community tarantool and tt,
// whose archives tt defines, and TCM and tt-ee, whose archives of the
// customer zone hold the executable alone. tarantool-ee is installed from an
// SDK archive, whose build, gc64 or not, an installation does not record.
func Repackable(program binary.Program) bool {
	switch program {
	case binary.ProgramCe, binary.ProgramTt, binary.ProgramTcm, binary.ProgramTtEe:
		return true
	default:
		return false
	}
}

// Add puts the archive of the build req names into the mirror in dir, and
// lists it in the index: repacked from the installation, or fetched from the
// source. An archive of the same build that is there already is replaced, and
// the rest of the mirror is kept. The archive is checked before it is listed:
// one without its executable, or a tarantool without include/tarantool/
// module.h, is refused, and a tarantool 3.9 or later without its EmmyLua
// stubs is a warning.
func Add(ctx context.Context, dir string, req Request) (Result, error) {
	binaries := filepath.Join(dir, DirName)

	err := os.MkdirAll(binaries, dirMode)
	if err != nil {
		return Result{}, fmt.Errorf("creating %s: %w", binaries, err)
	}

	temp, err := os.CreateTemp(binaries, ".download-*")
	if err != nil {
		return Result{}, fmt.Errorf("creating an archive in %s: %w", binaries, err)
	}

	defer func() { _ = os.Remove(temp.Name()) }()

	entry, from, err := writeArchive(ctx, temp, req)

	closeErr := temp.Close()
	if err == nil && closeErr != nil {
		err = fmt.Errorf("writing %s: %w", temp.Name(), closeErr)
	}

	if err != nil {
		return Result{}, err
	}

	warnings, err := checkArchive(temp.Name(), req.Program, req.Version)
	if err != nil {
		return Result{}, fmt.Errorf("%s %s from %s: %w", req.Program, req.Version, from, err)
	}

	entry.File = fileName(entry)

	target := filepath.Join(binaries, entry.File)

	err = os.Chmod(temp.Name(), fileMode)
	if err == nil {
		err = os.Rename(temp.Name(), target)
	}

	if err != nil {
		return Result{}, fmt.Errorf("writing %s: %w", target, err)
	}

	index, _, err := ReadIndex(dir)
	if err != nil {
		return Result{}, err
	}

	index.Format = IndexFormat
	index.put(entry)

	err = writeIndex(dir, index)
	if err != nil {
		return Result{}, err
	}

	return Result{Entry: entry, Path: target, From: from, Warnings: warnings}, nil
}

// fileName is the name of the archive of entry in the mirror:
// <product>[-<flavor>...]-<version>-<os>-<arch>.tar.gz.
func fileName(entry Entry) string {
	parts := slices.Concat([]string{entry.Product}, entry.Flavor,
		[]string{strings.TrimPrefix(entry.Version, "v"), entry.OS, entry.Arch})

	return strings.Join(parts, "-") + ".tar.gz"
}

// writeArchive writes the archive of req to file and returns its entry,
// without its file name, and where it came from.
func writeArchive(ctx context.Context, file *os.File, req Request) (Entry, string, error) {
	entry := Entry{
		Product: req.Program.String(), Version: req.Version, Flavor: nil,
		OS: req.OS, Arch: req.Arch, File: "", SHA256: "", Size: 0,
	}

	digest := sha256.New()
	counter := &countingWriter{written: 0}
	out := io.MultiWriter(file, digest, counter)

	var from string

	switch {
	case req.Installation != "" && Repackable(req.Program):
		from = "the installation " + req.Installation

		err := repack(out, req.Program, req.Installation)
		if err != nil {
			return Entry{}, from, fmt.Errorf("repacking %s: %w", req.Installation, err)
		}
	case req.Source != nil:
		from = req.Source.Name()

		artifact, err := fetch(ctx, out, req)
		if err != nil {
			return Entry{}, from, err
		}

		entry.Version, entry.Flavor = artifact.Version, slices.Clone(artifact.Flavor)

		sum := hex.EncodeToString(digest.Sum(nil))
		if artifact.SHA256 != "" && !strings.EqualFold(sum, artifact.SHA256) {
			return Entry{}, from, fmt.Errorf("%w: %s has SHA-256 %s, %s lists %s",
				errChecksum, artifact.Name, sum, from, artifact.SHA256)
		}
	default:
		return Entry{}, "", fmt.Errorf("%w: %s %s is not installed, and this build of tt "+
			"has no source of it", ErrNoArchive, req.Program, req.Version)
	}

	entry.SHA256 = hex.EncodeToString(digest.Sum(nil))
	entry.Size = counter.written

	return entry, from, nil
}

// countingWriter counts the bytes written through it.
type countingWriter struct {
	written int64
}

func (w *countingWriter) Write(data []byte) (int, error) {
	w.written += int64(len(data))

	return len(data), nil
}

// fetch writes the archive of the build of req the source has to out.
func fetch(ctx context.Context, out io.Writer, req Request) (bundle.Artifact, error) {
	query := bundle.Query{
		Product: req.Program.String(), OS: req.OS, Arch: req.Arch,
		Channel: bundle.ChannelRelease, Version: queryVersion(req.Program, req.Version),
	}

	bundles, err := binary.ListBundles(ctx, req.Source, req.Program, query)
	if err != nil {
		return bundle.Artifact{}, err
	}

	found, ok := pick(req.Program, req.Version, bundles)
	if !ok {
		return bundle.Artifact{}, fmt.Errorf("%w: %s has no %s %s for %s/%s", ErrNoArchive,
			req.Source.Name(), req.Program, req.Version, req.OS, req.Arch)
	}

	err = req.Source.Fetch(ctx, found.Artifact, out)
	if err != nil {
		return bundle.Artifact{}, fmt.Errorf("downloading %s from %s: %w", found.Artifact.Name,
			req.Source.Name(), err)
	}

	return found.Artifact, nil
}

// queryVersion is the version a source is asked for: the tag of a Community
// release as the repository spells it, v2.15.0 of tt, and the release line of
// the others, the major and minor of the version.
func queryVersion(program binary.Program, ver string) string {
	switch program {
	case binary.ProgramCe:
		return ver
	case binary.ProgramTt:
		return "v" + strings.TrimPrefix(ver, "v")
	default:
		parsed, err := version.Parse(ver)
		if err != nil {
			return ""
		}

		return fmt.Sprintf("%d.%d", parsed.Major, parsed.Minor)
	}
}

// pick chooses the build tt binary installs as installation among bundles,
// sorted from the oldest to the newest: the release without flavours of a
// Community product, and of the others the newest revision, a gc64 one of
// an SDK first.
func pick(program binary.Program, installation string, bundles binary.Bundles) (
	binary.Bundle, bool,
) {
	var (
		fallback binary.Bundle
		matched  bool
	)

	for _, found := range slices.Backward(bundles) {
		if found.IsDebug() ||
			InstallationOf(program, found.Artifact.Version, found.Artifact.Flavor) != installation {
			continue
		}

		if program.BundleSource() != binary.ProgramSDK ||
			slices.Contains(found.Artifact.Flavor, "gc64") {
			return found, true
		}

		if !matched {
			fallback, matched = found, true
		}
	}

	return fallback, matched
}

// InstallationOf returns the name tt binary installs the build of program of
// the version and flavours as: the tag of a Community release without its v,
// and the installation an archive of the customer zone is installed as
// (binary.BundleInstallationID). Empty for a build whose version tt cannot
// read, and for a Community build with flavours, which is no release.
func InstallationOf(program binary.Program, ver string, flavor []string) string {
	if program == binary.ProgramCe || program == binary.ProgramTt {
		if len(flavor) != 0 {
			return ""
		}

		return strings.TrimPrefix(ver, "v")
	}

	parsed, err := version.Parse(strings.Join(append(slices.Clone(flavor), ver), "-"))
	if err != nil {
		return ""
	}

	return binary.BundleInstallationID(program, parsed, false)
}

// repack writes the installation of program at dir to out as the archive
// it is installed from: the executable and the license files at the root
// and, of a tarantool, include/tarantool and share/tarantool. The entries
// are sorted and carry no times or owners, so the same installation packs
// to the same bytes.
func repack(out io.Writer, program binary.Program, dir string) error {
	compressed, err := gzip.NewWriterLevel(out, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("compressing: %w", err)
	}

	archive := tar.NewWriter(compressed)

	err = repackEntries(archive, program, dir)
	if err != nil {
		return err
	}

	err = archive.Close()
	if err == nil {
		err = compressed.Close()
	}

	if err != nil {
		return fmt.Errorf("writing the archive: %w", err)
	}

	return nil
}

func repackEntries(archive *tar.Writer, program binary.Program, dir string) error {
	err := writeFile(archive, program.Exec(), filepath.Join(dir, program.Exec()), executableMode)
	if err != nil {
		return err
	}

	for _, name := range licenseNames {
		source := filepath.Join(dir, name)
		if binary.IsRegularFile(source) {
			err = writeFile(archive, name, source, fileMode)
			if err != nil {
				return err
			}
		}
	}

	if !program.IsTarantool() {
		return nil
	}

	for _, tree := range []string{headersDir, shareDir} {
		source := filepath.Join(dir, filepath.FromSlash(tree))
		if !binary.IsDir(source) {
			continue
		}

		err = writeTree(archive, tree, source)
		if err != nil {
			return err
		}
	}

	return nil
}

// writeTree writes the directory source as the tree name of the archive,
// its parents included. A link is written as what it leads to when that is
// a regular file of the tree, and is left out otherwise.
func writeTree(archive *tar.Writer, name, source string) error {
	for _, parent := range []string{path.Dir(name), name} {
		err := writeDir(archive, parent)
		if err != nil {
			return err
		}
	}

	root, err := os.OpenRoot(source)
	if err != nil {
		return fmt.Errorf("opening %s: %w", source, err)
	}

	defer func() { _ = root.Close() }()

	//nolint:wrapcheck // The function below wraps every error it returns.
	return fs.WalkDir(root.FS(), ".", func(inside string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("reading %s: %w", source, err)
		}

		if inside == "." {
			return nil
		}

		member := name + "/" + inside

		info, err := root.Stat(inside)

		switch {
		case err == nil && entry.IsDir():
			return writeDir(archive, member)
		case err == nil && info.Mode().IsRegular():
			return writeFile(archive, member, filepath.Join(source, inside), info.Mode().Perm())
		default:
			return nil
		}
	})
}

func writeDir(archive *tar.Writer, name string) error {
	err := archive.WriteHeader(&tar.Header{Typeflag: tar.TypeDir, Name: name + "/", Mode: dirMode})
	if err != nil {
		return fmt.Errorf("writing the archive: %w", err)
	}

	return nil
}

func writeFile(archive *tar.Writer, name, source string, mode fs.FileMode) error {
	file, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("opening %s: %w", source, err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("reading %s: %w", source, err)
	}

	err = archive.WriteHeader(&tar.Header{
		Typeflag: tar.TypeReg, Name: name, Mode: int64(mode), Size: info.Size(),
	})
	if err != nil {
		return fmt.Errorf("writing the archive: %w", err)
	}

	written, err := io.Copy(archive, file)
	if err == nil && written != info.Size() {
		err = fmt.Errorf("%w: %s", errFileChanged, source)
	}

	if err != nil {
		return fmt.Errorf("writing %s to the archive: %w", name, err)
	}

	return nil
}

// layout is where the archive of a product keeps what it must carry.
type layout struct {
	// executables are the names the executable may have; one must be there.
	executables []string
	// headers is module.h of a tarantool, empty for another product.
	headers string
	// stubs is the directory of the EmmyLua stubs of a tarantool.
	stubs string
}

// layoutOf returns the layout of the archive of program.
func layoutOf(program binary.Program) layout {
	switch program {
	case binary.ProgramCe:
		return layout{executables: []string{"tarantool"}, headers: moduleH, stubs: stubsDir}
	case binary.ProgramEe, binary.ProgramSDK:
		return layout{
			executables: []string{sdkRoot + "/tarantool"},
			headers:     sdkRoot + "/" + moduleH, stubs: sdkRoot + "/" + stubsDir,
		}
	case binary.ProgramTcm:
		return layout{executables: []string{"tcm", legacyTcm}, headers: "", stubs: ""}
	default:
		return layout{executables: []string{program.Exec()}, headers: "", stubs: ""}
	}
}

// checkArchive checks that the archive at path of program carries its
// executable and, of a tarantool, the headers, and returns a warning for the
// stubs a tarantool of ver should have and has not.
func checkArchive(archivePath string, program binary.Program, ver string) ([]string, error) {
	names, err := archiveNames(archivePath)
	if err != nil {
		return nil, err
	}

	want := layoutOf(program)

	if !slices.ContainsFunc(want.executables, func(name string) bool { return names[name] }) {
		return nil, fmt.Errorf("%w: no executable %s", ErrContents, want.executables[0])
	}

	if want.headers == "" {
		return nil, nil
	}

	if !names[want.headers] {
		return nil, fmt.Errorf("%w: no %s, modules cannot be built against it",
			ErrContents, want.headers)
	}

	if hasStubs(ver) && !slices.ContainsFunc(keys(names), func(name string) bool {
		return strings.HasPrefix(name, want.stubs+"/")
	}) {
		return []string{fmt.Sprintf("%s %s has no EmmyLua stubs in %s: tt emmylua finds "+
			"none for it where it is installed from the mirror", program, ver, want.stubs)}, nil
	}

	return nil, nil
}

// hasStubs reports whether Tarantool of the version ships EmmyLua stubs.
func hasStubs(ver string) bool {
	parsed, err := version.Parse(ver)
	if err != nil {
		return false
	}

	return parsed.Major > stubsSinceMajor ||
		parsed.Major == stubsSinceMajor && parsed.Minor >= stubsSinceMinor
}

func keys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}

	return out
}

// archiveNames lists the regular files and directories of the gzip-compressed
// tar at path, by their cleaned names.
func archiveNames(archivePath string) (map[string]bool, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", archivePath, err)
	}

	defer func() { _ = file.Close() }()

	compressed, err := gzip.NewReader(file)
	if err != nil {
		return nil, fmt.Errorf("%w: not a gzip-compressed tar: %w", ErrContents, err)
	}

	defer func() { _ = compressed.Close() }()

	reader := tar.NewReader(compressed)
	names := map[string]bool{}

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return names, nil
		}

		if err != nil {
			return nil, fmt.Errorf("%w: reading the tar: %w", ErrContents, err)
		}

		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeDir {
			names[path.Clean(strings.TrimPrefix(header.Name, "./"))] = true
		}
	}
}
