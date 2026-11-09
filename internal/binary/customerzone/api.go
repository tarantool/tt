package customerzone

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"golang.org/x/net/html"
)

var errMissingCatalogSession = errors.New(
	"tarantool.io API returned no session for package catalog",
)

const (
	// TntIoURI is the customer zone of tarantool.io.
	TntIoURI = "https://www.tarantool.io/en/accounts/customer_zone"
	// APIURI is the API that lists the archives of a product.
	APIURI = TntIoURI + "/api"
	// PkgURI is where the archives are downloaded from.
	PkgURI = TntIoURI + "/packages"
)

type apiRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Query    string `json:"query"`
}

// apiPackage is the name of the package of program in the customer zone.
func apiPackage(program binary.Program) string {
	switch program.BundleSource() {
	case binary.ProgramSDK:
		return "enterprise"
	case binary.ProgramTcm:
		return "tarantool-cluster-manager"
	case binary.ProgramTtEe:
		return binary.ProgramTtEe.String()
	default:
		// These programs do not have a customer-zone API package name.
	}

	return ""
}

// apiOS is the name of the OS goos in the customer zone.
func apiOS(goos string) (string, error) {
	switch goos {
	case goosLinux:
		return apiOSLinux, nil
	case goosDarwin:
		return apiOSMacOS, nil
	default:
		return "", fmt.Errorf("%w%s", errUnsupportedOS, goos)
	}
}

// apiArch is the name of the architecture goarch in the customer zone. The
// SDK names them as uname does, the standalone archives as Go does.
func apiArch(program binary.Program, goarch string) (string, error) {
	archMapping := map[string]string{goarchAMD64: apiArchX8664, goarchARM64: apiArchARM64}
	if program.BundleSource().IsStandaloneArchive() {
		archMapping = map[string]string{goarchAMD64: goarchAMD64, goarchARM64: goarchARM64}
	}

	arch, ok := archMapping[goarch]
	if !ok {
		return "", fmt.Errorf("%w%s", errUnsupportedArchitecture, goarch)
	}

	return arch, nil
}

// buildType is the directory of the builds of a channel in the customer zone.
func buildType(channel bundle.Channel) string {
	if channel == bundle.ChannelDev {
		return "dev"
	}

	return "release"
}

// packageQuery is shared by discovery and downloads. SDK and TCM catalogs
// group by build type and platform; tt-ee versions are directly under the
// package root. release is the release directory, empty for the whole package.
func packageQuery(program binary.Program, query bundle.Query, release string) (string, error) {
	result := apiPackage(program)

	if program.BundleSource() != binary.ProgramTtEe {
		arch, err := apiArch(program, query.Arch)
		if err != nil {
			return "", fmt.Errorf("failed to get architecture: %w", err)
		}

		osType, err := apiOS(query.OS)
		if err != nil {
			return "", fmt.Errorf("failed to get OS type for API: %w", err)
		}

		result += "/" + buildType(query.Channel) + "/" + osType + "/" + arch
	}

	if release != "" {
		result += "/" + release
	}

	return result, nil
}

// sendAPIRequest prepares and sends the request to the tarantool.io API.
func sendAPIRequest(ctx context.Context, request apiRequest, doer binary.TntIoDoer) (
	[]byte, error,
) {
	postData, err := json.Marshal(request) //nolint:gosec // the API takes the password in the body
	if err != nil {
		return nil, fmt.Errorf("failed to marshal API request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, APIURI,
		bytes.NewBuffer(postData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tt")

	return doer.Do(req)
}

// parseAPIResponse processes the HTTP response from the tarantool.io API:
// the archives of each release directory, without their checksum files.
func parseAPIResponse(respBody []byte) (map[string][]string, error) {
	var apiReply map[string][]string

	err := json.Unmarshal(respBody, &apiReply)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal API response JSON: %w. Body: %s",
			err, string(respBody))
	}

	for release, pkgs := range apiReply {
		var filtered []string

		for _, pkg := range pkgs {
			if !strings.HasSuffix(pkg, ".sha256") {
				filtered = append(filtered, pkg)
			}
		}

		apiReply[release] = filtered
	}

	return apiReply, nil
}

// packageVersions returns the archives of the product of query in each
// release directory.
func packageVersions(ctx context.Context, credentials connect.UserCredentials,
	program binary.Program, query bundle.Query, doer binary.TntIoDoer,
) (map[string][]string, error) {
	if program == binary.ProgramTtEe {
		// The API requires a nonempty query, so authenticate through its existing
		// enterprise catalog. Discover tt-ee versions in the website catalog.
		_, err := sendAPIRequest(ctx, apiRequest{
			Username: credentials.Username,
			Password: credentials.Password,
			Query:    apiPackage(binary.ProgramSDK),
		}, doer)
		if err != nil {
			return nil, err
		}

		return ttEECatalogVersions(ctx, query.Version, doer)
	}

	request, err := packageQuery(program, query, query.Version)
	if err != nil {
		return nil, err
	}

	resp, err := sendAPIRequest(ctx, apiRequest{
		Username: credentials.Username,
		Password: credentials.Password,
		Query:    request,
	}, doer)
	if err != nil {
		return nil, err
	}

	return parseAPIResponse(resp)
}

// ttEECatalogVersions reads the flat catalog of tt-ee: a directory per
// version. A line skips the directories of other versions.
func ttEECatalogVersions(ctx context.Context, line string, doer binary.TntIoDoer) (
	map[string][]string, error,
) {
	address := PkgURI + "/tt-ee"

	entries, err := ttEEDirectoryEntries(ctx, address, doer)
	if err != nil {
		return nil, err
	}

	versions := make(map[string][]string)

	for _, entry := range entries {
		if _, exists := versions[entry]; exists {
			continue
		}

		parsed, err := version.Parse(entry)
		if err != nil || !onReleaseLine(line, parsed) {
			continue
		}

		files, err := ttEEDirectoryEntries(ctx, address+"/"+url.PathEscape(entry), doer)
		if err != nil {
			return nil, err
		}

		versions[entry] = files
	}

	return versions, nil
}

func ttEEDirectoryEntries(ctx context.Context, address string, doer binary.TntIoDoer) (
	[]string, error,
) {
	if doer.Token() == "" {
		return nil, errMissingCatalogSession
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("create package catalog request: %w", err)
	}

	request.Header.Set("User-Agent", "tt")
	binary.AddSessionCookie(request, doer.Token())

	body, err := doer.Do(request)
	if err != nil {
		return nil, fmt.Errorf("read package catalog %s: %w", address, err)
	}

	// Only collect names. Requests are built from PkgURI; catalog links are
	// never followed directly.
	var entries []string

	tokenizer := html.NewTokenizer(bytes.NewReader(body))
	for tokenizer.Next() != html.ErrorToken {
		token := tokenizer.Token()
		if token.Type != html.StartTagToken || token.Data != "a" {
			continue
		}

		for _, attribute := range token.Attr {
			if attribute.Key != "href" {
				continue
			}

			link, err := url.Parse(attribute.Val)
			if err != nil || link.User != nil || link.RawQuery != "" || link.Fragment != "" ||
				(link.Host != "" && link.Host != request.URL.Host) {
				continue
			}

			entries = append(entries, path.Base(strings.TrimRight(link.Path, "/")))
		}
	}

	err = tokenizer.Err()
	if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("parse tt-ee catalog: %w", err)
	}

	return entries, nil
}
