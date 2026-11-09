package install

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
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
	ttReleasesURL      = "https://github.com/tarantool/tt/releases/download/"
	ttLatestReleaseURL = "https://github.com/tarantool/tt/releases/latest"
	ttChecksumsName    = "checksums.txt"
)

var (
	ttPackageVersionPattern = regexp.MustCompile(
		`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

	errPackageChecksum = errors.New("package checksum mismatch")
)

// ttPackageDownloader downloads release archives directly, without the GitHub API.
type ttPackageDownloader struct {
	packageDownloader

	releasesURL string
	latestURL   string
}

// latestPrebuiltTt returns the tag of the latest published tt release. A pushed tag
// may still have no release, so the newest tag is not a reliable choice for prebuilt delivery.
func latestPrebuiltTt() (string, error) {
	return newTtPackageDownloader(false).latest()
}

func newTtPackageDownloader(keepTemp bool) ttPackageDownloader {
	return ttPackageDownloader{
		client:    &http.Client{Timeout: packageDownloadTimeout},
		fileLimit: maxPackageSize, keepTemp: keepTemp,
		releasesURL: ttReleasesURL,
		latestURL:   ttLatestReleaseURL,
	}
}

// latest reads the tag from the redirect of the latest release page, without the GitHub API.
// GitHub skips drafts and pre-releases there.
func (d ttPackageDownloader) latest() (string, error) {
	client := *d.client

	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	downloader := d.packageDownloader

	downloader.client = &client

	response, err := downloader.get(d.latestURL)
	if err != nil {
		return "", fmt.Errorf("get latest tt release: %w", err)
	}

	_ = response.Body.Close()

	location, err := response.Location()
	if err != nil {
		return "", fmt.Errorf("%w: latest tt release %s: HTTP %d without a redirect",
			errPackageHTTPStatus, d.latestURL, response.StatusCode)
	}

	directory, tag := path.Split(location.Path)
	if !strings.HasSuffix(directory, "/releases/tag/") ||
		!ttPackageVersionPattern.MatchString(tag) {
		return "", fmt.Errorf("%w: latest tt release %s redirects to %s",
			errPackageContents, d.latestURL, location)
	}

	return tag, nil
}

func preparePrebuiltTt(version, destination string, keepTemp bool) (programArtifact, error) {
	return newTtPackageDownloader(keepTemp).prepare(version, destination,
		runtime.GOOS, runtime.GOARCH)
}

func (d ttPackageDownloader) prepare(version, destination, goos, goarch string) (
	programArtifact, error,
) {
	if !ttPackageVersionPattern.MatchString(version) {
		return programArtifact{}, fmt.Errorf("%w: no prebuilt tt package for %q on %s/%s; "+
			"use --source-build to build from source",
			errPrebuiltPackageUnavailable, version, goos, goarch)
	}

	err := checkTtPackagePlatform(version, goos, goarch)
	if err != nil {
		return programArtifact{}, err
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

	err = d.verifyChecksum(version, name, archive)
	if err != nil {
		return programArtifact{}, err
	}

	prepared, err := extractPrebuilt(binary.ProgramTt, archive, extractDir)
	if err != nil {
		return programArtifact{}, fmt.Errorf("extract tt package %s: %w; "+
			"use --source-build to build from source", address, err)
	}

	succeeded = true

	return prepared, nil
}

// verifyChecksum checks the archive against checksums.txt of the same release.
// Releases before 1.1.0 publish no checksums, so their archives are installed unverified.
// A mismatch is a delivery error and must not trigger a source build.
func (d ttPackageDownloader) verifyChecksum(version, name, archivePath string) error {
	address := strings.TrimRight(d.releasesURL, "/") + "/" +
		url.PathEscape(version) + "/" + ttChecksumsName

	response, err := d.get(address)
	if err != nil {
		return fmt.Errorf("download tt checksums %s: %w", address, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusNotFound {
		log.Warnf("tt %s publishes no %s; the archive is not verified", version, ttChecksumsName)

		return nil
	}

	if response.StatusCode != http.StatusOK {
		return &packageHTTPError{status: response.StatusCode, address: address, program: "tt"}
	}

	expected, err := findChecksum(io.LimitReader(response.Body, maxPackageIndexSize), name)
	if err != nil {
		return fmt.Errorf("tt checksums %s: %w", address, err)
	}

	archive, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open tt archive: %w", err)
	}

	defer func() { _ = archive.Close() }()

	hash := sha256.New()

	_, err = io.Copy(hash, archive)
	if err != nil {
		return fmt.Errorf("read tt archive: %w", err)
	}

	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("%w: %s has sha256 %s, %s expects %s",
			errPackageChecksum, name, actual, address, expected)
	}

	return nil
}

// findChecksum returns the sha256 of the file from the "<sha256>  <name>" lines of goreleaser.
func findChecksum(checksums io.Reader, name string) (string, error) {
	scanner := bufio.NewScanner(checksums)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}

		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size {
			return "", fmt.Errorf("%w: malformed checksum %q for %s",
				errPackageContents, fields[0], name)
		}

		return hex.EncodeToString(sum), nil
	}

	err := scanner.Err()
	if err != nil {
		return "", fmt.Errorf("read checksums: %w", err)
	}

	return "", fmt.Errorf("%w: no checksum for %s", errPackageContents, name)
}
