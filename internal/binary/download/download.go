// Package download is the bounded HTTP delivery of the release files the
// sources of the Community products read: the RPM repository of Tarantool
// and the releases of tt on GitHub. A request has a timeout, a body has a
// size limit, and a reply other than 200 OK is an error that names the file.
//
// The downloads are those of sdk/fetch; the package holds what is tt's own
// about them: the limits, and the errors that name the product and offer a
// build from source.
package download

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/tarantool/tt/sdk/fetch"
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
	// ErrHTTPStatus reports a reply other than 200 OK. It is
	// fetch.ErrHTTPStatus.
	ErrHTTPStatus = fetch.ErrHTTPStatus
	// ErrTooLarge reports a body above its limit.
	ErrTooLarge = errors.New("package response exceeds size limit")
)

// Client downloads release files. Its configuration belongs to the source
// it serves, so tests need no global HTTP configuration.
type Client struct {
	// HTTP makes the requests; nil is a client with fetch.DefaultTimeout.
	HTTP *http.Client
	// FileLimit bounds the body Copy writes; 0 is fetch.DefaultLimit.
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
func (*HTTPError) Unwrap() error {
	return ErrHTTPStatus
}

// Get requests address. The caller closes the body.
func (c Client) Get(ctx context.Context, address string) (*http.Response, error) {
	return c.fetchClient().Get(ctx, address)
}

// Copy writes the file at address to dst, at most FileLimit bytes of it. A
// reply other than 200 OK is an *HTTPError; a failure may leave a part of
// the file written.
func (c Client) Copy(ctx context.Context, address, program string, dst io.Writer) error {
	client := c.fetchClient()

	err := client.Copy(ctx, address, dst)

	var status *fetch.HTTPError

	switch {
	case errors.As(err, &status):
		return &HTTPError{Status: status.Status, Address: address, Program: program}
	case errors.Is(err, fetch.ErrTooLarge):
		return fmt.Errorf("%w: %s package %s exceeds %d bytes",
			ErrTooLarge, program, address, client.Limit)
	default:
		return err
	}
}

// fetchClient returns the client of sdk/fetch the downloads go through.
func (c Client) fetchClient() fetch.Client {
	limit := c.FileLimit
	if limit <= 0 {
		limit = fetch.DefaultLimit
	}

	return fetch.Client{HTTP: c.HTTP, Limit: limit}
}
