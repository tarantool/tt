// Package download is the bounded HTTP delivery of the release files the
// sources of the Community products read: the RPM repository of Tarantool
// and the releases of tt on GitHub. A request has a timeout, a body has a
// size limit, and a reply other than 200 OK is an error that names the file.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// IndexLimit bounds an index or a list of checksums.
	IndexLimit = 8 << 20
	// FileLimit bounds a downloaded package.
	FileLimit = 512 << 20
	// Timeout bounds a request, the body included.
	Timeout = 5 * time.Minute
)

var (
	// ErrHTTPStatus reports a reply other than 200 OK.
	ErrHTTPStatus = errors.New("unexpected HTTP status")
	// ErrTooLarge reports a body above its limit.
	ErrTooLarge = errors.New("package response exceeds size limit")
)

// Client downloads release files. Its configuration belongs to the source
// it serves, so tests need no global HTTP configuration.
type Client struct {
	// HTTP makes the requests.
	HTTP *http.Client
	// FileLimit bounds the body Copy writes.
	FileLimit int64
}

// New returns a client with the timeout and the size limit of tt.
func New() Client {
	return Client{HTTP: &http.Client{Timeout: Timeout}, FileLimit: FileLimit}
}

// HTTPError is a reply other than 200 OK to the download of a file.
type HTTPError struct {
	// Status is the status code of the reply.
	Status int
	// Address is the URL of the file.
	Address string
	// Program names the product of the file in the message.
	Program string
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("%s: download %s package %s: HTTP %d %s; "+
		"use --source-build to build from source", ErrHTTPStatus,
		e.Program, e.Address, e.Status, http.StatusText(e.Status))
}

// Unwrap returns ErrHTTPStatus.
func (e *HTTPError) Unwrap() error {
	return ErrHTTPStatus
}

// Get requests address. The caller closes the body.
func (c Client) Get(ctx context.Context, address string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create package request: %w", err)
	}

	response, err := c.HTTP.Do(request)
	if err != nil {
		return nil, fmt.Errorf("request package repository: %w", err)
	}

	return response, nil
}

// Copy writes the file at address to dst, at most FileLimit bytes of it. A
// reply other than 200 OK is an *HTTPError; a failure may leave a part of
// the file written.
func (c Client) Copy(ctx context.Context, address, program string, dst io.Writer) error {
	response, err := c.Get(ctx, address)
	if err != nil {
		return fmt.Errorf("download %s package %s: %w", program, address, err)
	}

	defer func() { _ = response.Body.Close() }()

	if response.StatusCode != http.StatusOK {
		return &HTTPError{Status: response.StatusCode, Address: address, Program: program}
	}

	if response.ContentLength > c.FileLimit {
		return fmt.Errorf("%w: %s package %s exceeds %d bytes",
			ErrTooLarge, program, address, c.FileLimit)
	}

	written, err := io.Copy(dst, io.LimitReader(response.Body, c.FileLimit+1))
	if err != nil {
		return fmt.Errorf("download %s package %s: %w", program, address, err)
	}

	if written > c.FileLimit {
		return fmt.Errorf("%w: %s package %s exceeds %d bytes",
			ErrTooLarge, program, address, c.FileLimit)
	}

	return nil
}
