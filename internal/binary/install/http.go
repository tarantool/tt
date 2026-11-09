package install

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"

	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errDestinationDirectoryMissing    = errors.New("destination directory doesn't exist: ")
	errDestinationPathIsNotADirectory = errors.New("destination path is not a directory: ")
)

// validateDestination checks if the destination path exists and is a directory.
func validateDestination(dst string) error {
	_, err := os.Stat(dst)
	if os.IsNotExist(err) {
		return fmt.Errorf("%w%s", errDestinationDirectoryMissing, dst)
	}

	if !binary.IsDir(dst) {
		return fmt.Errorf("%w%s", errDestinationPathIsNotADirectory, dst)
	}

	return nil
}

// createHttpRequest creates a new GET HTTP request with the necessary headers and cookies.
func createHTTPRequest(bundleSource, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, bundleSource, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	binary.AddSessionCookie(req, token)
	req.Header.Set("User-Agent", "tt")

	return req, nil
}

// downloadToFile streams the response to the request into the destination file.
// A failed download leaves no file behind.
func downloadToFile(doer binary.TntIoDoer, req *http.Request, destFilePath string) error {
	file, err := os.Create(destFilePath)
	if err != nil {
		return fmt.Errorf("failed to create destination file %s: %w", destFilePath, err)
	}

	err = doer.Download(req, file)

	closeErr := file.Close()
	if err == nil && closeErr != nil {
		err = fmt.Errorf("failed to close destination file %s: %w", destFilePath, closeErr)
	}

	if err != nil {
		_ = os.Remove(destFilePath)

		return err
	}

	return nil
}

// DownloadBundle downloads a bundle file from the given source URL into the destination directory.
// It handles potential redirects and uses the provided token for authentication via cookies.
func DownloadBundle(doer binary.TntIoDoer, bundleName, bundleSource, dst string) error {
	if doer == nil || reflect.ValueOf(doer).IsNil() {
		return binary.ErrTarantoolIODoerMissing
	}

	err := validateDestination(dst)
	if err != nil {
		return err
	}

	req, err := createHTTPRequest(bundleSource, doer.Token())
	if err != nil {
		return err
	}

	return downloadToFile(doer, req, filepath.Join(dst, bundleName))
}
