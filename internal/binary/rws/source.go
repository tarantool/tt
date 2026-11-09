// Package rws is the source of the prebuilt Tarantool Community: the static
// RPMs of Tarantool 3 in the repository of rws.tarantool.org.
//
// The repository has no API: the RPMs of an architecture are the links of
// the HTML listing of its Packages/ directory. A build is the runtime RPM
// of a version, the newest revision of it, and the devel RPM of the same
// revision with the headers. The RPMs are statically linked against all but
// glibc, so the source lists builds only on a Linux host with the glibc
// loader, libc and libgcc. Fetch downloads both RPMs, unpacks them and
// writes the archive of tarantool sdk/bundle defines.
package rws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/net/html"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary/download"
)

const (
	// RepositoryURL is the repository of the static RPMs of Tarantool 3.
	RepositoryURL = "https://rws.tarantool.org/release/series-3/linux-rpm/static/"

	// product is the only product of the source.
	product = "tarantool"
	// series is the major version the repository has the builds of.
	series = "3"

	goosLinux   = "linux"
	goarchAMD64 = "amd64"
	goarchARM64 = "arm64"

	rpmArchAMD64 = "x86_64"
	rpmArchARM64 = "aarch64"
)

var (
	packageVersionPattern = regexp.MustCompile(`^3\.[0-9]+\.[0-9]+(?:[-.][A-Za-z0-9.]+)?$`)
	packageReleasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~^]*$`)

	// rpmArches are the architectures of the repository, by GOARCH.
	rpmArches = map[string]string{goarchAMD64: rpmArchAMD64, goarchARM64: rpmArchARM64}

	errUnknownProduct  = errors.New("unknown product")
	errInvalidLocator  = errors.New("invalid locator")
	errPackageContents = errors.New("invalid package contents")
)

// Source is the repository of the static RPMs of Tarantool.
type Source struct {
	client        download.Client
	repositoryURL string
	indexLimit    int64
	// systemRoot is where the libraries of the host are looked for: "/".
	systemRoot string
}

var _ bundle.Source = (*Source)(nil)

// New returns the repository of rws.tarantool.org.
func New() *Source {
	return &Source{
		client:        download.New(),
		repositoryURL: RepositoryURL,
		indexLimit:    download.IndexLimit,
		systemRoot:    "/",
	}
}

// Name implements bundle.Source.
func (*Source) Name() string {
	return "rws"
}

// Products implements bundle.Source.
func (*Source) Products() []string {
	return []string{product}
}

// locator is the pair of RPMs of a build.
type locator struct {
	Runtime string `json:"runtime"`
	Headers string `json:"headers"`
}

// List implements bundle.Source. It lists the builds of Tarantool 3 on a
// Linux host with glibc; the repository has no development builds. The
// version of a query narrows the listing to a release line.
func (s *Source) List(ctx context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	if query.Product != product {
		return nil, fmt.Errorf("%w %q", errUnknownProduct, query.Product)
	}

	packageArch := rpmArches[query.Arch]

	switch {
	case query.Channel == bundle.ChannelDev:
		log.Debugf("%s has no development builds of Tarantool", s.Name())

		return nil, nil
	case query.OS != goosLinux || packageArch == "":
		log.Debugf("%s has no Tarantool for %s/%s", s.Name(), query.OS, query.Arch)

		return nil, nil
	case query.Version != "" && query.Version != series &&
		!strings.HasPrefix(query.Version, series+"."):
		log.Debugf("%s has no Tarantool %s, only Tarantool %s", s.Name(), query.Version, series)

		return nil, nil
	}

	err := checkPlatform(query.Arch, s.systemRoot)
	if err != nil {
		log.Debugf("%s: %v", s.Name(), err)

		return nil, nil
	}

	packagesURL := fmt.Sprintf("%s/%s/Packages/",
		strings.TrimRight(s.repositoryURL, "/"), packageArch)

	files, err := s.index(ctx, packagesURL)
	if err != nil {
		return nil, err
	}

	return selectPackages(files, packagesURL, query.Version, packageArch), nil
}

// selectPackages returns the builds of the release line among files: for
// each version the newest revision of its runtime RPM, if the devel RPM of
// that revision is there too.
func selectPackages(files map[string]struct{}, packagesURL, line, packageArch string,
) []bundle.Artifact {
	prefix, suffix := product+"-", "."+packageArch+".rpm"
	newest := make(map[string]string)

	for name := range files {
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}

		// The release of an RPM has no dash: the version is up to the last one.
		version, release, found := strings.CutLast(strings.TrimSuffix(
			strings.TrimPrefix(name, prefix), suffix), "-")
		if !found || !packageVersionPattern.MatchString(version) ||
			!packageReleasePattern.MatchString(release) || !onLine(line, version) {
			continue
		}

		selected, seen := newest[version]

		comparison := comparePackageReleases(release, selected)
		if !seen || comparison > 0 || comparison == 0 && release > selected {
			newest[version] = release
		}
	}

	artifacts := make([]bundle.Artifact, 0, len(newest))

	for version, release := range newest {
		runtimeName := prefix + version + "-" + release + suffix
		// Headers must match the newest runtime revision exactly. Do not silently
		// select an older runtime just because its devel package is still present.
		headersName := product + "-devel-" + version + "-" + release + suffix

		if _, found := files[headersName]; !found {
			log.Debugf("no matching headers package %s for %s", headersName, runtimeName)

			continue
		}

		reference, err := json.Marshal(locator{
			Runtime: packagesURL + runtimeName, Headers: packagesURL + headersName,
		})
		if err != nil {
			continue // Two strings always marshal.
		}

		artifacts = append(artifacts, bundle.Artifact{
			Version: version, Flavor: nil,
			Name: strings.TrimSuffix(runtimeName, ".rpm") + ".tar.gz",
			Size: 0, SHA256: "", Locator: string(reference),
		})
	}

	slices.SortFunc(artifacts, func(left, right bundle.Artifact) int {
		return strings.Compare(left.Version, right.Version)
	})

	return artifacts
}

// onLine reports whether version belongs to the release line: "3", "3.7"
// or a whole version. An empty line has every version.
func onLine(line, version string) bool {
	return line == "" || version == line || strings.HasPrefix(version, line+".")
}

func parsePackageIndex(reader io.Reader) (map[string]struct{}, error) {
	files := make(map[string]struct{})
	tokenizer := html.NewTokenizer(reader)

	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			err := tokenizer.Err()
			if !errors.Is(err, io.EOF) {
				return nil, fmt.Errorf("parse RPM repository HTML: %w", err)
			}

			return files, nil
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokenizer.Token()
			if token.Data != "a" {
				continue
			}

			for _, attribute := range token.Attr {
				if attribute.Key != "href" {
					continue
				}

				link, err := url.Parse(attribute.Val)
				if err != nil || link.RawQuery != "" || link.Fragment != "" {
					continue
				}

				name := path.Base(link.Path)
				if strings.HasSuffix(name, ".rpm") {
					files[name] = struct{}{}
				}
			}
		default:
		}
	}
}

// Fetch implements bundle.Source. It downloads the runtime and the devel
// RPM of the build into a temporary directory, unpacks them and writes the
// executable and the headers to dst as a tar.gz. An RPM the listing named
// and the repository does not serve is a failed delivery.
func (s *Source) Fetch(ctx context.Context, artifact bundle.Artifact, dst io.Writer) error {
	var packages locator

	err := json.Unmarshal([]byte(artifact.Locator), &packages)
	if err != nil || packages.Runtime == "" || packages.Headers == "" {
		return fmt.Errorf("%w %q of %s", errInvalidLocator, artifact.Locator, s.Name())
	}

	extractDir, err := os.MkdirTemp("", "tt-rws-")
	if err != nil {
		return fmt.Errorf("create package extraction directory: %w", err)
	}

	defer func() { _ = os.RemoveAll(extractDir) }()

	for _, item := range []struct{ address, directory string }{
		{packages.Runtime, "runtime"}, {packages.Headers, "headers"},
	} {
		directory := filepath.Join(extractDir, item.directory)

		err = os.Mkdir(directory, dirMode)
		if err != nil {
			return fmt.Errorf("create RPM extraction directory: %w", err)
		}

		err = s.downloadAndExtractRPM(ctx, item.address, directory)
		if err != nil {
			return err
		}
	}

	executable := filepath.Join(extractDir, "runtime", "usr", "bin", "tarantool")
	headers := filepath.Join(extractDir, "headers", "usr", "include", "tarantool")

	info, err := os.Lstat(executable)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
		return fmt.Errorf("%w: prebuilt Tarantool package has no executable "+
			"usr/bin/tarantool; use --source-build to build from source", errPackageContents)
	}

	info, err = os.Lstat(filepath.Join(headers, "module.h"))
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("%w: prebuilt Tarantool devel package has no "+
			"usr/include/tarantool/module.h; use --source-build to build from source",
			errPackageContents)
	}

	return writeArchive(dst, executable, headers)
}

func (s *Source) downloadAndExtractRPM(ctx context.Context, address, destination string,
) error {
	archive, err := os.CreateTemp(destination, "package-*.rpm")
	if err != nil {
		return fmt.Errorf("create temporary package: %w", err)
	}

	defer func() { _ = os.Remove(archive.Name()) }()

	copyErr := s.client.Copy(ctx, address, "Tarantool", archive)
	closeErr := archive.Close()

	if copyErr != nil {
		return copyErr
	}

	if closeErr != nil {
		return fmt.Errorf("close downloaded package: %w", closeErr)
	}

	err = extractRPM(archive.Name(), destination)
	if err != nil {
		return fmt.Errorf("extract Tarantool package %s: %w", address, err)
	}

	return nil
}

// index returns the names of the RPMs of the listing at packagesURL.
func (s *Source) index(ctx context.Context, packagesURL string) (map[string]struct{}, error) {
	response, err := s.client.Get(ctx, packagesURL)
	if err != nil {
		return nil, fmt.Errorf("fetch Tarantool package index %s: %w", packagesURL, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: fetch Tarantool package index %s: HTTP %s; "+
			"use --source-build to build from source",
			download.ErrHTTPStatus, packagesURL, response.Status)
	}

	limited := &io.LimitedReader{R: response.Body, N: s.indexLimit + 1}

	files, err := parsePackageIndex(limited)
	if err != nil {
		return nil, fmt.Errorf("read Tarantool package index: %w", err)
	}

	if limited.N == 0 {
		return nil, fmt.Errorf("%w: Tarantool package index exceeds %d bytes",
			download.ErrTooLarge, s.indexLimit)
	}

	return files, nil
}
