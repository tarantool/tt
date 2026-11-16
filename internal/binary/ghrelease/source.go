// Package ghrelease is the source of the prebuilt tt: the archives of the
// releases of tt on GitHub.
//
// The archives are downloaded directly, without the GitHub API and its rate
// limits: tt-<version>-<os>-<arch>.tar.gz of a release, checked against the
// checksums.txt of the same release. There is no listing, so the source
// answers a query for a release tag only, with the archive the release
// would have; Fetch reports the one a release does not have with
// bundle.ErrNotFound. Only Latest may ask the GitHub API, for the releases
// of a major version of tt (see Source.Latest).
package ghrelease

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary/download"
)

const (
	// ReleasesURL is where the files of the releases of tt are downloaded from.
	ReleasesURL = "https://github.com/tarantool/tt/releases/download/"
	// LatestURL redirects to the latest release of tt.
	LatestURL = "https://github.com/tarantool/tt/releases/latest"
	// APIReleasesURL lists the releases of tt, newest first, in the GitHub API.
	APIReleasesURL = "https://api.github.com/repos/tarantool/tt/releases"

	// releasesPerPage is the size of a page of the API listing, its maximum.
	releasesPerPage = 100
	// maxReleasePages bounds the pages of the listing Latest reads, as
	// install.sh does.
	maxReleasePages = 10
	// tokenVariable names the GitHub token the API is asked with, if set: it
	// lifts the rate limit of anonymous requests.
	tokenVariable = "GITHUB_TOKEN" //nolint:gosec // The name of a variable, not a credential.

	// firstDarwinARM64Major is the first major version of tt whose macOS
	// archives are arm64 ones; those of the earlier ones are amd64 ones.
	firstDarwinARM64Major = 3

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
	errNoRelease           = errors.New("no release")
)

// Source is the releases of tt on GitHub.
type Source struct {
	client      download.Client
	releasesURL string
	latestURL   string
	apiURL      string
	// major is the major version of tt Latest looks for.
	major uint64
}

var _ bundle.Source = (*Source)(nil)

// New returns the releases of tt on GitHub. Its latest release is that of
// the major version of this tt.
func New() *Source {
	return &Source{
		client: download.New(), releasesURL: ReleasesURL, latestURL: LatestURL,
		apiURL: APIReleasesURL, major: version.Major,
	}
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

// Latest returns the tag of the latest published release of tt of the major
// version of the source: the newest stable one, or, while that major
// version has none, the newest pre-release. Another major version is never
// the latest: tt 3 does not install tt 2 unless asked to. A pushed tag may
// still have no release, so the newest tag is not a reliable choice for
// prebuilt delivery.
//
// The page of the latest release is asked first, without the GitHub API and
// its rate limits: it names the newest stable release of any major version,
// and serves when that is of the one wanted. Otherwise the releases are
// listed with the GitHub API, newest first, and the first stable release of
// the major version is the latest, as install.sh chooses it; with
// GITHUB_TOKEN set, the API is asked with it.
func (s *Source) Latest(ctx context.Context) (string, error) {
	tag, err := s.latestRedirect(ctx)

	switch {
	case err != nil:
		log.Debugf("%s: %v; listing the releases of tt", s.Name(), err)
	case tagMajor(tag) == s.major:
		return tag, nil
	default:
		log.Debugf("%s: the latest release of tt, %s, is not of tt %d; "+
			"listing the releases of tt", s.Name(), tag, s.major)
	}

	return s.latestListed(ctx)
}

// latestRedirect returns the tag of the latest release of tt, of any major
// version, from the redirect of the page of the latest release. GitHub skips
// drafts and pre-releases there.
func (s *Source) latestRedirect(ctx context.Context) (string, error) {
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

// apiRelease is what the GitHub API tells of a release that Latest reads.
type apiRelease struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
}

// latestListed returns the tag of the latest release of tt of the major
// version of the source from the listing of the GitHub API: the first stable
// one, the listing being newest first, or the first pre-release when there
// is no stable one. Every page is read until a stable release shows up, so
// a stable release is not missed behind a page of the releases of another
// major version.
func (s *Source) latestListed(ctx context.Context) (string, error) {
	prerelease := ""

	for page := 1; page <= maxReleasePages; page++ {
		releases, err := s.releasesPage(ctx, page)
		if err != nil {
			return "", err
		}

		for _, release := range releases {
			if release.Draft || !versionPattern.MatchString(release.TagName) ||
				tagMajor(release.TagName) != s.major {
				continue
			}

			if !release.Prerelease {
				return release.TagName, nil
			}

			if prerelease == "" {
				prerelease = release.TagName
			}
		}

		if len(releases) < releasesPerPage {
			break
		}
	}

	if prerelease == "" {
		return "", fmt.Errorf("%w of tt %d in %s", errNoRelease, s.major, s.apiURL)
	}

	return prerelease, nil
}

// releasesPage returns a page, numbered from 1, of the releases of tt the
// GitHub API lists.
func (s *Source) releasesPage(ctx context.Context, page int) ([]apiRelease, error) {
	address := fmt.Sprintf("%s?per_page=%d&page=%d", s.apiURL, releasesPerPage, page)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("list tt releases: %w", err)
	}

	request.Header.Set("Accept", "application/vnd.github+json")

	token := os.Getenv(tokenVariable)
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	client := s.client.HTTP
	if client == nil {
		client = &http.Client{Timeout: download.Timeout}
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("list tt releases %s: %w", address, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		hint := ""
		if response.StatusCode == http.StatusForbidden ||
			response.StatusCode == http.StatusTooManyRequests {
			hint = "; the rate limit of the GitHub API may be exceeded: set " + tokenVariable
		}

		return nil, fmt.Errorf("%w: list tt releases %s: HTTP %d %s%s", download.ErrHTTPStatus,
			address, response.StatusCode, http.StatusText(response.StatusCode), hint)
	}

	var releases []apiRelease

	err = json.NewDecoder(io.LimitReader(response.Body, download.IndexLimit)).Decode(&releases)
	if err != nil {
		return nil, fmt.Errorf("%w: tt releases %s: %w", errPackageContents, address, err)
	}

	return releases, nil
}

// tagMajor returns the major version of a release tag, v3.0.0 or 3.0.0, or
// 0 for a tag that names none.
func tagMajor(tag string) uint64 {
	number, _, _ := strings.Cut(strings.TrimPrefix(tag, "v"), ".")

	major, err := strconv.ParseUint(number, 10, 64)
	if err != nil {
		return 0
	}

	return major
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
func checkPlatform(tag, goos, goarch string) error {
	darwinArch := goarchARM64

	if tagMajor(tag) < firstDarwinARM64Major {
		darwinArch = goarchAMD64
	}

	supported := goos == goosLinux && (goarch == goarchAMD64 || goarch == goarchARM64) ||
		goos == goosDarwin && goarch == darwinArch
	if !supported {
		return fmt.Errorf("%w: no prebuilt tt %s for %s/%s",
			errUnsupportedPlatform, tag, goos, goarch)
	}

	return nil
}
