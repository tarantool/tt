package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"

	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errNoAPIDoerWasApplied          = errors.New("no API doer was applied")
	errNoPlatformInformerWasApplied = errors.New("no platform informer was applied")

	errUnsupportedArchitecture = errors.New("unsupported architecture: ")
	errUnsupportedOS           = errors.New("unsupported OS: ")
)

const (
	TntIoURI = "https://www.tarantool.io/en/accounts/customer_zone"
	APIURI   = TntIoURI + "/api"
	PkgURI   = TntIoURI + "/packages"
)

type apiRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Query    string `json:"query"`
}

// PlatformInformer is an interface that provides methods to get platform information.
type PlatformInformer interface {
	// GetOs returns the operating system type.
	GetOs() (binary.OsType, error)
	// GetArch returns the architecture type.
	GetArch() (string, error)
}

type realInfo struct{}

func NewPlatformInformer() *realInfo {
	return &realInfo{}
}

// GetOs implement PlatformInformer interface.
func (*realInfo) GetOs() (binary.OsType, error) {
	return binary.GetOs()
}

// GetArch implement PlatformInformer interface.
func (*realInfo) GetArch() (string, error) {
	return binary.GetArch()
}

// httpDoer is a struct that implements the TntIoDoer interface using the http package.
type httpDoer struct {
	client *http.Client
	token  string
}

func NewTntIoDoer() *httpDoer {
	return &httpDoer{
		client: http.DefaultClient,
		token:  "",
	}
}

// Do implement TntIoDoer interface.
// It sends an HTTP request and returns Body data from HTTP response.
// It also saves the session token from the response cookies.
func (d *httpDoer) Do(req *http.Request) ([]byte, error) {
	resp, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send API request: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, err := io.ReadAll(resp.Body)

		return nil, fmt.Errorf("API request failed with status %s: %q; %w",
			resp.Status, string(bodyBytes), err)
	}

	d.token = getSessionToken(resp.Cookies())

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read API response body: %w", err)
	}

	return respBody, nil
}

func (d *httpDoer) Token() string {
	return d.token
}

// getOsForApi determines the OS type string required by the tarantool.io API.
func getOsForAPI(informer PlatformInformer) (string, error) {
	osType, err := informer.GetOs()
	if err != nil {
		return "", fmt.Errorf("failed to get OS: %w", err)
	}

	switch osType {
	case binary.OsLinux:
		return "linux", nil
	case binary.OsMacos:
		return "macos", nil
	default:
		return "", fmt.Errorf("%w%d", errUnsupportedOS, osType)
	}
}

// getArchForApi determines the architecture type string required by the tarantool.io API.
func getArchForAPI(informer PlatformInformer, program binary.Program) (string, error) {
	arch, err := informer.GetArch()
	if err != nil {
		return "", fmt.Errorf("failed to get architecture: %w", err)
	}

	// Here is default architecture mapping, accepted for tarantool.io API.
	archMapping := map[string]string{
		"x86_64":  "x86_64",
		"aarch64": "aarch64",
	}
	// Workaround: change mapping for specific packages.
	// Best way to unify paths for all apps in the customer zone.
	if program.BundleSource().IsStandaloneArchive() {
		archMapping["x86_64"] = "amd64"
		archMapping["aarch64"] = "arm64"
	}

	if arch, ok := archMapping[arch]; ok {
		// Return arch only if it in valid mapping.
		return arch, nil
	}

	return "", fmt.Errorf("%w%s", errUnsupportedArchitecture, arch)
}

func getBuildType(isDev bool) string {
	if isDev {
		return "dev"
	}

	return "release"
}

// TntIoMakePkgURI generates a URI for downloading a package.
func TntIoMakePkgURI(searchCtx *SearchCtx, tarball string) (string, error) {
	query, err := packageQuery(searchCtx, searchCtx.ReleaseVersion)
	if err != nil {
		return "", err
	}

	return PkgURI + "/" + query + "/" + tarball, nil
}

// bundlePlatformSuffix validates Linux-only tt-ee availability and filters its
// flat version catalog by host architecture. Other sources already group by platform.
func bundlePlatformSuffix(program binary.Program, informer PlatformInformer) (string, error) {
	if program.BundleSource() != binary.ProgramTtEe {
		return "", nil
	}

	if informer == nil || reflect.ValueOf(informer).IsNil() {
		return "", errNoPlatformInformerWasApplied
	}

	osType, err := getOsForAPI(informer)
	if err != nil {
		return "", err
	}

	if osType != "linux" {
		return "", fmt.Errorf("%w%s: tt-ee archives are available only for Linux",
			errUnsupportedOS, osType)
	}

	arch, err := getArchForAPI(informer, program)
	if err != nil {
		return "", err
	}

	return "-" + osType + "-" + arch + ".tar.gz", nil
}

// packageQuery is shared by discovery and downloads. SDK/TCM catalogs group by
// build type and platform; tt-ee versions are directly under the package root.
func packageQuery(searchCtx *SearchCtx, releaseVersion string) (string, error) {
	if searchCtx.platformInformer == nil || reflect.ValueOf(searchCtx.platformInformer).IsNil() {
		return "", errNoPlatformInformerWasApplied
	}

	query := GetAPIPackage(searchCtx.Program)
	if searchCtx.Program.BundleSource() == binary.ProgramTtEe {
		_, err := bundlePlatformSuffix(searchCtx.Program, searchCtx.platformInformer)
		if err != nil {
			return "", err
		}
	} else {
		arch, err := getArchForAPI(searchCtx.platformInformer, searchCtx.Program)
		if err != nil {
			return "", fmt.Errorf("failed to get architecture: %w", err)
		}

		osType, err := getOsForAPI(searchCtx.platformInformer)
		if err != nil {
			return "", fmt.Errorf("failed to get OS type for API: %w", err)
		}

		query += "/" + getBuildType(searchCtx.DevBuilds) + "/" + osType + "/" + arch
	}

	if releaseVersion != "" {
		query += "/" + releaseVersion
	}

	return query, nil
}

// buildApiQuery constructs the query string for the tarantool.io API.
func buildAPIQuery(searchCtx *SearchCtx, credentials connect.UserCredentials) (
	apiRequest, error,
) {
	releaseVersion := searchCtx.ReleaseVersion
	if searchCtx.Program.BundleSource() == binary.ProgramTtEe {
		// Read the flat catalog once; --version may be a partial version mask.
		releaseVersion = ""
	}

	query, err := packageQuery(searchCtx, releaseVersion)
	if err != nil {
		return apiRequest{}, err
	}

	return apiRequest{
		Username: credentials.Username,
		Password: credentials.Password,
		Query:    query,
	}, nil
}

// sendApiRequest prepares and sends the request to the tarantool.io API.
func sendAPIRequest(request apiRequest, doer binary.TntIoDoer) ([]byte, error) {
	postData, err := json.Marshal(request) //nolint:gosec // the API takes the password in the body
	if err != nil {
		return nil, fmt.Errorf("failed to marshal API request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, APIURI, bytes.NewBuffer(postData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tt") // Consider making User-Agent configurable or dynamic.

	if doer != nil {
		return doer.Do(req)
	}

	return nil, errNoAPIDoerWasApplied
}

// getSessionToken parse cookies to get session token.
func getSessionToken(cookies []*http.Cookie) string {
	for _, cookie := range cookies {
		if cookie.Name == "sessionid" {
			return cookie.Value
		}
	}

	return ""
}

// filterChecksums removes checksum files from the API response.
func filterChecksums(apiReply map[string][]string) {
	for release, pkgs := range apiReply {
		var filtered []string

		for _, pkg := range pkgs {
			if !strings.HasSuffix(pkg, ".sha256") {
				filtered = append(filtered, pkg)
			}
		}

		apiReply[release] = filtered
	}
}

// parseApiResponse processes the HTTP response from the tarantool.io API.
func parseAPIResponse(respBody []byte) (map[string][]string, error) {
	var apiReply map[string][]string

	err := json.Unmarshal(respBody, &apiReply)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal API response JSON: %w. Body: %s",
			err, string(respBody))
	}

	filterChecksums(apiReply)

	return apiReply, nil
}

// tntIoGetPkgVersions returns a list of versions of the requested package for the given host.
func tntIoGetPkgVersions(credentials connect.UserCredentials, searchCtx *SearchCtx) (
	map[string][]string, error,
) {
	if searchCtx.TntIoDoer == nil {
		return nil, binary.ErrTarantoolIODoerMissing
	}

	if searchCtx.platformInformer == nil {
		return nil, errNoPlatformInformerWasApplied
	}

	request, err := buildAPIQuery(searchCtx, credentials)
	if err != nil {
		return nil, err
	}

	resp, err := sendAPIRequest(request, searchCtx.TntIoDoer)
	if err != nil {
		return nil, err
	}

	apiReply, err := parseAPIResponse(resp)
	if err != nil {
		return nil, err
	}

	return apiReply, nil
}
