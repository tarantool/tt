package binary

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const tntIoTestToken = "test-token"

type tntIoTestTransport func(*http.Request) (*http.Response, error)

func (transport tntIoTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// tntIoTestClient is a client whose requests the handler answers in memory.
func tntIoTestClient(token string, handler tntIoTestTransport) *TntIoClient {
	client := NewTntIoClient(token)

	client.client.Transport = handler

	return client
}

func tntIoTestResponse(request *http.Request, status int, body io.Reader) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     make(http.Header),
		Body:       io.NopCloser(body),
		Request:    request,
	}
}

func tntIoTestRequest(t *testing.T, method, address string) *http.Request {
	t.Helper()

	request, err := http.NewRequestWithContext(t.Context(), method, address, nil)
	require.NoError(t, err)

	return request
}

// blockingBody returns its chunk, then blocks until the request is canceled.
type blockingBody struct {
	ctx   context.Context //nolint:containedctx // the body ends with its request
	chunk []byte
}

func (b *blockingBody) Read(buffer []byte) (int, error) {
	if len(b.chunk) > 0 {
		read := copy(buffer, b.chunk)

		b.chunk = b.chunk[read:]

		return read, nil
	}

	<-b.ctx.Done()

	return 0, b.ctx.Err()
}

// steadyBody returns a byte each interval.
type steadyBody struct {
	bytes    int
	interval time.Duration
}

func (b *steadyBody) Read(buffer []byte) (int, error) {
	if b.bytes == 0 {
		return 0, io.EOF
	}

	time.Sleep(b.interval)

	b.bytes--

	buffer[0] = 'x'

	return 1, nil
}

func TestTntIoClientRequestError(t *testing.T) {
	for _, truncated := range []bool{false, true} {
		name := "complete body"
		if truncated {
			name = "truncated body"
		}

		t.Run(name, func(t *testing.T) {
			client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
				var body io.Reader = strings.NewReader("Invalid request data, usage:\n" +
					`{"username": username, "password": password, "query": query}.`)

				if truncated {
					body = io.MultiReader(body, iotest.ErrReader(io.ErrUnexpectedEOF))
				}

				response := tntIoTestResponse(request, http.StatusBadRequest, body)

				response.Status = "400 Bad Request"

				return response, nil
			})

			_, err := client.Do(tntIoTestRequest(t, http.MethodPost, "https://api.test"))
			require.ErrorIs(t, err, errTntIoRequestFailed)
			require.ErrorContains(t, err, "with status 400 Bad Request")
			require.ErrorContains(t, err, "Invalid request data, usage:")
			require.NotContains(t, err.Error(), "%!w")

			if truncated {
				require.ErrorIs(t, err, io.ErrUnexpectedEOF)
			}
		})
	}
}

func TestTntIoClientRequestErrorQuotesStartOfBody(t *testing.T) {
	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		page := strings.Repeat("<p>Not found</p>", maxTntIoErrorBody)

		return tntIoTestResponse(request, http.StatusNotFound, strings.NewReader(page)), nil
	})

	err := client.Download(tntIoTestRequest(t, http.MethodGet, "https://www.tarantool.io/x"),
		io.Discard)
	require.ErrorIs(t, err, errTntIoRequestFailed)
	assert.Less(t, len(err.Error()), 2*maxTntIoErrorBody)
}

func TestTntIoClientSavesSessionToken(t *testing.T) {
	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		response := tntIoTestResponse(request, http.StatusOK, strings.NewReader("{}"))
		response.Header.Add("Set-Cookie", "csrftoken=other")
		response.Header.Add("Set-Cookie", "sessionid="+tntIoTestToken)

		return response, nil
	})

	body, err := client.Do(tntIoTestRequest(t, http.MethodPost, "https://www.tarantool.io/api"))
	require.NoError(t, err)
	assert.Equal(t, "{}", string(body))
	assert.Equal(t, tntIoTestToken, client.Token())
}

func TestTntIoClientBoundsResponse(t *testing.T) {
	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		return tntIoTestResponse(request, http.StatusOK,
			bytes.NewReader(make([]byte, maxTntIoResponseSize+1))), nil
	})

	_, err := client.Do(tntIoTestRequest(t, http.MethodGet, "https://www.tarantool.io/catalog"))
	require.ErrorIs(t, err, errTntIoResponseTooLarge)
}

func TestTntIoClientRequestTimeout(t *testing.T) {
	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()

		return nil, request.Context().Err()
	})

	client.requestTimeout = 10 * time.Millisecond

	_, err := client.Do(tntIoTestRequest(t, http.MethodPost, "https://www.tarantool.io/api"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestTntIoClientSendsTokenToTarantoolIoOnly(t *testing.T) {
	cookies := make(map[string][]string)

	client := tntIoTestClient(tntIoTestToken, func(request *http.Request) (*http.Response, error) {
		for _, cookie := range request.Cookies() {
			cookies[request.URL.Host] = append(cookies[request.URL.Host], cookie.Value)
		}

		response := tntIoTestResponse(request, http.StatusFound, http.NoBody)

		switch request.URL.Host {
		case "www.tarantool.io":
			response.Header.Set("Location", "https://download.tarantool.io/bundle.tar.gz")
		case "download.tarantool.io":
			response.Header.Set("Location", "https://storage.test/bundle.tar.gz")
		default:
			response = tntIoTestResponse(request, http.StatusOK, strings.NewReader("bundle"))
		}

		return response, nil
	})

	request := tntIoTestRequest(t, http.MethodGet, "https://www.tarantool.io/bundle.tar.gz")
	AddSessionCookie(request, client.Token())

	var downloaded bytes.Buffer

	require.NoError(t, client.Download(request, &downloaded))
	assert.Equal(t, "bundle", downloaded.String())
	assert.Equal(t, map[string][]string{
		"www.tarantool.io":      {tntIoTestToken},
		"download.tarantool.io": {tntIoTestToken},
	}, cookies)
}

func TestTntIoClientDownloadStreams(t *testing.T) {
	content := bytes.Repeat([]byte("bundle"), 1<<20)

	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		return tntIoTestResponse(request, http.StatusOK,
			iotest.HalfReader(bytes.NewReader(content))), nil
	})

	var downloaded bytes.Buffer

	err := client.Download(tntIoTestRequest(t, http.MethodGet, "https://www.tarantool.io/b"),
		&downloaded)
	require.NoError(t, err)
	assert.Equal(t, content, downloaded.Bytes())
}

func TestTntIoClientDownloadStalls(t *testing.T) {
	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		return tntIoTestResponse(request, http.StatusOK,
			&blockingBody{ctx: request.Context(), chunk: []byte("part")}), nil
	})

	client.stallTimeout = 20 * time.Millisecond

	var downloaded bytes.Buffer

	err := client.Download(tntIoTestRequest(t, http.MethodGet, "https://www.tarantool.io/b"),
		&downloaded)
	require.ErrorIs(t, err, errTntIoStalled)
	assert.Equal(t, "part", downloaded.String())
}

func TestTntIoClientDownloadStallsBeforeResponse(t *testing.T) {
	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()

		return nil, request.Context().Err()
	})

	client.stallTimeout = 20 * time.Millisecond

	err := client.Download(tntIoTestRequest(t, http.MethodGet, "https://www.tarantool.io/b"),
		io.Discard)
	require.ErrorIs(t, err, errTntIoStalled)
}

func TestTntIoClientDownloadOutlastsStallTimeout(t *testing.T) {
	// The download takes longer than the stall timeout, but its bytes keep arriving.
	client := tntIoTestClient("", func(request *http.Request) (*http.Response, error) {
		return tntIoTestResponse(request, http.StatusOK,
			&steadyBody{bytes: 30, interval: 10 * time.Millisecond}), nil
	})

	client.stallTimeout = 200 * time.Millisecond

	var downloaded bytes.Buffer

	err := client.Download(tntIoTestRequest(t, http.MethodGet, "https://www.tarantool.io/b"),
		&downloaded)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("x", 30), downloaded.String())
}

func TestAddSessionCookie(t *testing.T) {
	for _, test := range []struct {
		address string
		token   string
		sent    bool
	}{
		{"https://www.tarantool.io/en/accounts/customer_zone/packages", tntIoTestToken, true},
		{"https://tarantool.io/packages", tntIoTestToken, true},
		{"https://DOWNLOAD.Tarantool.IO/packages", tntIoTestToken, true},
		{"https://www.tarantool.io:443/packages", tntIoTestToken, true},
		{"https://www.tarantool.io/packages", "", false},
		{"http://www.tarantool.io/packages", tntIoTestToken, false},
		{"https://eviltarantool.io/packages", tntIoTestToken, false},
		{"https://tarantool.io.storage.test/packages", tntIoTestToken, false},
		{"https://storage.test/tarantool.io/packages", tntIoTestToken, false},
	} {
		t.Run(test.address, func(t *testing.T) {
			request := tntIoTestRequest(t, http.MethodGet, test.address)

			AddSessionCookie(request, test.token)
			AddSessionCookie(request, test.token)

			if !test.sent {
				assert.Empty(t, request.Cookies())

				return
			}

			require.Len(t, request.Cookies(), 1)
			assert.Equal(t, sessionCookie, request.Cookies()[0].Name)
			assert.Equal(t, test.token, request.Cookies()[0].Value)
		})
	}
}
