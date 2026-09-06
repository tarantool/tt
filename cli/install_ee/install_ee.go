package install_ee

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"

	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
)

var (
	errDestinationDirectoryMissing    = errors.New("destination directory doesn't exist: ")
	errDestinationPathIsNotADirectory = errors.New("destination path is not a directory: ")
	errHTTPRequestError               = errors.New("HTTP request error: ")
	errTarantoolIODoerMissing         = errors.New("no tarantool.io doer was applied")
)

// httpDoer is a struct that implements the search.TntIoDoer interface using the http package.
type httpDoer struct {
	client *http.Client
	token  string
}

// NewTntIoDownloader configures and returns an HTTP client suitable for downloading bundles.
func NewTntIoDownloader(token string) *httpDoer {
	return &httpDoer{
		client: &http.Client{
			Timeout: 0,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				req.Host = req.URL.Hostname()
				addSessionIDCookie(req, token)
				return nil
			},
		},
		token: token,
	}
}

// Do implement TntIoDoer interface.
// It sends an HTTP request and returns Body data from HTTP response.
func (d *httpDoer) Do(req *http.Request) ([]byte, error) {
	res, err := d.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w%s", errHTTPRequestError, http.StatusText(res.StatusCode))
	}

	respBody, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read API response body: %w", err)
	}

	return respBody, nil
}

func (d *httpDoer) Token() string {
	return d.token
}

// validateDestination checks if the destination path exists and is a directory.
func validateDestination(dst string) error {
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		return fmt.Errorf("%w%s", errDestinationDirectoryMissing, dst)
	}

	if !util.IsDir(dst) {
		return fmt.Errorf("%w%s", errDestinationPathIsNotADirectory, dst)
	}

	return nil
}

// addSessionIdCookie adds a session ID cookie to the request if the token is not empty.
func addSessionIDCookie(req *http.Request, token string) {
	if token != "" {
		cookie := &http.Cookie{
			Name:  "sessionid",
			Value: token,
		}
		req.AddCookie(cookie)
	}
}

// createHttpRequest creates a new GET HTTP request with the necessary headers and cookies.
func createHTTPRequest(bundleSource, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, bundleSource, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	addSessionIDCookie(req, token)
	req.Header.Set("User-Agent", "tt")
	return req, nil
}

// saveResponseBodyToFile creates the destination file and copies the response body content into it.
func saveResponseBodyToFile(body []byte, destFilePath string) (errRet error) {
	file, err := os.Create(destFilePath)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", destFilePath, err)
	}

	defer func() {
		// Report close error only if no other error occurred during copy.
		if closeErr := file.Close(); closeErr != nil && errRet == nil {
			errRet = fmt.Errorf("failed to close destination file %s: %w", destFilePath, closeErr)
		}
	}()

	_, err = file.Write(body)
	if err != nil {
		file.Close()
		os.Remove(destFilePath)
		return fmt.Errorf("failed to write downloaded content to %s: %w", destFilePath, err)
	}

	return nil
}

// DownloadBundle downloads a bundle file from the given source URL into the destination directory.
// It handles potential redirects and uses the provided token for authentication via cookies.
func DownloadBundle(doer search.TntIoDoer, bundleName, bundleSource, dst string) error {
	if doer == nil || reflect.ValueOf(doer).IsNil() {
		return errTarantoolIODoerMissing
	}

	if err := validateDestination(dst); err != nil {
		return err
	}

	req, err := createHTTPRequest(bundleSource, doer.Token())
	if err != nil {
		return err
	}

	responseBody, err := doer.Do(req)
	if err != nil {
		return err
	}

	destFilePath := filepath.Join(dst, bundleName)
	if err := saveResponseBodyToFile(responseBody, destFilePath); err != nil {
		return err
	}

	return nil
}
