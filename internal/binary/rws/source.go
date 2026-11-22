// Package rws is the source of the prebuilt Tarantool Community: the static
// deb packages of Tarantool 3 in the repository of rws.tarantool.org.
//
// The repository is a Debian one: the Packages index of an architecture in
// its static distribution lists each package with its version, its file and
// the SHA-256 of the file. A build is the runtime package, tarantool, of a
// version, the newest revision of it, and the tarantool-dev package of the
// same revision with the headers. The packages are statically linked against
// all but glibc, so the source lists builds only on a Linux host with the
// glibc loader, libc and libgcc. Fetch downloads both packages, checks them
// against the index, unpacks them and writes the archive of tarantool
// sdk/bundle defines.
//
// The executable of a deb keeps its debug information, which the static RPMs
// of the same builds - the same build-id - have stripped: the gdb extensions
// of LuaJIT and Tarantool need it to debug a core of it.
package rws

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/download"
)

const (
	// RepositoryURL is the repository of the static deb packages of
	// Tarantool 3.
	RepositoryURL = "https://rws.tarantool.org/release/series-3/linux-deb/"

	// distribution and component are where the repository has the static
	// packages.
	distribution = "static"
	component    = "main"

	// product is the only product of the source.
	product = "tarantool"
	// series is the major version the repository has the builds of.
	series = "3"

	goosLinux   = "linux"
	goarchAMD64 = "amd64"
	goarchARM64 = "arm64"

	debArchAMD64 = "amd64"
	debArchARM64 = "arm64"
)

var (
	// debArches are the architectures of the repository, by GOARCH.
	debArches = map[string]string{goarchAMD64: debArchAMD64, goarchARM64: debArchARM64}

	errUnknownProduct  = errors.New("unknown product")
	errInvalidLocator  = errors.New("invalid locator")
	errPackageContents = errors.New("invalid package contents")
	errChecksum        = errors.New("package checksum mismatch")
)

// Source is the repository of the static deb packages of Tarantool.
type Source struct {
	client        download.Client
	repositoryURL string
	indexLimit    int64
	dataLimit     int64
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
		dataLimit:     maxDebDataSize,
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

// List implements bundle.Source. It lists the builds of Tarantool 3 on a
// Linux host with glibc; the repository has no development builds. The
// version of a query narrows the listing to a release line.
func (s *Source) List(ctx context.Context, query bundle.Query) ([]bundle.Artifact, error) {
	if query.Product != product {
		return nil, fmt.Errorf("%w %q", errUnknownProduct, query.Product)
	}

	packageArch := debArches[query.Arch]

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

	indexURL := fmt.Sprintf("%s/dists/%s/%s/binary-%s/Packages",
		strings.TrimRight(s.repositoryURL, "/"), distribution, component, packageArch)

	paragraphs, err := s.index(ctx, indexURL)
	if err != nil {
		return nil, err
	}

	return selectPackages(paragraphs, s.repositoryURL, query.Version, packageArch), nil
}

// Fetch implements bundle.Source. It downloads the runtime and the headers
// package of the build into a temporary directory, checks them against the
// size and the SHA-256 of the index, unpacks them and writes the executable,
// the headers, the share/tarantool of the runtime package - the EmmyLua
// stubs of Tarantool 3.9 and later - and its LICENSE and AUTHORS to dst as
// a tar.gz. A package the index named and the repository does not serve is
// a failed delivery.
func (s *Source) Fetch(ctx context.Context, artifact bundle.Artifact, dst io.Writer) error {
	var packages locator

	err := json.Unmarshal([]byte(artifact.Locator), &packages)
	if err != nil || !packages.Runtime.valid() || !packages.Headers.valid() {
		return fmt.Errorf("%w %q of %s", errInvalidLocator, artifact.Locator, s.Name())
	}

	extractDir, err := os.MkdirTemp("", "tt-rws-")
	if err != nil {
		return fmt.Errorf("create package extraction directory: %w", err)
	}

	forget := binary.RemoveOnInterrupt(extractDir)

	defer func() {
		_ = os.RemoveAll(extractDir)

		forget()
	}()

	for _, item := range []struct {
		file      packageFile
		directory string
	}{
		{packages.Runtime, "runtime"}, {packages.Headers, "headers"},
	} {
		err = s.downloadAndExtractDeb(ctx, item.file, extractDir, item.directory)
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
		return fmt.Errorf("%w: prebuilt Tarantool headers package has no "+
			"usr/include/tarantool/module.h; use --source-build to build from source",
			errPackageContents)
	}

	usrShare := filepath.Join(extractDir, "runtime", "usr", "share")
	contents := archiveContents{
		executable: executable, headers: headers, share: "",
		licenses: findLicenses(filepath.Join(usrShare, "licenses", product),
			filepath.Join(usrShare, "doc", product)),
	}

	info, err = os.Stat(filepath.Join(usrShare, product))
	if err == nil && info.IsDir() {
		contents.share = filepath.Join(usrShare, product)
	}

	return writeArchive(dst, contents)
}

// licenseNames are the names of the license files of Tarantool.
var licenseNames = []string{"LICENSE", "LICENSE.md", "LICENSE.txt", "COPYING", "AUTHORS"}

// findLicenses returns the license files of the first of the directories,
// usr/share/licenses/tarantool and usr/share/doc/tarantool, the one of a
// deb, that has any.
func findLicenses(directories ...string) []string {
	for _, directory := range directories {
		var licenses []string

		for _, name := range licenseNames {
			path := filepath.Join(directory, name)

			info, err := os.Lstat(path)
			if err == nil && info.Mode().IsRegular() {
				licenses = append(licenses, path)
			}
		}

		if len(licenses) > 0 {
			return licenses
		}
	}

	return nil
}

// downloadAndExtractDeb downloads the package file into extractDir, checks
// it and unpacks its data into the directory name of extractDir.
func (s *Source) downloadAndExtractDeb(ctx context.Context, file packageFile,
	extractDir, name string,
) error {
	archive, err := os.CreateTemp(extractDir, name+"-*.deb")
	if err != nil {
		return fmt.Errorf("create temporary package: %w", err)
	}

	defer func() { _ = os.Remove(archive.Name()) }()

	digest := sha256.New()

	copyErr := s.client.Copy(ctx, file.URL, "Tarantool", io.MultiWriter(archive, digest))
	closeErr := archive.Close()

	if copyErr != nil {
		return copyErr
	}

	if closeErr != nil {
		return fmt.Errorf("close downloaded package: %w", closeErr)
	}

	info, err := os.Stat(archive.Name())
	if err != nil {
		return fmt.Errorf("check downloaded package: %w", err)
	}

	actual := hex.EncodeToString(digest.Sum(nil))
	if actual != file.SHA256 || info.Size() != file.Size {
		return fmt.Errorf("%w: Tarantool package %s has SHA-256 %s and %d bytes, "+
			"the package index lists %s and %d bytes; use --source-build to build from source",
			errChecksum, file.URL, actual, info.Size(), file.SHA256, file.Size)
	}

	err = extractDeb(archive.Name(), filepath.Join(extractDir, name), s.dataLimit)
	if err != nil {
		return fmt.Errorf("extract Tarantool package %s: %w", file.URL, err)
	}

	return nil
}

// index returns the paragraphs of the package index at indexURL.
func (s *Source) index(ctx context.Context, indexURL string) ([]paragraph, error) {
	response, err := s.client.Get(ctx, indexURL)
	if err != nil {
		return nil, fmt.Errorf("fetch Tarantool package index %s: %w", indexURL, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: fetch Tarantool package index %s: HTTP %s; "+
			"use --source-build to build from source",
			download.ErrHTTPStatus, indexURL, response.Status)
	}

	limited := &io.LimitedReader{R: response.Body, N: s.indexLimit + 1}

	paragraphs, err := parseIndex(limited)
	if limited.N == 0 {
		return nil, fmt.Errorf("%w: Tarantool package index exceeds %d bytes",
			download.ErrTooLarge, s.indexLimit)
	}

	if err != nil {
		return nil, fmt.Errorf("read Tarantool package index %s: %w", indexURL, err)
	}

	return paragraphs, nil
}
