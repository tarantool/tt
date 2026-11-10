package download_test

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/fetch"
	"github.com/tarantool/tt/v3/internal/binary/download"
)

func TestCopyErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter, request *http.Request,
	) {
		if request.URL.Path == "/missing" {
			http.NotFound(writer, request)

			return
		}

		_, _ = writer.Write([]byte("twelve bytes"))
	}))
	t.Cleanup(server.Close)

	client := download.Client{HTTP: server.Client(), FileLimit: 12}

	var buffer bytes.Buffer

	require.NoError(t, client.Copy(t.Context(), server.URL+"/file", "tt", &buffer))
	assert.Equal(t, "twelve bytes", buffer.String())

	err := client.Copy(t.Context(), server.URL+"/missing", "tt", &buffer)

	var status *download.HTTPError

	require.ErrorAs(t, err, &status)
	assert.Equal(t, http.StatusNotFound, status.Status)
	require.ErrorIs(t, err, download.ErrHTTPStatus)
	require.ErrorIs(t, err, fetch.ErrHTTPStatus)
	require.EqualError(t, err, "unexpected HTTP status: download tt package "+server.URL+
		"/missing: HTTP 404 Not Found; use --source-build to build from source")

	client.FileLimit = 11
	err = client.Copy(t.Context(), server.URL+"/file", "Tarantool", &buffer)
	require.ErrorIs(t, err, download.ErrTooLarge)
	require.EqualError(t, err, "package response exceeds size limit: Tarantool package "+
		server.URL+"/file exceeds 11 bytes")
}
