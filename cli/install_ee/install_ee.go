package install_ee

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

const (
	eeSourceLinux string = "https://download.tarantool.io/enterprise/"
	eeSourceMacos string = "https://download.tarantool.io/enterprise-macos/"
)

// getTarballName extracts tarball name from html data.
func getTarballName(data string) (string, error) {
	re := regexp.MustCompile(">(.*)<")

	parsedData := re.FindStringSubmatch(data)
	if len(parsedData) == 0 {
		return "", fmt.Errorf("cannot parse tarball name")
	}

	return parsedData[1], nil
}

// getVersions collects a list of all available tarantool-ee
// versions for the host architecture.
func getVersions(data *[]byte) ([]version.Version, error) {
	versions := []version.Version{}
	matchRe := ""

	arch, err := util.GetArch()
	if err != nil {
		return nil, err
	}

	osType, err := util.GetOs()
	if err != nil {
		return nil, err
	}

	switch osType {
	case util.OsLinux:
		// Bundles without specifying the architecture are all x86_64.
		if arch == "x86_64" {
			matchRe = ".*>tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3}(?:-nogc64)?)(?:-linux-x86_64)?\\.tar\\.gz<.*"
		} else {
			matchRe = ".*>tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3}(?:-nogc64)?)(?:-linux-" + arch + ")\\.tar\\.gz<.*"
		}
	case util.OsMacos:
		// Bundles without specifying the architecture are all x86_64.
		if arch == "x86_64" {
			matchRe = ".*>tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3})-macos(?:x-x86_64)?\\.tar\\.gz<.*"
		} else {
			matchRe = ".*>tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3})(?:-macosx-" + arch + ")\\.tar\\.gz<.*"
		}
	}

	re := regexp.MustCompile(matchRe)
	parsedData := re.FindAllStringSubmatch(strings.TrimSpace(string(*data)), -1)
	if len(parsedData) == 0 {
		return nil, fmt.Errorf("no packages for this OS")
	}

	for _, entry := range parsedData {
		version, err := version.GetVersionDetails(entry[1])
		if err != nil {
			return nil, err
		}
		version.Tarball, err = getTarballName(entry[0])
		if err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}

	version.SortVersions(versions)

	return versions, nil
}

// getTarballURL returns a tarball address for the target operating system.
func getTarballURL() (string, error) {
	osType, err := util.GetOs()
	if err != nil {
		return "", err
	}

	switch osType {
	case util.OsLinux:
		return eeSourceLinux, nil
	case util.OsMacos:
		return eeSourceMacos, nil
	}

	return "", fmt.Errorf("this operating system is not supported")
}

func FetchVersionsLocal(files []string) ([]version.Version, error) {
	versions := []version.Version{}
	matchRe := ""

	arch, err := util.GetArch()
	if err != nil {
		return nil, err
	}

	osType, err := util.GetOs()
	if err != nil {
		return nil, err
	}

	switch osType {
	case util.OsLinux:
		// Bundles without specifying the architecture are all x86_64.
		if arch == "x86_64" {
			matchRe = "^tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3}(?:-nogc64)?)(?:-linux-x86_64)?\\.tar\\.gz$"
		} else {
			matchRe = "^tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3}(?:-nogc64)?)(?:-linux-" + arch + ")\\.tar\\.gz$"
		}
	case util.OsMacos:
		// Bundles without specifying the architecture are all x86_64.
		if arch == "x86_64" {
			matchRe = "^tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3})-macos(?:x-x86_64)?\\.tar\\.gz$"
		} else {
			matchRe = "^tarantool-enterprise-bundle-" +
				"(.*-g[a-f0-9]+-r[0-9]{3})(?:-macosx-" + arch + ")\\.tar\\.gz$"
		}
	}

	re := regexp.MustCompile(matchRe)

	for _, file := range files {
		parsedData := re.FindStringSubmatch(file)
		if len(parsedData) == 0 {
			continue
		}

		version, err := version.GetVersionDetails(parsedData[1])
		if err != nil {
			return nil, err
		}

		version.Tarball = file
		versions = append(versions, version)
	}

	version.SortVersions(versions)

	return versions, nil
}

// FetchVersions returns all available tarantool-ee versions.
// The result will be sorted in ascending order.
func FetchVersions(cliOpts *config.CliOpts) ([]version.Version, error) {
	credentials, err := getCreds(cliOpts)
	if err != nil {
		return nil, err
	}

	source, err := getTarballURL()
	if err != nil {
		return nil, err
	}

	client := http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, source, http.NoBody)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(credentials.username, credentials.password)

	res, err := client.Do(req)
	if err != nil {
		return nil, err
	} else if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request error: %s", http.StatusText(res.StatusCode))
	}

	defer res.Body.Close()

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}

	versions, err := getVersions(&resBody)
	if err != nil {
		return nil, err
	}

	return versions, nil
}

// GetTarantoolEE downloads given tarantool-ee bundle into directory.
func GetTarantoolEE(cliOpts *config.CliOpts, bundleName string, dst string) error {
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return fmt.Errorf("directory doesn't exist: %s", dst)
	}
	if !util.IsDir(dst) {
		return fmt.Errorf("incorrect path: %s", dst)
	}
	credentials, err := getCreds(cliOpts)
	if err != nil {
		return err
	}
	source, err := getTarballURL()
	if err != nil {
		return err
	}
	eeLink := source + bundleName
	client := http.Client{Timeout: 0}
	req, err := http.NewRequest(http.MethodGet, eeLink, http.NoBody)
	if err != nil {
		return err
	}

	req.SetBasicAuth(credentials.username, credentials.password)

	res, err := client.Do(req)
	if err != nil {
		return err
	} else if res.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP request error: %s", http.StatusText(res.StatusCode))
	}

	defer res.Body.Close()

	resBody, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	file, err := os.Create(filepath.Join(dst, bundleName))
	if err != nil {
		return err
	}
	file.Write(resBody)
	file.Close()

	return nil
}
