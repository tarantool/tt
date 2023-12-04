package install_ee

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// GetTarantoolEE downloads given tarantool-ee bundle into directory.
func GetTarantoolEE(cliOpts *config.CliOpts, bundleName, bundleSource string,
	token string, dst string) error {

	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return fmt.Errorf("directory doesn't exist: %s", dst)
	}
	if !util.IsDir(dst) {
		return fmt.Errorf("incorrect path: %s", dst)
	}

	client := http.Client{
		Timeout: 0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			// API uses signed 'host' header, it must be set explicitly,
			// because when redirecting it is empty.
			req.Host = req.URL.Hostname()

			return nil
		},
	}

	req, err := http.NewRequest(http.MethodGet, bundleSource, http.NoBody)
	if err != nil {
		return err
	}

	cookie := &http.Cookie{
		Name:  "sessionid",
		Value: token,
	}
	req.AddCookie(cookie)
	req.Header.Set("User-Agent", "tt")

	res, err := client.Do(req)
	if err != nil {
		return err
	} else if res.StatusCode != http.StatusOK {
		res.Body.Close()
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
