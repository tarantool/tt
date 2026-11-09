package binary

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	// tntIoDomain is the domain the session cookie of the customer zone belongs to.
	tntIoDomain = "tarantool.io"
	// sessionCookie is the name of the session cookie of the customer zone.
	sessionCookie = "sessionid"

	// tntIoRequestTimeout bounds an API or catalog request, its response included.
	tntIoRequestTimeout = time.Minute
	// tntIoStallTimeout bounds the wait for the next bytes of a download. A bundle
	// takes hundreds of megabytes, so the download as a whole is not bounded.
	tntIoStallTimeout = time.Minute
	// maxTntIoResponseSize bounds a response read into memory: an API reply or a catalog.
	maxTntIoResponseSize = 16 << 20
	// maxTntIoErrorBody bounds the part of a failed response an error quotes.
	maxTntIoErrorBody = 4 << 10
	// maxTntIoRedirects is the limit of redirects http.Client follows by default.
	maxTntIoRedirects = 10
)

var (
	errTntIoRequestFailed    = errors.New("tarantool.io request failed")
	errTntIoResponseTooLarge = errors.New("tarantool.io response exceeds size limit")
	errTntIoStalled          = errors.New("tarantool.io download stalled")
	errTntIoTooManyRedirects = errors.New("too many redirects")
)

// TntIoDoer sends the requests of the customer zone of tarantool.io.
type TntIoDoer interface {
	// Do sends an HTTP request and returns Body data from HTTP response.
	Do(req *http.Request) ([]byte, error)

	// Download sends an HTTP request and streams the body of the response to dst.
	Download(req *http.Request, dst io.Writer) error

	// Token provides the session token after the Do method is executed.
	Token() string
}

// TntIoClient is the TntIoDoer of tt: it searches and downloads bundles alike.
type TntIoClient struct {
	client         *http.Client
	token          string
	requestTimeout time.Duration
	stallTimeout   time.Duration
}

// NewTntIoClient returns a client that sends the session token to tarantool.io only.
func NewTntIoClient(token string) *TntIoClient {
	var client *TntIoClient

	httpClient := &http.Client{
		// A redirect to another host, the storage of a bundle, takes no cookie: the
		// client drops the cookies of the request, and this adds the token back only
		// for tarantool.io.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxTntIoRedirects {
				return fmt.Errorf("%w: %d", errTntIoTooManyRedirects, len(via))
			}

			AddSessionCookie(req, client.token)

			return nil
		},
	}

	client = &TntIoClient{
		client:         httpClient,
		token:          token,
		requestTimeout: tntIoRequestTimeout,
		stallTimeout:   tntIoStallTimeout,
	}

	return client
}

// Do implements TntIoDoer. It also saves the session token from the response cookies.
func (c *TntIoClient) Do(req *http.Request) ([]byte, error) {
	ctx, cancel := context.WithTimeout(req.Context(), c.requestTimeout)
	defer cancel()

	resp, err := c.client.Do(req.WithContext(ctx))
	if err != nil {
		return nil, fmt.Errorf("failed to send API request: %w", err)
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return nil, responseError(resp)
	}

	for _, cookie := range resp.Cookies() {
		if cookie.Name == sessionCookie && cookie.Value != "" {
			c.token = cookie.Value
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTntIoResponseSize+1))
	if err != nil {
		return nil, fmt.Errorf("failed to read API response body: %w", err)
	}

	if len(body) > maxTntIoResponseSize {
		return nil, fmt.Errorf("%w: %s exceeds %d bytes",
			errTntIoResponseTooLarge, req.URL.Redacted(), maxTntIoResponseSize)
	}

	return body, nil
}

// Download implements TntIoDoer. It fails if no bytes arrive for the stall timeout.
func (c *TntIoClient) Download(req *http.Request, dst io.Writer) error {
	ctx, cancel := context.WithCancelCause(req.Context())
	defer cancel(nil)

	stall := time.AfterFunc(c.stallTimeout, func() {
		cancel(fmt.Errorf("%w: no data for %s", errTntIoStalled, c.stallTimeout))
	})
	defer stall.Stop()

	resp, err := c.client.Do(req.WithContext(ctx))
	if err != nil {
		return fmt.Errorf("HTTP request failed: %w", stallCause(ctx, err))
	}

	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return responseError(resp)
	}

	_, err = io.Copy(dst, &stallReader{reader: resp.Body, stall: stall, timeout: c.stallTimeout})
	if err != nil {
		return fmt.Errorf("failed to download %s: %w", req.URL.Redacted(), stallCause(ctx, err))
	}

	return nil
}

// Token implements TntIoDoer.
func (c *TntIoClient) Token() string {
	return c.token
}

// AddSessionCookie adds the session token to a request for tarantool.io over HTTPS.
// A request elsewhere, a redirect to a storage included, goes without it.
func AddSessionCookie(req *http.Request, token string) {
	if token == "" || !isTntIoURL(req) {
		return
	}

	_, err := req.Cookie(sessionCookie)
	if err == nil {
		// The client keeps the cookies of a redirect within the domain.
		return
	}

	req.AddCookie(&http.Cookie{ //nolint:gosec // request cookie: only name and value are sent
		Name:  sessionCookie,
		Value: token,
	})
}

func isTntIoURL(req *http.Request) bool {
	host := strings.ToLower(req.URL.Hostname())

	return req.URL.Scheme == "https" &&
		(host == tntIoDomain || strings.HasSuffix(host, "."+tntIoDomain))
}

// responseError quotes the start of a failed response, which explains the failure.
func responseError(resp *http.Response) error {
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTntIoErrorBody))
	requestErr := fmt.Errorf("%w with status %s: %q", errTntIoRequestFailed,
		resp.Status, string(body))

	if err != nil {
		return fmt.Errorf("%w; reading response body: %w", requestErr, err)
	}

	return requestErr
}

// stallCause names the stall timeout as the cause of a canceled request.
func stallCause(ctx context.Context, err error) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, errTntIoStalled) {
		return fmt.Errorf("%w: %w", cause, err)
	}

	return err
}

// stallReader restarts the stall timer each time bytes arrive.
type stallReader struct {
	reader  io.Reader
	stall   *time.Timer
	timeout time.Duration
}

func (r *stallReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.stall.Reset(r.timeout)
	}

	return n, err //nolint:wrapcheck // io.Copy expects io.EOF as is
}
