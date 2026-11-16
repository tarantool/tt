package install

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	maxPrebuiltPayloadSize    = 1 << 30
	maxPrebuiltArchiveEntries = 100_000
	headerFilePermissions     = 0o644
)

var (
	// errPrebuiltPackageUnavailable reports no prebuilt build of a release:
	// tt builds it from source instead.
	errPrebuiltPackageUnavailable = errors.New("prebuilt package unavailable")
	errPackageTooLarge            = errors.New("package response exceeds size limit")
	errPackageContents            = errors.New("invalid package contents")
	errNoLatestRelease            = errors.New("no latest release")
)

// prebuiltDownloadMessages are what tt says when it downloads the archive
// of a Community product.
var prebuiltDownloadMessages = map[binary.Program]string{
	binary.ProgramCe: "Downloading Tarantool binary and headers...",
	binary.ProgramTt: "Downloading tt binary...",
}

// latestReleaseSource is a source that knows the latest release of its
// product, as the releases of tt on GitHub do.
type latestReleaseSource interface {
	Latest(ctx context.Context) (string, error)
}

// platformSource is a source that knows the platforms its releases publish
// archives for, as the releases of tt on GitHub do. A host it publishes none
// for is an error that names the platforms there are, not an archive that
// is not found.
type platformSource interface {
	CheckPlatform(tag, goos, goarch string) error
}

// latestPrebuiltTt returns the tag of the latest release of tt the source
// of tt has. A pushed tag may still have no release, so the newest tag is
// not a reliable choice for prebuilt delivery.
func latestPrebuiltTt(ctx context.Context) (string, error) {
	source, err := binary.BundleSource(binary.ProgramTt)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errNoLatestRelease, err)
	}

	latest, ok := source.(latestReleaseSource)
	if !ok {
		return "", fmt.Errorf("%w of tt in %s", errNoLatestRelease, source.Name())
	}

	tag, err := latest.Latest(ctx)
	if err != nil {
		return "", fmt.Errorf("%w of tt in %s: %w", errNoLatestRelease, source.Name(), err)
	}

	return tag, nil
}

// preparePrebuilt downloads the build of the release tag of program from the
// source of program and unpacks it into a directory of destination. A
// build the source does not have is errPrebuiltPackageUnavailable: no
// source of program, no build of the tag for the host, or a Fetch that
// reports bundle.ErrNotFound. A host the source knows it publishes no build
// for (platformSource) and any other failure are errors.
func preparePrebuilt(ctx context.Context, program binary.Program, tag, destination string,
	keepTemp bool,
) (programArtifact, error) {
	source, err := binary.BundleSource(program)
	if err != nil {
		return programArtifact{}, fmt.Errorf("%w: no source of prebuilt %s in this build of tt; "+
			"use --source-build to build from source", errPrebuiltPackageUnavailable, program)
	}

	if checker, ok := source.(platformSource); ok {
		err = checker.CheckPlatform(tag, runtime.GOOS, runtime.GOARCH)
		if err != nil {
			return programArtifact{}, fmt.Errorf("%w; use --source-build to build %s %s "+
				"from source", err, program, tag)
		}
	}

	artifact, err := findPrebuilt(ctx, source, program, tag)
	if err != nil {
		return programArtifact{}, err
	}

	log.Infof("Installing prebuilt %s=%s", program, tag)
	log.Infof("%s", prebuiltDownloadMessages[program])

	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return programArtifact{}, fmt.Errorf("resolve package destination: %w", err)
	}

	err = os.MkdirAll(absoluteDestination, defaultDirPermissions)
	if err != nil {
		return programArtifact{}, fmt.Errorf("create package destination: %w", err)
	}

	extractDir, err := os.MkdirTemp(absoluteDestination, program.String()+"-package-")
	if err != nil {
		return programArtifact{}, fmt.Errorf("create package extraction directory: %w", err)
	}

	succeeded := false

	defer func() {
		if !succeeded && !keepTemp {
			_ = os.RemoveAll(extractDir)
		}
	}()

	archive, err := os.CreateTemp(extractDir, "package-*.tar.gz")
	if err != nil {
		return programArtifact{}, fmt.Errorf("create temporary package: %w", err)
	}

	_ = archive.Close()

	err = fetchArtifact(ctx, source, artifact, archive.Name())
	if errors.Is(err, bundle.ErrNotFound) {
		// Keep the source checkout directory empty, including with --no-clean.
		_ = os.RemoveAll(extractDir)

		return programArtifact{}, fmt.Errorf("%w: %w; use --source-build to build from source",
			errPrebuiltPackageUnavailable, err)
	}

	if err != nil {
		return programArtifact{}, err
	}

	if !keepTemp {
		defer func() { _ = os.Remove(archive.Name()) }()
	}

	prepared, err := extractPrebuilt(program, archive.Name(), extractDir)
	if err != nil {
		return programArtifact{}, fmt.Errorf("extract %s package %s: %w; "+
			"use --source-build to build from source", program, artifact.Name, err)
	}

	succeeded = true

	return prepared, nil
}

// findPrebuilt returns the build of the release tag of program the source
// lists for the host: its version is the tag, with or without the leading
// v, and it has no flavours.
func findPrebuilt(ctx context.Context, source bundle.Source, program binary.Program,
	tag string,
) (bundle.Artifact, error) {
	query := binary.NewBundleQuery(program, false, tag)

	artifacts, err := source.List(ctx, query)
	if err != nil {
		return bundle.Artifact{}, fmt.Errorf("list %s archives in %s: %w",
			program, source.Name(), err)
	}

	for _, artifact := range artifacts {
		if len(artifact.Flavor) == 0 &&
			strings.TrimPrefix(artifact.Version, "v") == strings.TrimPrefix(tag, "v") {
			return artifact, nil
		}
	}

	return bundle.Artifact{}, fmt.Errorf("%w: %s has no prebuilt %s %s for %s/%s; "+
		"use --source-build to build from source", errPrebuiltPackageUnavailable,
		source.Name(), program, tag, query.OS, query.Arch)
}

// extractPrebuilt unpacks the archive of a Community product sdk/bundle
// defines into destination: the executable and the license files at the
// root and, of tarantool, the headers in include/tarantool and the Lua
// files in share/tarantool. Other regular files and directories are
// skipped; any other entry, or one outside the archive, is an error.
// Bounded reads include the gzip footer, detecting truncation and checksum
// failures.
func extractPrebuilt(program binary.Program, archivePath, destination string) (
	programArtifact, error,
) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return programArtifact{}, fmt.Errorf("open %s archive: %w", program, err)
	}

	defer func() { _ = archive.Close() }()

	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return programArtifact{}, fmt.Errorf("%w: open %s gzip archive: %w",
			errPackageContents, program, err)
	}

	defer func() { _ = compressed.Close() }()

	root, err := os.OpenRoot(destination)
	if err != nil {
		return programArtifact{}, fmt.Errorf("open %s extraction directory: %w", program, err)
	}

	defer func() { _ = root.Close() }()

	extractor := prebuiltExtractor{
		program: program, root: root, headers: "", share: "", executable: false,
		hasShare: false, licenses: nil,
	}
	if program == binary.ProgramCe {
		extractor.headers = path.Join(binary.IncDirName, program.Exec())
		extractor.share = shareDir
	}

	err = extractor.extract(compressed)
	if err != nil {
		return programArtifact{}, err
	}

	prepared := programArtifact{
		program: program, executable: filepath.Join(destination, program.Exec()), headers: "",
		share: "", licenses: nil,
	}

	if extractor.hasShare {
		prepared.share = filepath.Join(destination, filepath.FromSlash(extractor.share))
	}

	for _, license := range extractor.licenses {
		prepared.licenses = append(prepared.licenses, filepath.Join(destination, license))
	}

	if extractor.headers != "" {
		info, err := root.Lstat(path.Join(extractor.headers, "module.h"))
		if err != nil || !info.Mode().IsRegular() {
			return programArtifact{}, fmt.Errorf("%w: prebuilt %s package has no %s/module.h",
				errPackageContents, program, extractor.headers)
		}

		prepared.headers = filepath.Join(destination, filepath.FromSlash(extractor.headers))
	}

	return prepared, nil
}

// prebuiltExtractor unpacks the entries of an archive of program into root.
type prebuiltExtractor struct {
	program binary.Program
	root    *os.Root
	// headers is the directory of the headers in the archive, if program has them.
	headers string
	// share is the directory of the Lua files in the archive, if program
	// may have them; hasShare is set once one of them is extracted.
	share    string
	hasShare bool
	// executable is set once the executable is extracted.
	executable bool
	// licenses are the license files extracted.
	licenses []string
}

func (e *prebuiltExtractor) extract(compressed io.Reader) error {
	payload := &io.LimitedReader{R: compressed, N: maxPrebuiltPayloadSize + 1}
	reader := tar.NewReader(payload)

	for entries := 0; ; entries++ {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}

		if payload.N == 0 {
			return fmt.Errorf("%w: %s archive payload exceeds %d bytes",
				errPackageTooLarge, e.program, maxPrebuiltPayloadSize)
		}

		if readErr != nil {
			return fmt.Errorf("%w: read %s archive: %w", errPackageContents, e.program, readErr)
		}

		if entries >= maxPrebuiltArchiveEntries {
			return fmt.Errorf("%w: too many %s archive entries", errPackageContents, e.program)
		}

		if header.Size > maxPrebuiltPayloadSize {
			return fmt.Errorf("%w: %s archive entry exceeds %d bytes",
				errPackageTooLarge, e.program, maxPrebuiltPayloadSize)
		}

		err := e.extractEntry(header, reader)
		if err != nil {
			return err
		}
	}

	_, readErr := io.Copy(io.Discard, payload)
	if payload.N == 0 {
		return fmt.Errorf("%w: %s archive payload exceeds %d bytes",
			errPackageTooLarge, e.program, maxPrebuiltPayloadSize)
	}

	if readErr != nil {
		return fmt.Errorf("%w: read %s archive footer: %w", errPackageContents, e.program, readErr)
	}

	if !e.executable {
		return fmt.Errorf("%w: prebuilt %s package has no executable %s",
			errPackageContents, e.program, e.program.Exec())
	}

	return nil
}

func (e *prebuiltExtractor) extractEntry(header *tar.Header, content io.Reader) error {
	name := path.Clean(header.Name)
	if !filepath.IsLocal(name) ||
		header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
		return fmt.Errorf("%w: unsafe %s archive entry %q",
			errPackageContents, e.program, header.Name)
	}

	switch {
	case name == e.program.Exec():
		if e.executable || header.Typeflag != tar.TypeReg || header.Mode&0o100 == 0 {
			return fmt.Errorf("%w: %s must be a single regular executable",
				errPackageContents, e.program.Exec())
		}

		e.executable = true

		return e.extractFile(name, content, header.Size, defaultDirPermissions)
	case isUnder(name, e.headers):
		return e.extractTreeEntry(name, header, content)
	case isUnder(name, e.share):
		e.hasShare = true

		return e.extractTreeEntry(name, header, content)
	case header.Typeflag == tar.TypeReg && slices.Contains(licenseNames, name):
		e.licenses = append(e.licenses, name)

		return e.extractFile(name, content, header.Size, headerFilePermissions)
	default:
		return nil
	}
}

// isUnder reports whether the archive entry name is the directory tree or
// in it; nothing is under an empty tree.
func isUnder(name, tree string) bool {
	return tree != "" && (name == tree || strings.HasPrefix(name, tree+"/"))
}

// extractTreeEntry unpacks a directory or a file of a tree, its parents
// included.
func (e *prebuiltExtractor) extractTreeEntry(name string, header *tar.Header,
	content io.Reader,
) error {
	if header.Typeflag == tar.TypeDir {
		return e.mkdirAll(name)
	}

	err := e.mkdirAll(path.Dir(name))
	if err != nil {
		return err
	}

	return e.extractFile(name, content, header.Size, headerFilePermissions)
}

func (e *prebuiltExtractor) mkdirAll(name string) error {
	err := e.root.MkdirAll(filepath.FromSlash(name), defaultDirPermissions)
	if err != nil {
		return fmt.Errorf("%w: create %s: %w", errPackageContents, name, err)
	}

	return nil
}

func (e *prebuiltExtractor) extractFile(name string, content io.Reader, size int64,
	mode os.FileMode,
) error {
	output, err := e.root.OpenFile(filepath.FromSlash(name),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("%w: create %s: %w", errPackageContents, name, err)
	}

	_, copyErr := io.CopyN(output, content, size)
	closeErr := output.Close()

	if copyErr != nil {
		return fmt.Errorf("%w: extract %s: %w", errPackageContents, name, copyErr)
	}

	if closeErr != nil {
		return fmt.Errorf("close %s: %w", name, closeErr)
	}

	return nil
}
