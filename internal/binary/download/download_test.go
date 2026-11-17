package download_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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

	_, ok := download.AsCertificateError(err)
	assert.False(t, ok, "a body too large is no certificate failure")
}

// failedGet returns the error the request of address with client fails with.
func failedGet(t *testing.T, client download.Client, address string) error {
	t.Helper()

	response, err := client.Get(t.Context(), address)
	if err == nil {
		_ = response.Body.Close()
	}

	require.Error(t, err)

	return err
}

// TestAsCertificateError tells a TLS certificate tt cannot verify, through
// the errors that wrap it, by the host of the request: one of an authority
// the system does not trust, with the advice to install the CA
// certificates, and one of another host, without it.
func TestAsCertificateError(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	t.Cleanup(server.Close)

	// A system without its CA certificates trusts no authority.
	untrusted := download.Client{HTTP: &http.Client{Transport: &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool(), MinVersion: tls.VersionTLS12},
	}}}

	requestErr := failedGet(t, untrusted, server.URL+"/index")
	wrapped := fmt.Errorf("list tarantool archives in rws: %w", requestErr)

	certificate, ok := download.AsCertificateError(wrapped)
	require.True(t, ok, "%v", wrapped)
	assert.Equal(t, "127.0.0.1", certificate.Host)
	require.EqualError(t, certificate, "the TLS certificate of 127.0.0.1 cannot be verified: "+
		"x509: certificate signed by unknown authority; install the CA certificates of the "+
		"system (ca-certificates), or set SSL_CERT_FILE or SSL_CERT_DIR to a CA bundle")
	require.ErrorIs(t, certificate, requestErr)

	var authority x509.UnknownAuthorityError

	require.ErrorAs(t, certificate, &authority)

	again, ok := download.AsCertificateError(fmt.Errorf("again: %w", certificate))
	require.True(t, ok)
	assert.Same(t, certificate, again)

	// The test certificate is for 127.0.0.1 and example.com, not localhost.
	trusted := download.Client{HTTP: server.Client()}
	address := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)

	requestErr = failedGet(t, trusted, address)

	certificate, ok = download.AsCertificateError(requestErr)
	require.True(t, ok, "%v", requestErr)
	assert.Equal(t, "localhost", certificate.Host)
	require.ErrorContains(t, certificate, "the TLS certificate of localhost cannot be verified: "+
		"x509: certificate is valid for")
	assert.NotContains(t, certificate.Error(), "ca-certificates",
		"the CA certificates do not make a certificate of another host valid")

	server.Close()

	requestErr = failedGet(t, trusted, server.URL)

	_, ok = download.AsCertificateError(requestErr)
	assert.False(t, ok, "a refused connection is no certificate failure: %v", requestErr)
}
