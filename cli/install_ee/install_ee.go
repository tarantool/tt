package install_ee

import (
	"bufio"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
	"golang.org/x/term"
)

const eeSource string = "https://download.tarantool.io/enterprise/"

type userCredentials struct {
	username string
	password string
}

// getCredsInteractive Interactively prompts the user for credentials.
func getCredsInteractive() (userCredentials, error) {
	res := userCredentials{}
	reader := bufio.NewReader(os.Stdin)

	fmt.Printf("Enter username: ")
	resp, err := reader.ReadString('\n')
	if err != nil {
		return res, err
	}
	res.username = strings.TrimSpace(resp)

	fmt.Printf("Enter password: ")
	bytePass, err := term.ReadPassword(int(syscall.Stdin))
	if err != nil {
		return res, err
	}
	res.password = strings.TrimSpace(string(bytePass))

	return res, nil
}

// getCredsFromFile gets credentials from file.
func getCredsFromFile(path string) (userCredentials, error) {
	res := userCredentials{}
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return res, err
	}

	re := regexp.MustCompile("(?P<user>.*):(?P<pass>.*)")
	matches := util.FindNamedMatches(re, strings.TrimSpace(string(data)))

	if len(matches) == 0 {
		return res, fmt.Errorf("Corrupted credentials.")
	}

	res.username = matches["user"]
	res.password = matches["pass"]

	return res, nil
}

// getCreds gets credentials for tarantool-ee download.
func getCreds(cliOpts *config.CliOpts) (userCredentials, error) {
	if cliOpts.EE == nil || (cliOpts.EE != nil && cliOpts.EE.CredPath == "") {
		return getCredsInteractive()
	}

	return getCredsFromFile(cliOpts.EE.CredPath)
}

// getTarballName extracts tarball name from html data.
func getTarballName(data string) (string, error) {
	re := regexp.MustCompile(">(.*)<")

	parsedData := re.FindStringSubmatch(data)
	if len(parsedData) == 0 {
		return "", fmt.Errorf("Cannot parse tarball name")
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

	// Bundles without specifying the architecture are all x86_64.
	if arch == "x86_64" {
		matchRe = ".*>tarantool-enterprise-bundle-" +
			"(.*-g[a-f0-9]+-r[0-9]{3})(?:-linux-x86_64)?\\.tar\\.gz<.*"
	} else {
		matchRe = ".*>tarantool-enterprise-bundle-" +
			"(.*-g[a-f0-9]+-r[0-9]{3})(?:-linux-" + arch + ")\\.tar\\.gz<.*"
	}

	re := regexp.MustCompile(matchRe)
	parsedData := re.FindAllStringSubmatch(strings.TrimSpace(string(*data)), -1)
	if len(parsedData) == 0 {
		return nil, fmt.Errorf("Cannot parse versions")
	}

	for _, entry := range parsedData {
		version, err := version.GetVersionDetails(entry[1])
		if err != nil {
			return nil, err
		}
		version.Str = entry[1]
		version.Tarball, err = getTarballName(entry[0])
		if err != nil {
			return nil, err
		}
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

	client := http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodGet, eeSource, http.NoBody)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(credentials.username, credentials.password)

	res, err := client.Do(req)
	if err != nil {
		return nil, err
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
