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

// validateDestination checks if the destination path exists and is a directory.
func validateDestination(dst string) error {
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return fmt.Errorf("destination directory doesn't exist: %s", dst)
	}

	if !util.IsDir(dst) {
		return fmt.Errorf("destination path is not a directory: %s", dst)
	}

	return nil
}

// addSessionIdCookie adds a session ID cookie to the request if the token is not empty.
func addSessionIdCookie(req *http.Request, token string) {
	if token != "" {
		cookie := &http.Cookie{
			Name:  "sessionid",
			Value: token,
		}
		req.AddCookie(cookie)
	}
}

// createHttpClient configures and returns an HTTP client suitable for downloading bundles.
func createHttpClient(token string) *http.Client {
	client := &http.Client{
		Timeout: 0,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			req.Host = req.URL.Hostname()
			addSessionIdCookie(req, token)
			return nil
		},
	}
	return client
}

// createHttpRequest creates a new GET HTTP request with the necessary headers and cookies.
func createHttpRequest(bundleSource, token string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, bundleSource, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	addSessionIdCookie(req, token)
	req.Header.Set("User-Agent", "tt")
	return req, nil
}

// executeRequestAndCheckResponse executes the HTTP request and checks the response status.
// It returns the response body if the status is OK. Caller is responsible for closing it.
func executeRequestAndCheckResponse(client *http.Client, req *http.Request) (io.ReadCloser, error) {
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}

	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		return nil, fmt.Errorf("HTTP request error: %s", http.StatusText(res.StatusCode))
	}

	return res.Body, nil
}

// saveResponseBodyToFile creates the destination file and copies the response body content into it.
// It ensures the response body is closed and handles potential file writing errors.
func saveResponseBodyToFile(body io.ReadCloser, destFilePath string) (errRet error) {
	defer body.Close()

	file, err := os.Create(destFilePath)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", destFilePath, err)
	}

	defer func() {
		// Report close error only if no other error occurred during copy
		if closeErr := file.Close(); closeErr != nil && errRet == nil {
			errRet = fmt.Errorf("failed to close destination file %s: %w", destFilePath, closeErr)
		}
	}()

	_, err = io.Copy(file, body)
	if err != nil {
		file.Close()
		os.Remove(destFilePath)
		return fmt.Errorf("failed to write downloaded content to %s: %w", destFilePath, err)
	}

	return nil
}

// DownloadBundle downloads a bundle file from the given source URL into the destination directory.
// It handles potential redirects and uses the provided token for authentication via cookies.
func DownloadBundle(cliOpts *config.CliOpts, bundleName, bundleSource string,
	token string, dst string,
) error {
	if err := validateDestination(dst); err != nil {
		return err
	}

	client := createHttpClient(token)
	req, err := createHttpRequest(bundleSource, token)
	if err != nil {
		return err
	}

	responseBody, err := executeRequestAndCheckResponse(client, req)
	if err != nil {
		return err
	}

	destFilePath := filepath.Join(dst, bundleName)
	if err := saveResponseBodyToFile(responseBody, destFilePath); err != nil {
		return err
	}

	return nil
}
