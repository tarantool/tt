package fetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// DefaultLimit bounds a body when a Client sets no limit, and a file
	// UnpackFile takes when its caller sets none: 1 GiB, room for a
	// toolchain of a few hundred megabytes.
	DefaultLimit = 1 << 30
	// DefaultTimeout bounds a request, the body included, when a Client
	// brings no HTTP client of its own.
	DefaultTimeout = 10 * time.Minute
)

var (
	// ErrHTTPStatus is wrapped by a reply other than 200 OK, an *HTTPError.
	ErrHTTPStatus = errors.New("unexpected HTTP status")
	// ErrTooLarge reports a body or a file above its size limit.
	ErrTooLarge = errors.New("size limit exceeded")
	// ErrChecksum reports a download whose SHA-256 is not the one asked for.
	ErrChecksum = errors.New("checksum mismatch")
	// ErrNotInArchive reports an archive without the file asked for.
	ErrNotInArchive = errors.New("no such file in the archive")
)

// HTTPError is a reply other than 200 OK to a download.
type HTTPError struct {
	// Status is the status code of the reply.
	Status int
	// URL is the URL requested.
	URL string
}

// Error implements error.
func (e *HTTPError) Error() string {
	return fmt.Sprintf("download %s: HTTP %d %s", e.URL, e.Status, http.StatusText(e.Status))
}

// Unwrap returns ErrHTTPStatus.
func (*HTTPError) Unwrap() error {
	return ErrHTTPStatus
}

// Client makes the requests of downloads. The zero value is ready to use,
// with DefaultTimeout and DefaultLimit.
type Client struct {
	// HTTP makes the requests; nil is a client with DefaultTimeout. A client
	// given here brings its own timeout.
	HTTP *http.Client
	// Limit bounds the body Copy and Bytes read; 0 or less is DefaultLimit.
	Limit int64
}

// Get requests url and returns the reply, whatever its status. The caller
// closes the body and bounds what it reads of it.
func (c Client) Get(ctx context.Context, url string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: DefaultTimeout}
	}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	return response, nil
}

// Copy writes the body of the reply to url to dst, at most Limit bytes of
// it. A reply other than 200 OK is an *HTTPError, a larger body wraps
// ErrTooLarge, and every error names url. A failure may leave a part of the
// body written.
func (c Client) Copy(ctx context.Context, url string, dst io.Writer) error {
	response, err := c.open(ctx, url)
	if err != nil {
		return err
	}

	defer func() { _ = response.Body.Close() }()

	limit := c.limit()

	written, err := io.Copy(dst, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("download %s: %w", url, err)
	}

	if written > limit {
		return tooLarge(url, limit)
	}

	return nil
}

// Bytes returns the body of the reply to url, at most Limit bytes of it,
// with the errors of Copy.
func (c Client) Bytes(ctx context.Context, url string) ([]byte, error) {
	response, err := c.open(ctx, url)
	if err != nil {
		return nil, err
	}

	defer func() { _ = response.Body.Close() }()

	limit := c.limit()

	data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}

	if int64(len(data)) > limit {
		return nil, tooLarge(url, limit)
	}

	return data, nil
}

// open requests url and checks the reply before its body is read: 200 OK,
// and the length of the body, when the server sends it, within the limit.
// The caller closes the body.
func (c Client) open(ctx context.Context, url string) (*http.Response, error) {
	response, err := c.Get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", url, err)
	}

	if response.StatusCode != http.StatusOK {
		_ = response.Body.Close()

		return nil, &HTTPError{Status: response.StatusCode, URL: url}
	}

	if response.ContentLength > c.limit() {
		_ = response.Body.Close()

		return nil, tooLarge(url, c.limit())
	}

	return response, nil
}

// limit returns the bound of a body.
func (c Client) limit() int64 {
	if c.Limit <= 0 {
		return DefaultLimit
	}

	return c.Limit
}

// tooLarge reports a body of url above limit.
func tooLarge(url string, limit int64) error {
	return fmt.Errorf("download %s: %w: more than %d bytes", url, ErrTooLarge, limit)
}
