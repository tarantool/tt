package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	maxPackageIndexSize    = 8 << 20
	maxPackageSize         = 512 << 20
	packageDownloadTimeout = 5 * time.Minute
)

var (
	errPrebuiltPackageUnavailable = errors.New("prebuilt package unavailable")
	errPackageHTTPStatus          = errors.New("unexpected HTTP status")
	errPackageTooLarge            = errors.New("package response exceeds size limit")
	errPackageContents            = errors.New("invalid package contents")
)

// packageDownloader shares bounded HTTP delivery between the release repositories.
// Configuration belongs to the installer, so tests need no global HTTP configuration.
type packageDownloader struct {
	client    *http.Client
	fileLimit int64
	keepTemp  bool
}

type packageHTTPError struct {
	status  int
	address string
	program string
}

func (e *packageHTTPError) Error() string {
	return fmt.Sprintf("%s: download %s package %s: HTTP %d %s; "+
		"use --source-build to build from source", errPackageHTTPStatus,
		e.program, e.address, e.status, http.StatusText(e.status))
}

func (e *packageHTTPError) Unwrap() error {
	return errPackageHTTPStatus
}

func (d packageDownloader) get(address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("create package request: %w", err)
	}

	response, err := d.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request package repository: %w", err)
	}

	return response, nil
}

// download saves a package for extraction. A missing advertised file is a delivery error,
// rather than an unavailable release, and must not trigger a source build.
func (d packageDownloader) download(address, destination, pattern, program string) (
	string, error,
) {
	response, err := d.get(address)
	if err != nil {
		return "", fmt.Errorf("download %s package %s: %w", program, address, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return "", &packageHTTPError{
			status: response.StatusCode, address: address, program: program,
		}
	}

	if response.ContentLength > d.fileLimit {
		return "", fmt.Errorf("%w: %s package %s exceeds %d bytes",
			errPackageTooLarge, program, address, d.fileLimit)
	}

	archive, err := os.CreateTemp(destination, pattern)
	if err != nil {
		return "", fmt.Errorf("create temporary package: %w", err)
	}

	succeeded := false

	defer func() {
		if !succeeded && !d.keepTemp {
			_ = os.Remove(archive.Name())
		}
	}()

	written, copyErr := io.Copy(archive, io.LimitReader(response.Body, d.fileLimit+1))
	closeErr := archive.Close()

	if copyErr != nil {
		return "", fmt.Errorf("download %s package %s: %w", program, address, copyErr)
	}

	if closeErr != nil {
		return "", fmt.Errorf("close downloaded package: %w", closeErr)
	}

	if written > d.fileLimit {
		return "", fmt.Errorf("%w: %s package %s exceeds %d bytes",
			errPackageTooLarge, program, address, d.fileLimit)
	}

	succeeded = true

	return archive.Name(), nil
}
