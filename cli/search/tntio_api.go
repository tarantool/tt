package search

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/util"
)

const (
	TntIoURI = "https://www.tarantool.io/en/accounts/customer_zone"
	ApiURI   = TntIoURI + "/api"
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
	GetOs() (util.OsType, error)
	// GetArch returns the architecture type.
	GetArch() (string, error)
}

type realInfo struct{}

// GetOs implement PlatformInformer interface.
func (*realInfo) GetOs() (util.OsType, error) {
	return util.GetOs()
}

// GetArch implement PlatformInformer interface.
func (*realInfo) GetArch() (string, error) {
	return util.GetArch()
}

func NewPlatformInformer() *realInfo {
	return &realInfo{}
}

// TntIoDoer is an interface that wraps the Do method.
type TntIoDoer interface {
	// Do sends an HTTP request and returns Body data from HTTP response.
	Do(req *http.Request) ([]byte, error)

	// Token provides the session token after the Do method is executed.
	Token() string
}

// httpDoer is a struct that implements the TntIoDoer interface using the http package.
type httpDoer struct {
	client *http.Client
	token  string
}

func NewTntIoDoer() *httpDoer {
	return &httpDoer{
		client: http.DefaultClient,
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

	defer resp.Body.Close()

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
func getOsForApi(informer PlatformInformer) (string, error) {
	os, err := informer.GetOs()
	if err != nil {
		return "", fmt.Errorf("failed to get OS: %w", err)
	}

	switch os {
	case util.OsLinux:
		return "linux", nil
	case util.OsMacos:
		return "macos", nil
	default:
		return "", fmt.Errorf("unsupported OS: %d", os)
	}
}

// getArchForApi determines the architecture type string required by the tarantool.io API.
func getArchForApi(informer PlatformInformer, program string) (string, error) {
	arch, err := informer.GetArch()
	if err != nil {
		return "", fmt.Errorf("failed to get architecture: %w", err)
	}

	// Here is default architecture mapping, accepted for tarantool.io API.
	m := map[string]string{
		"x86_64":  "x86_64",
		"aarch64": "aarch64",
	}
	// Workaround: change mapping for specific packages.
	// Best way to unify paths for all apps in the customer zone.
	switch program {
	case ProgramTcm:
		m["x86_64"] = "amd64"
		m["aarch64"] = "arm64"
	}

	if arch, ok := m[arch]; ok {
		// Return arch only if it in valid mapping.
		return arch, nil
	}
	return "", fmt.Errorf("unsupported architecture: %s", arch)
}

// TntIoMakePkgURI generates a URI for downloading a package.
func TntIoMakePkgURI(Package string, Release string,
	Tarball string, DevBuilds bool,
) (string, error) {
	var uri string
	buildType := "release"

	if DevBuilds {
		buildType = "dev"
	}

	// FIXME: use platformInformer from Search Context.
	informer := &realInfo{}
	arch, err := getArchForApi(informer, ProgramEe)
	if err != nil {
		return "", err
	}

	osType, err := getOsForApi(informer)
	if err != nil {
		return "", err
	}

	uri = fmt.Sprintf("%s/%s/%s/%s/%s/%s/%s",
		PkgURI, Package, buildType, osType, arch, Release, Tarball)

	return uri, nil
}

// buildApiQuery constructs the query string for the tarantool.io API.
func buildApiQuery(searchCtx *SearchCtx, credentials install_ee.UserCredentials) (
	apiRequest, error,
) {
	buildType := "release"
	if searchCtx.DevBuilds {
		buildType = "dev"
	}

	arch, err := getArchForApi(searchCtx.platformInformer, searchCtx.ProgramName)
	if err != nil {
		return apiRequest{}, fmt.Errorf("failed to get architecture: %w", err)
	}

	osType, err := getOsForApi(searchCtx.platformInformer)
	if err != nil {
		return apiRequest{}, fmt.Errorf("failed to get OS type for API: %w", err)
	}

	request := apiRequest{
		Username: credentials.Username,
		Password: credentials.Password,
	}
	if len(searchCtx.ReleaseVersion) > 0 {
		request.Query = fmt.Sprintf("%s/%s/%s/%s/%s",
			searchCtx.Package, buildType, osType, arch, searchCtx.ReleaseVersion)
	} else {
		request.Query = fmt.Sprintf("%s/%s/%s/%s",
			searchCtx.Package, buildType, osType, arch)
	}

	return request, nil
}

// sendApiRequest prepares and sends the request to the tarantool.io API.
func sendApiRequest(request apiRequest, doer TntIoDoer) ([]byte, error) {
	postData, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal API request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, ApiURI, bytes.NewBuffer(postData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tt") // Consider making User-Agent configurable or dynamic

	if doer != nil {
		return doer.Do(req)
	}
	return nil, errors.New("no API doer was applied")
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
func parseApiResponse(respBody []byte) (map[string][]string, error) {
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
func tntIoGetPkgVersions(credentials install_ee.UserCredentials, searchCtx *SearchCtx) (
	map[string][]string, error,
) {
	if searchCtx.tntIoDoer == nil {
		return nil, fmt.Errorf("no tarantool.io doer was applied")
	}
	if searchCtx.platformInformer == nil {
		return nil, fmt.Errorf("no platform informer was applied")
	}

	request, err := buildApiQuery(searchCtx, credentials)
	if err != nil {
		return nil, err
	}

	resp, err := sendApiRequest(request, searchCtx.tntIoDoer)
	if err != nil {
		return nil, err
	}

	apiReply, err := parseApiResponse(resp)
	if err != nil {
		return nil, err
	}

	return apiReply, nil
}
