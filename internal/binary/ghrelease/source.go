// Package ghrelease is the source of the prebuilt tt: the archives of the
// releases of tt on GitHub.
//
// The archives are downloaded directly, without the GitHub API and its rate
// limits: tt-<version>-<os>-<arch>.tar.gz of a release, checked against the
// checksums.txt of the same release. There is no listing, so the source
// answers a query for a release tag only, with the archive the release
// would have; Fetch reports the one a release does not have with
// bundle.ErrNotFound.
package ghrelease

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary/download"
)

const (
	// ReleasesURL is where the files of the releases of tt are downloaded from.
	ReleasesURL = "https://github.com/tarantool/tt/releases/download/"
	// LatestURL redirects to the latest release of tt.
	LatestURL = "https://github.com/tarantool/tt/releases/latest"

	checksumsName = "checksums.txt"

	// product is the only product of the source.
	product = "tt"

	goosDarwin  = "darwin"
	goosLinux   = "linux"
	goarchAMD64 = "amd64"
	goarchARM64 = "arm64"
)

var (
	versionPattern = regexp.MustCompile(`^v?[0-9]+\.[0-9]+\.[0-9]+(?:[-+][A-Za-z0-9.-]+)?$`)

	errUnknownProduct      = errors.New("unknown product")
	errUnsupportedPlatform = errors.New("unsupported platform")
	errPackageContents     = errors.New("invalid package contents")
)

// Source is the releases of tt on GitHub.
type Source struct {
	client      download.Client
	releasesURL string
	latestURL   string
}

var _ bundle.Source = (*Source)(nil)

// New returns the releases of tt on GitHub.
func New() *Source {
	return &Source{client: download.New(), releasesURL: ReleasesURL, latestURL: LatestURL}
}

// Name implements bundle.Source.
func (*Source) Name() string {
	return "github"
}

// Products implements bundle.Source.
func (*Source) Products() []string {
	return []string{product}
}

// List implements bundle.Source. The version of the query is the tag of a
// release, v2.15.0; a query without one has no builds, as has one for a
// platform the release does not have. The artifact has the SHA-256
// checksums.txt lists; a release without checksums.txt, released before
// 1.1.0, has an artifact without it, and one whose checksums.txt does not
// list the archive has none.
func (s *Source) List(ctx context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	if query.Product != product {
		return nil, fmt.Errorf("%w %q", errUnknownProduct, query.Product)
	}

	tag := query.Version

	switch {
	case query.Channel == bundle.ChannelDev:
		log.Debugf("%s has no development builds of tt", s.Name())

		return nil, nil
	case !versionPattern.MatchString(tag):
		log.Debugf("%s has no release of tt %q", s.Name(), tag)

		return nil, nil
	}

	err := checkPlatform(tag, query.OS, query.Arch)
	if err != nil {
		log.Debugf("%s: %v", s.Name(), err)

		return nil, nil
	}

	name := fmt.Sprintf("tt-%s-%s-%s.tar.gz", strings.TrimPrefix(tag, "v"), query.OS, query.Arch)
	release := strings.TrimRight(s.releasesURL, "/") + "/" + url.PathEscape(tag) + "/"

	sum, listed, err := s.checksum(ctx, release+checksumsName, name)
	if err != nil {
		return nil, err
	}

	if !listed {
		log.Debugf("%s of tt %s lists no %s", checksumsName, tag, name)

		return nil, nil
	}

	return []bundle.Artifact{{
		Version: strings.TrimPrefix(tag, "v"), Flavor: nil, Name: name,
		Size: 0, SHA256: sum, Locator: release + name,
	}}, nil
}

// Fetch implements bundle.Source. An archive the release does not have
// wraps bundle.ErrNotFound. An archive without a digest is reported as
// installed unverified.
func (s *Source) Fetch(ctx context.Context, artifact bundle.Artifact, dst io.Writer) error {
	err := s.client.Copy(ctx, artifact.Locator, product, dst)

	var status *download.HTTPError

	if errors.As(err, &status) && status.Status == http.StatusNotFound {
		return fmt.Errorf("%w: no tt archive %s; use --source-build to build from source",
			bundle.ErrNotFound, artifact.Locator)
	}

	if err != nil {
		return err
	}

	if artifact.SHA256 == "" {
		tag, _ := url.PathUnescape(path.Base(path.Dir(artifact.Locator)))
		log.Warnf("tt %s publishes no %s; the archive is not verified", tag, checksumsName)
	}

	return nil
}

// Latest returns the tag of the latest published release of tt. A pushed
// tag may still have no release, so the newest tag is not a reliable choice
// for prebuilt delivery. The tag is read from the redirect of the page of
// the latest release, without the GitHub API; GitHub skips drafts and
// pre-releases there.
func (s *Source) Latest(ctx context.Context) (string, error) {
	httpClient := *s.client.HTTP

	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	client := s.client

	client.HTTP = &httpClient

	response, err := client.Get(ctx, s.latestURL)
	if err != nil {
		return "", fmt.Errorf("get latest tt release: %w", err)
	}

	_ = response.Body.Close()

	location, err := response.Location()
	if err != nil {
		return "", fmt.Errorf("%w: latest tt release %s: HTTP %d without a redirect",
			download.ErrHTTPStatus, s.latestURL, response.StatusCode)
	}

	directory, tag := path.Split(location.Path)
	if !strings.HasSuffix(directory, "/releases/tag/") || !versionPattern.MatchString(tag) {
		return "", fmt.Errorf("%w: latest tt release %s redirects to %s",
			errPackageContents, s.latestURL, location)
	}

	return tag, nil
}

// checksum returns the SHA-256 of the file name checksums.txt at address
// lists, and whether it lists one. A release without checksums.txt has
// every file, unverified: the empty digest, listed.
func (s *Source) checksum(ctx context.Context, address, name string) (string, bool, error) {
	response, err := s.client.Get(ctx, address)
	if err != nil {
		return "", false, fmt.Errorf("download tt checksums %s: %w", address, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode == http.StatusNotFound {
		return "", true, nil
	}

	if response.StatusCode != http.StatusOK {
		return "", false, &download.HTTPError{
			Status: response.StatusCode, Address: address, Program: product,
		}
	}

	sum, listed, err := findChecksum(io.LimitReader(response.Body, download.IndexLimit), name)
	if err != nil {
		return "", false, fmt.Errorf("tt checksums %s: %w", address, err)
	}

	return sum, listed, nil
}

// findChecksum returns the sha256 of the file from the "<sha256>  <name>" lines of goreleaser.
func findChecksum(checksums io.Reader, name string) (string, bool, error) {
	scanner := bufio.NewScanner(checksums)

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || strings.TrimPrefix(fields[1], "*") != name {
			continue
		}

		sum, err := hex.DecodeString(fields[0])
		if err != nil || len(sum) != sha256.Size {
			return "", false, fmt.Errorf("%w: malformed checksum %q for %s",
				errPackageContents, fields[0], name)
		}

		return hex.EncodeToString(sum), true, nil
	}

	err := scanner.Err()
	if err != nil {
		return "", false, fmt.Errorf("read checksums: %w", err)
	}

	return "", false, nil
}

// checkPlatform recognizes the platforms published in GitHub Releases.
// tt 2 publishes macOS archives for amd64, tt 3 for arm64.
func checkPlatform(version, goos, goarch string) error {
	darwinArch := goarchARM64

	majorVersion, _, _ := strings.Cut(strings.TrimPrefix(version, "v"), ".")

	major, err := strconv.Atoi(majorVersion)
	if err == nil && major < 3 {
		darwinArch = goarchAMD64
	}

	supported := goos == goosLinux && (goarch == goarchAMD64 || goarch == goarchARM64) ||
		goos == goosDarwin && goarch == darwinArch
	if !supported {
		return fmt.Errorf("%w: no prebuilt tt %s for %s/%s",
			errUnsupportedPlatform, version, goos, goarch)
	}

	return nil
}
