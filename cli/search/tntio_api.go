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

// platformInformer is an interface that provides methods to get platform information.
type platformInformer interface {
	// GetOs returns the operating system type.
	GetOs() (string, error)
	// GetArch returns the architecture type.
	GetArch() (string, error)
}

// platformInfo is a struct that implements the platformInformer interface.
type platformInfo struct {
	program string
}

// apiDoer is an interface that wraps the Do method.
type apiDoer interface {
	// Do sends an HTTP request and returns Body data from HTTP response.
	Do(req *http.Request) ([]byte, error)

	// Token provides the session token after the Do method is executed.
	Token() string
}

// httpDoer is a struct that implements the apiDoer interface using the http package.
type httpDoer struct {
	client *http.Client
	token  string
}

// GetOs implement platformInformer interface.
// It returns the operating system type.
func (p *platformInfo) GetOs() (string, error) {
	os, err := util.GetOs()
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

// GetArch implement platformInformer interface.
// It returns the architecture type.
func (p *platformInfo) GetArch() (string, error) {
	arch, err := util.GetArch()
	if err != nil {
		return "", fmt.Errorf("failed to get architecture: %w", err)
	}

	m := map[string]string{}
	switch p.program {
	case ProgramTcm:
		m["x86_64"] = "amd64"
	}

	if arch, ok := m[arch]; ok {
		return arch, nil
	}
	return arch, nil
}

// Do implement apiDoer interface.
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

// getOsTypeForAPI determines the OS type string required by the tarantool.io API.
func getOsTypeForAPI() (string, error) {
	os, err := util.GetOs()
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

// TntIoMakePkgURI generates a URI for downloading a package.
func TntIoMakePkgURI(Package string, Release string,
	Tarball string, DevBuilds bool,
) (string, error) {
	var uri string
	buildType := "release"

	if DevBuilds {
		buildType = "dev"
	}

	arch, err := util.GetArch()
	if err != nil {
		return "", err
	}

	osType, err := getOsTypeForAPI() // FIXME: use platformInformer{}
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

	arch, err := searchCtx.platformInformer.GetArch()
	if err != nil {
		return apiRequest{}, fmt.Errorf("failed to get architecture: %w", err)
	}

	osType, err := searchCtx.platformInformer.GetOs()
	if err != nil {
		return apiRequest{}, fmt.Errorf("failed to get OS type for API: %w", err)
	}

	requst := apiRequest{
		Username: credentials.Username,
		Password: credentials.Password,
	}
	if len(searchCtx.ReleaseVersion) > 0 {
		requst.Query = fmt.Sprintf("%s/%s/%s/%s/%s",
			searchCtx.Package, buildType, osType, arch, searchCtx.ReleaseVersion)
	} else {
		requst.Query = fmt.Sprintf("%s/%s/%s/%s",
			searchCtx.Package, buildType, osType, arch)
	}

	return requst, nil
}

// sendApiRequest prepares and sends the request to the tarantool.io API.
func sendApiRequest(request apiRequest, doer apiDoer) ([]byte, error) {
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
	if searchCtx.apiDoer == nil {
		searchCtx.apiDoer = &httpDoer{client: http.DefaultClient}
	}

	request, err := buildApiQuery(searchCtx, credentials)
	if err != nil {
		return nil, err
	}

	resp, err := sendApiRequest(request, searchCtx.apiDoer)
	if err != nil {
		return nil, err
	}

	apiReply, err := parseApiResponse(resp)
	if err != nil {
		return nil, err
	}

	return apiReply, nil
}
