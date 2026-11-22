package rws

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"
)

const (
	// runtimePackage is the package of the executable, headersPackage that
	// of the headers.
	runtimePackage = "tarantool"
	headersPackage = "tarantool-dev"

	// maxIndexLine bounds a line of the package index.
	maxIndexLine = 1 << 20
	// sha256Length is the length of a hex SHA-256.
	sha256Length = 64
)

var (
	// filenamePattern is the path of a package file in the repository.
	filenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~/-]*\.deb$`)

	errInvalidIndex = errors.New("invalid package index")
)

// paragraph is a paragraph of a Debian control file: the fields by their
// names in lower case, the lines of a multiline value joined with newlines.
type paragraph map[string]string

// parseIndex reads the paragraphs of the Packages index of a repository, a
// Debian control file. A line that is neither a field, nor the continuation
// of one, nor blank, and a field repeated in a paragraph are errors: what
// the repository served is no index.
func parseIndex(reader io.Reader) ([]paragraph, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(nil, maxIndexLine)

	var (
		paragraphs []paragraph
		current    paragraph
		field      string
	)

	for number := 1; scanner.Scan(); number++ {
		line := scanner.Text()

		switch {
		case strings.TrimSpace(line) == "":
			current, field = nil, ""
		case line[0] == ' ' || line[0] == '\t':
			if field == "" {
				return nil, fmt.Errorf("%w: line %d continues no field", errInvalidIndex, number)
			}

			current[field] += "\n" + strings.TrimSpace(line)
		default:
			name, value, found := strings.Cut(line, ":")
			if !found || name == "" || strings.ContainsAny(name, " \t") {
				return nil, fmt.Errorf("%w: line %d is no field", errInvalidIndex, number)
			}

			name = strings.ToLower(name)

			if current == nil {
				current = make(paragraph)
				paragraphs = append(paragraphs, current)
			}

			if _, repeated := current[name]; repeated {
				return nil, fmt.Errorf("%w: line %d repeats the field %s",
					errInvalidIndex, number, name)
			}

			current[name], field = strings.TrimSpace(value), name
		}
	}

	err := scanner.Err()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errInvalidIndex, err)
	}

	return paragraphs, nil
}

// packageFile is a package file of the repository, as the index lists it.
type packageFile struct {
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// valid reports whether the file has an address, a size and a digest.
func (file packageFile) valid() bool {
	return file.URL != "" && file.Size > 0 && isSHA256(file.SHA256)
}

// locator is the pair of packages of a build.
type locator struct {
	Runtime packageFile `json:"runtime"`
	Headers packageFile `json:"headers"`
}

// indexedPackage is a package of Tarantool the index lists for the
// architecture.
type indexedPackage struct {
	version debVersion
	file    packageFile
}

// selectPackages returns the builds of the release line among the
// paragraphs of the index of packageArch: for each version the newest
// revision of its runtime package, if the headers package of that revision
// is there too. repositoryURL is the root the file names of the index are
// relative to.
func selectPackages(paragraphs []paragraph, repositoryURL, line, packageArch string,
) []bundle.Artifact {
	newest := make(map[string]indexedPackage)
	headers := make(map[string]packageFile)

	for _, fields := range paragraphs {
		name := fields["package"]
		if name != runtimePackage && name != headersPackage ||
			fields["architecture"] != packageArch {
			continue
		}

		indexed, err := readPackage(fields, repositoryURL)
		if err != nil {
			log.Debugf("skip %s %s in the package index: %v", name, fields["version"], err)

			continue
		}

		if !onLine(line, indexed.version.upstream) {
			continue
		}

		if name == headersPackage {
			if _, seen := headers[indexed.version.raw]; !seen {
				headers[indexed.version.raw] = indexed.file
			}

			continue
		}

		selected, seen := newest[indexed.version.upstream]

		comparison := compareDebVersions(indexed.version, selected.version)
		if !seen || comparison > 0 ||
			comparison == 0 && indexed.version.raw > selected.version.raw {
			newest[indexed.version.upstream] = indexed
		}
	}

	artifacts := make([]bundle.Artifact, 0, len(newest))

	for upstream, runtime := range newest {
		// Headers must match the newest runtime revision exactly. Do not silently
		// select an older runtime just because its headers package is still present.
		headersFile, found := headers[runtime.version.raw]
		if !found {
			log.Debugf("no matching headers package %s %s for %s %s",
				headersPackage, runtime.version.raw, runtimePackage, runtime.version.raw)

			continue
		}

		reference, err := json.Marshal(locator{Runtime: runtime.file, Headers: headersFile})
		if err != nil {
			continue // Strings and numbers always marshal.
		}

		artifacts = append(artifacts, bundle.Artifact{
			Version: upstream, Flavor: nil,
			Name: strings.TrimSuffix(path.Base(runtime.file.URL), ".deb") + ".tar.gz",
			Size: 0, SHA256: "", Locator: string(reference),
		})
	}

	slices.SortFunc(artifacts, func(left, right bundle.Artifact) int {
		return strings.Compare(left.Version, right.Version)
	})

	return artifacts
}

var errInvalidPackage = errors.New("invalid package")

// readPackage returns the version and the file of the package of fields.
func readPackage(fields paragraph, repositoryURL string) (indexedPackage, error) {
	version, ok := parseDebVersion(fields["version"])
	if !ok {
		return indexedPackage{}, fmt.Errorf("%w: no version of Tarantool 3", errInvalidPackage)
	}

	filename := fields["filename"]
	if !filenamePattern.MatchString(filename) || path.Clean(filename) != filename ||
		slices.Contains(strings.Split(filename, "/"), "..") {
		return indexedPackage{}, fmt.Errorf("%w: file name %q", errInvalidPackage, filename)
	}

	size, err := strconv.ParseInt(fields["size"], 10, 64)
	if err != nil || size <= 0 {
		return indexedPackage{}, fmt.Errorf("%w: size %q", errInvalidPackage, fields["size"])
	}

	digest := strings.ToLower(fields["sha256"])
	if !isSHA256(digest) {
		return indexedPackage{}, fmt.Errorf("%w: SHA-256 %q", errInvalidPackage, fields["sha256"])
	}

	return indexedPackage{version: version, file: packageFile{
		URL:  strings.TrimRight(repositoryURL, "/") + "/" + filename,
		Size: size, SHA256: digest,
	}}, nil
}

// isSHA256 reports whether value is a hex SHA-256 in lower case.
func isSHA256(value string) bool {
	return len(value) == sha256Length && strings.Trim(value, "0123456789abcdef") == ""
}

// onLine reports whether version belongs to the release line: "3", "3.7"
// or a whole version. An empty line has every version.
func onLine(line, version string) bool {
	return line == "" || version == line || strings.HasPrefix(version, line+".")
}
