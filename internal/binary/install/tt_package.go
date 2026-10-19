package install

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	ttReleasesURL       = "https://github.com/tarantool/tt/releases/download/"
	maxTtPayloadSize    = 1 << 30
	maxTtArchiveEntries = 100_000
)

var ttPackageVersionPattern = regexp.MustCompile(
	`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

// ttPackageDownloader downloads release archives directly, without the GitHub API.
type ttPackageDownloader struct {
	packageDownloader

	releasesURL string
}

func preparePrebuiltTt(version, destination string, keepTemp bool) (programArtifact, error) {
	downloader := ttPackageDownloader{
		client:    &http.Client{Timeout: packageDownloadTimeout},
		fileLimit: maxPackageSize, keepTemp: keepTemp,
		releasesURL: ttReleasesURL,
	}

	return downloader.prepare(version, destination, runtime.GOOS, runtime.GOARCH)
}

func (d ttPackageDownloader) prepare(version, destination, goos, goarch string) (
	programArtifact, error,
) {
	err := checkTtPackagePlatform(goos, goarch)
	if err != nil {
		return programArtifact{}, err
	}

	if !ttPackageVersionPattern.MatchString(version) {
		return programArtifact{}, fmt.Errorf("%w: no prebuilt tt package for %q on %s/%s; "+
			"use --source-build to build from source",
			errPrebuiltPackageUnavailable, version, goos, goarch)
	}

	log.Infof("Installing prebuilt tt=%s", version)

	name := fmt.Sprintf("tt-%s-%s-%s.tar.gz", strings.TrimPrefix(version, "v"), goos, goarch)
	address := strings.TrimRight(d.releasesURL, "/") + "/" +
		url.PathEscape(version) + "/" + name

	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return programArtifact{}, fmt.Errorf("resolve package destination: %w", err)
	}

	err = os.MkdirAll(absoluteDestination, defaultDirPermissions)
	if err != nil {
		return programArtifact{}, fmt.Errorf("create package destination: %w", err)
	}

	extractDir, err := os.MkdirTemp(absoluteDestination, "tt-package-")
	if err != nil {
		return programArtifact{}, fmt.Errorf("create package extraction directory: %w", err)
	}

	succeeded := false

	defer func() {
		if !succeeded && !d.keepTemp {
			_ = os.RemoveAll(extractDir)
		}
	}()

	log.Infof("Downloading tt binary...")

	archive, err := d.download(address, extractDir, "package-*.tar.gz", "tt")
	if err != nil {
		var status *packageHTTPError

		if errors.As(err, &status) && status.status == http.StatusNotFound {
			// Keep the source checkout directory empty, including with --no-clean.
			_ = os.RemoveAll(extractDir)

			return programArtifact{}, fmt.Errorf("%w: no tt archive %s; "+
				"use --source-build to build from source", errPrebuiltPackageUnavailable, address)
		}

		return programArtifact{}, err
	}

	if !d.keepTemp {
		defer func() { _ = os.Remove(archive) }()
	}

	executable, err := extractTtPackage(archive, extractDir)
	if err != nil {
		return programArtifact{}, fmt.Errorf("extract tt package %s: %w; "+
			"use --source-build to build from source", address, err)
	}

	succeeded = true

	return programArtifact{program: binary.ProgramTt, executable: executable, headers: ""}, nil
}

// extractTtPackage extracts only the root executable. Other regular release files are skipped.
// Bounded reads include the gzip footer, detecting truncation and checksum failures.
func extractTtPackage(archivePath, destination string) (string, error) {
	archive, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open tt archive: %w", err)
	}

	defer func() { _ = archive.Close() }()

	compressed, err := gzip.NewReader(archive)
	if err != nil {
		return "", fmt.Errorf("%w: open tt gzip archive: %w", errPackageContents, err)
	}

	defer func() { _ = compressed.Close() }()

	payload := &io.LimitedReader{R: compressed, N: maxTtPayloadSize + 1}
	reader := tar.NewReader(payload)
	executable := ""

	for entries := 0; ; entries++ {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}

		if payload.N == 0 {
			return "", fmt.Errorf("%w: tt archive payload exceeds %d bytes",
				errPackageTooLarge, maxTtPayloadSize)
		}

		if readErr != nil {
			return "", fmt.Errorf("%w: read tt archive: %w", errPackageContents, readErr)
		}

		if entries >= maxTtArchiveEntries {
			return "", fmt.Errorf("%w: too many tt archive entries", errPackageContents)
		}

		if header.Size > maxTtPayloadSize {
			return "", fmt.Errorf("%w: tt archive entry exceeds %d bytes",
				errPackageTooLarge, maxTtPayloadSize)
		}

		name := path.Clean(header.Name)
		if !filepath.IsLocal(name) ||
			header.Typeflag != tar.TypeReg && header.Typeflag != tar.TypeDir {
			return "", fmt.Errorf("%w: unsafe tt archive entry %q", errPackageContents, header.Name)
		}

		if name != "tt" {
			continue
		}

		if executable != "" || header.Typeflag != tar.TypeReg || header.Mode&0o100 == 0 {
			return "", fmt.Errorf("%w: tt must be a single regular executable", errPackageContents)
		}

		executable = filepath.Join(destination, "tt")

		output, openErr := os.OpenFile(executable, os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			defaultDirPermissions)
		if openErr != nil {
			return "", fmt.Errorf("create tt executable: %w", openErr)
		}

		_, copyErr := io.CopyN(output, reader, header.Size)
		closeErr := output.Close()

		if copyErr != nil {
			return "", fmt.Errorf("%w: extract tt executable: %w", errPackageContents, copyErr)
		}

		if closeErr != nil {
			return "", fmt.Errorf("close tt executable: %w", closeErr)
		}
	}

	_, readErr := io.Copy(io.Discard, payload)
	if payload.N == 0 {
		return "", fmt.Errorf("%w: tt archive payload exceeds %d bytes",
			errPackageTooLarge, maxTtPayloadSize)
	}

	if readErr != nil {
		return "", fmt.Errorf("%w: read tt archive footer: %w", errPackageContents, readErr)
	}

	if executable == "" {
		return "", fmt.Errorf("%w: prebuilt tt package has no executable tt", errPackageContents)
	}

	return executable, nil
}
