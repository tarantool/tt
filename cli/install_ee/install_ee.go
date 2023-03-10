package install_ee

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

const (
	EESource = "https://download.tarantool.io/"
)

// GetTarantoolEE downloads given tarantool-ee bundle into directory.
func GetTarantoolEE(cliOpts *config.CliOpts, bundleName, bundleSource string, dst string) error {
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return fmt.Errorf("directory doesn't exist: %s", dst)
	}
	if !util.IsDir(dst) {
		return fmt.Errorf("incorrect path: %s", dst)
	}
	credentials, err := GetCreds(cliOpts)
	if err != nil {
		return err
	}

	source, err := url.Parse(EESource)
	if err != nil {
		return err
	}

	source.Path = filepath.Join(bundleSource, bundleName)
	client := http.Client{Timeout: 0}
	req, err := http.NewRequest(http.MethodGet, source.String(), http.NoBody)
	if err != nil {
		return err
	}

	req.SetBasicAuth(credentials.Username, credentials.Password)

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
