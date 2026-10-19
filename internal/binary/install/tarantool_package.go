package install

import (
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

	"golang.org/x/net/html"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	tarantoolPackageRepositoryURL = "https://rws.tarantool.org/release/series-3/linux-rpm/static/"
)

var (
	packageVersionPattern = regexp.MustCompile(`^3\.[0-9]+\.[0-9]+(?:[-.][A-Za-z0-9.]+)?$`)
	packageReleasePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~^]*$`)
)

// tarantoolPackageDownloader keeps HTTP configuration local, including in tests.
type tarantoolPackageDownloader struct {
	packageDownloader

	repositoryURL string
	indexLimit    int64
	systemRoot    string
}

// preparePrebuiltTarantool downloads and extracts binary and headers for a resolved release.
func preparePrebuiltTarantool(version, destination string, keepTemp bool) (
	programArtifact, error,
) {
	if runtime.GOOS != osLinux {
		return programArtifact{}, fmt.Errorf("%w: unsupported platform %s/%s; "+
			"use --source-build to build from source",
			errPrebuiltPackageUnavailable, runtime.GOOS, runtime.GOARCH)
	}

	log.Infof("Installing prebuilt tarantool=%s", version)

	downloader := tarantoolPackageDownloader{
		client:     &http.Client{Timeout: packageDownloadTimeout},
		indexLimit: maxPackageIndexSize, fileLimit: maxPackageSize, keepTemp: keepTemp,
		repositoryURL: tarantoolPackageRepositoryURL,
		systemRoot:    "/",
	}

	return downloader.prepare(version, destination, runtime.GOARCH)
}

func (d tarantoolPackageDownloader) prepare(version, destination, goarch string) (
	programArtifact, error,
) {
	packageArch := map[string]string{archAMD64: rpmArchAMD64, archARM64: rpmArchARM64}[goarch]
	if packageArch == "" || !packageVersionPattern.MatchString(version) {
		return programArtifact{}, fmt.Errorf("%w: no prebuilt Tarantool package "+
			"for %q on linux/%s; use --source-build to build from source",
			errPrebuiltPackageUnavailable, version, goarch)
	}

	err := checkTarantoolPackagePlatform(goarch, d.systemRoot)
	if err != nil {
		return programArtifact{}, err
	}

	log.Infof("Downloading Tarantool binary and headers...")

	packagesURL := fmt.Sprintf("%s/%s/Packages/",
		strings.TrimRight(d.repositoryURL, "/"), packageArch)

	runtimeName, headersName, err := d.selectPackages(packagesURL, version, packageArch)
	if err != nil {
		return programArtifact{}, err
	}

	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return programArtifact{}, fmt.Errorf("resolve package destination: %w", err)
	}

	err = os.MkdirAll(absoluteDestination, defaultDirPermissions)
	if err != nil {
		return programArtifact{}, fmt.Errorf("create package destination: %w", err)
	}

	extractDir, err := os.MkdirTemp(absoluteDestination, "tarantool-package-")
	if err != nil {
		return programArtifact{}, fmt.Errorf("create package extraction directory: %w", err)
	}

	succeeded := false

	defer func() {
		if !succeeded && !d.keepTemp {
			_ = os.RemoveAll(extractDir)
		}
	}()

	for _, item := range []struct{ name, directory string }{
		{runtimeName, "runtime"}, {headersName, "headers"},
	} {
		directory := filepath.Join(extractDir, item.directory)

		err = os.Mkdir(directory, defaultDirPermissions)
		if err != nil {
			return programArtifact{}, fmt.Errorf("create RPM extraction directory: %w", err)
		}

		err = d.downloadAndExtractRPM(packagesURL+item.name, directory)
		if err != nil {
			return programArtifact{}, err
		}
	}

	srcExecutable := filepath.Join(extractDir, "runtime", "usr", "bin", "tarantool")
	srcHeaders := filepath.Join(extractDir, "headers", "usr", "include", "tarantool")
	info, err := os.Lstat(srcExecutable)

	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
		return programArtifact{}, fmt.Errorf("%w: prebuilt Tarantool package has no executable "+
			"usr/bin/tarantool; use --source-build to build from source", errPackageContents)
	}

	info, err = os.Lstat(filepath.Join(srcHeaders, "module.h"))
	if err != nil || !info.Mode().IsRegular() {
		return programArtifact{}, fmt.Errorf("%w: prebuilt Tarantool devel package has no "+
			"usr/include/tarantool/module.h; use --source-build to build from source",
			errPackageContents)
	}

	succeeded = true

	return programArtifact{
		program: binary.ProgramCe, executable: srcExecutable, headers: srcHeaders,
	}, nil
}

func (d tarantoolPackageDownloader) selectPackages(
	packagesURL, version, packageArch string,
) (
	string, string, error,
) {
	response, err := d.get(packagesURL)
	if err != nil {
		return "", "", fmt.Errorf("fetch Tarantool package index %s: %w", packagesURL, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("%w: fetch Tarantool package index %s: HTTP %s; "+
			"use --source-build to build from source",
			errPackageHTTPStatus, packagesURL, response.Status)
	}

	limited := &io.LimitedReader{R: response.Body, N: d.indexLimit + 1}

	files, err := parsePackageIndex(limited)
	if err != nil {
		return "", "", fmt.Errorf("read Tarantool package index: %w", err)
	}

	if limited.N == 0 {
		return "", "", fmt.Errorf("%w: Tarantool package index exceeds %d bytes",
			errPackageTooLarge, d.indexLimit)
	}

	prefix, suffix := "tarantool-"+version+"-", "."+packageArch+".rpm"

	var runtimePackageName, selectedRelease string

	for name := range files {
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
			continue
		}

		candidateRelease := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
		if !packageReleasePattern.MatchString(candidateRelease) {
			continue
		}

		comparison := comparePackageReleases(candidateRelease, selectedRelease)
		if runtimePackageName == "" || comparison > 0 ||
			comparison == 0 && candidateRelease > selectedRelease {
			runtimePackageName, selectedRelease = name, candidateRelease
		}
	}

	if runtimePackageName == "" {
		return "", "", fmt.Errorf("%w: no prebuilt Tarantool package for release %q (%s); "+
			"use --source-build to build from source",
			errPrebuiltPackageUnavailable, version, packageArch)
	}

	// Headers must match the newest runtime revision exactly. Do not silently
	// select an older runtime just because its devel package is still present.
	headersName := "tarantool-devel-" + version + "-" + selectedRelease + suffix
	if _, found := files[headersName]; !found {
		return "", "", fmt.Errorf("%w: no matching headers package %s for %s; "+
			"use --source-build to build from source",
			errPrebuiltPackageUnavailable, headersName, runtimePackageName)
	}

	return runtimePackageName, headersName, nil
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

func (d tarantoolPackageDownloader) downloadAndExtractRPM(
	address, destination string,
) error {
	archive, err := d.download(address, destination, "package-*.rpm", "Tarantool")
	if err != nil {
		return err
	}

	if !d.keepTemp {
		defer func() { _ = os.Remove(archive) }()
	}

	err = extractRPM(archive, destination)
	if err != nil {
		return fmt.Errorf("extract Tarantool package %s: %w", address, err)
	}

	return nil
}

// comparePackageReleases compares RPM release segments: numbers compare by value,
// numeric segments sort after letters, and ~ sorts before a normal release.
func comparePackageReleases(left, right string) int {
	for left != "" || right != "" {
		left = strings.TrimLeftFunc(left, isPackageReleaseSeparator)
		right = strings.TrimLeftFunc(right, isPackageReleaseSeparator)

		if strings.HasPrefix(left, "~") || strings.HasPrefix(right, "~") {
			if !strings.HasPrefix(left, "~") {
				return 1
			}

			if !strings.HasPrefix(right, "~") {
				return -1
			}

			left, right = left[1:], right[1:]

			continue
		}

		if strings.HasPrefix(left, "^") || strings.HasPrefix(right, "^") {
			switch {
			case left == "":
				return -1
			case right == "":
				return 1
			case left[0] != '^':
				return 1
			case right[0] != '^':
				return -1
			}

			left, right = left[1:], right[1:]

			continue
		}

		if left == "" || right == "" {
			return strings.Compare(left, right)
		}

		leftNumber, rightNumber := isPackageReleaseDigit(left[0]), isPackageReleaseDigit(right[0])
		if leftNumber != rightNumber {
			if leftNumber {
				return 1
			}

			return -1
		}

		leftEnd, rightEnd := packageReleaseSegmentEnd(left), packageReleaseSegmentEnd(right)
		leftSegment, rightSegment := left[:leftEnd], right[:rightEnd]

		if leftNumber {
			leftSegment = strings.TrimLeft(leftSegment, "0")
			rightSegment = strings.TrimLeft(rightSegment, "0")

			if len(leftSegment) > len(rightSegment) {
				return 1
			}

			if len(leftSegment) < len(rightSegment) {
				return -1
			}
		}

		if comparison := strings.Compare(leftSegment, rightSegment); comparison != 0 {
			return comparison
		}

		left, right = left[leftEnd:], right[rightEnd:]
	}

	return 0
}

func isPackageReleaseDigit(character byte) bool {
	return character >= '0' && character <= '9'
}

func isPackageReleaseSeparator(character rune) bool {
	return character != '~' && character != '^' &&
		(character < '0' || character > '9') &&
		(character < 'a' || character > 'z') && (character < 'A' || character > 'Z')
}

func packageReleaseSegmentEnd(value string) int {
	numeric := isPackageReleaseDigit(value[0])
	for index := range len(value) {
		character := value[index]
		if isPackageReleaseSeparator(rune(character)) || character == '~' || character == '^' ||
			isPackageReleaseDigit(character) != numeric {
			return index
		}
	}

	return len(value)
}
