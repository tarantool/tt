package search

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/util"
)

const TntIoURI = "https://www.tarantool.io/en/accounts/customer_zone"
const ApiURI = TntIoURI + "/api"
const PkgURI = TntIoURI + "/packages"

type apiRequst struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Query    string `json:"query"`
}

// TntIoMakePkgURI generates a URI for downloading a package.
func TntIoMakePkgURI(Package string, Release string,
	Tarball string, DevBuilds bool) (string, error) {
	var uri string
	var osType string
	buildType := "release"

	if DevBuilds {
		buildType = "dev"
	}

	arch, err := util.GetArch()
	if err != nil {
		return "", err
	}

	os, err := util.GetOs()
	if err != nil {
		return "", err
	}

	switch os {
	case util.OsLinux:
		osType = "linux"
	case util.OsMacos:
		osType = "macos"
	default:
		return "", fmt.Errorf("unsupported OS")
	}

	uri = fmt.Sprintf("%s/%s/%s/%s/%s/%s/%s",
		PkgURI, Package, buildType, osType, arch, Release, Tarball)

	return uri, nil
}

// tntIoGetPkgVersions returns a list of versions of the requested package for the given host.
func tntIoGetPkgVersions(cliOpts *config.CliOpts,
	searchCtx SearchCtx) (apiReply map[string][]string, token string, err error) {
	var query string
	var osType string
	buildType := "release"

	arch, err := util.GetArch()
	if err != nil {
		return nil, "", err
	}

	os, err := util.GetOs()
	if err != nil {
		return nil, "", err
	}

	switch os {
	case util.OsLinux:
		osType = "linux"
	case util.OsMacos:
		osType = "macos"
	default:
		return nil, "", fmt.Errorf("unsupported OS")
	}

	credentials, err := install_ee.GetCreds(cliOpts)
	if err != nil {
		return nil, "", err
	}

	if searchCtx.DevBuilds {
		buildType = "dev"
	}

	if len(searchCtx.ReleaseVersion) > 0 {
		query = fmt.Sprintf("%s/%s/%s/%s/%s",
			searchCtx.Package, buildType, osType, arch, searchCtx.ReleaseVersion)
	} else {
		query = fmt.Sprintf("%s/%s/%s/%s",
			searchCtx.Package, buildType, osType, arch)
	}

	msg := apiRequst{
		credentials.Username,
		credentials.Password,
		query,
	}

	postData, err := json.Marshal(msg)
	if err != nil {
		return nil, "", err
	}

	req, err := http.NewRequest(http.MethodPost, ApiURI, bytes.NewBuffer(postData))
	if err != nil {
		return nil, "", err
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Set("User-Agent", "tt")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, "", err
	} else if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, "", fmt.Errorf("HTTP request error: %s", http.StatusText(resp.StatusCode))
	}
	defer resp.Body.Close()

	// Get session cookie.
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "sessionid" {
			token = cookie.Value
			break
		}
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	err = json.Unmarshal(respBody, &apiReply)
	if err != nil {
		return nil, "", err
	}

	// Filter checksums.
	for release, pkgs := range apiReply {
		var filtered []string
		for _, pkg := range pkgs {
			if !strings.HasSuffix(pkg, ".sha256") {
				filtered = append(filtered, pkg)
			}
		}
		apiReply[release] = filtered
	}

	return apiReply, token, nil
}
