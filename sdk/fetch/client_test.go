package fetch_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
)

// reply is what a test server answers with.
type reply struct {
	// status is the status code; 0 is 200 OK.
	status int
	// body is the body written.
	body []byte
	// length overrides the Content-Length header when it is not empty.
	length string
	// chunked sends the body without a Content-Length.
	chunked bool
}

// serve starts a server that answers every request with reply and returns
// its URL and the count of the requests it got.
func serve(t *testing.T, answer reply) (string, *atomic.Int32) {
	t.Helper()

	var hits atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter, _ *http.Request,
	) {
		hits.Add(1)

		if answer.length != "" {
			writer.Header().Set("Content-Length", answer.length)
		}

		if answer.status != 0 {
			writer.WriteHeader(answer.status)
		}

		if answer.chunked {
			flusher, ok := writer.(http.Flusher)
			assert.True(t, ok)
			flusher.Flush()
		}

		_, _ = writer.Write(answer.body)
	}))
	t.Cleanup(server.Close)

	return server.URL + "/file", &hits
}

func TestClientCopy(t *testing.T) {
	t.Parallel()

	url, _ := serve(t, reply{body: []byte("payload")})

	var buffer bytes.Buffer

	// The zero value has a client and a limit of its own.
	require.NoError(t, fetch.Client{}.Copy(t.Context(), url, &buffer))
	assert.Equal(t, "payload", buffer.String())

	data, err := fetch.Client{Limit: 7}.Bytes(t.Context(), url)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), data)
}

func TestClientLimit(t *testing.T) {
	t.Parallel()

	body := []byte("twelve bytes")

	for _, test := range []struct {
		name   string
		answer reply
	}{
		{name: "content length", answer: reply{body: body}},
		{name: "chunked body", answer: reply{body: body, chunked: true}},
		{name: "announced length", answer: reply{length: "1048576", body: nil}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			url, _ := serve(t, test.answer)
			client := fetch.Client{HTTP: http.DefaultClient, Limit: 11}

			err := client.Copy(t.Context(), url, &bytes.Buffer{})
			require.ErrorIs(t, err, fetch.ErrTooLarge)
			require.ErrorContains(t, err, "download "+url+": ")
			require.ErrorContains(t, err, "more than 11 bytes")

			_, err = client.Bytes(t.Context(), url)
			require.ErrorIs(t, err, fetch.ErrTooLarge)
		})
	}
}

func TestClientHTTPStatus(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNotFound, http.StatusTooManyRequests,
		http.StatusInternalServerError} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			t.Parallel()

			url, _ := serve(t, reply{status: status, body: []byte("error page")})

			_, err := fetch.Client{}.Bytes(t.Context(), url)
			require.ErrorIs(t, err, fetch.ErrHTTPStatus)

			var httpErr *fetch.HTTPError

			require.ErrorAs(t, err, &httpErr)
			assert.Equal(t, fetch.HTTPError{Status: status, URL: url}, *httpErr)
			require.EqualError(t, err, "download "+url+": HTTP "+strconv.Itoa(status)+" "+
				http.StatusText(status))

			// Get leaves the status to its caller.
			response, err := fetch.Client{}.Get(t.Context(), url)
			require.NoError(t, err)
			require.NoError(t, response.Body.Close())
			assert.Equal(t, status, response.StatusCode)
		})
	}
}

func TestClientRequestErrors(t *testing.T) {
	t.Parallel()

	_, err := fetch.Client{}.Bytes(t.Context(), "::not a url")
	require.ErrorContains(t, err, "download ::not a url: create request")

	url, _ := serve(t, reply{body: nil})
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()

	_, err = fetch.Client{}.Bytes(t.Context(), closed.URL)
	require.ErrorContains(t, err, "download "+closed.URL+": send request")
	require.NotErrorIs(t, err, fetch.ErrHTTPStatus)

	data, err := fetch.Client{}.Bytes(t.Context(), url)
	require.NoError(t, err)
	assert.Empty(t, data)
}
