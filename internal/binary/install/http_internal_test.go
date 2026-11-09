package install

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
)

var errSimulatedNetworkError = errors.New("simulated network error")

type prebuiltTestTransport func(*http.Request) (*http.Response, error)

func (transport prebuiltTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

// fetchTestSource serves one archive: its body, then its error.
type fetchTestSource struct {
	body []byte
	err  error
	// fetched is the artifact Fetch was given.
	fetched bundle.Artifact
}

func (*fetchTestSource) Name() string { return "test-source" }

func (*fetchTestSource) Products() []string { return []string{"tcm"} }

func (*fetchTestSource) List(context.Context, bundle.Query) ([]bundle.Artifact, error) {
	return nil, nil
}

func (s *fetchTestSource) Fetch(_ context.Context, artifact bundle.Artifact,
	dst io.Writer,
) error {
	s.fetched = artifact

	_, err := dst.Write(s.body)
	if err != nil {
		return err
	}

	return s.err
}

func TestFetchArtifact(t *testing.T) {
	body := []byte("bundle content")
	digest := sha256.Sum256(body)

	tests := map[string]struct {
		source   *fetchTestSource
		sha256   string
		name     string
		conflict bool
		errIs    error
		errMsg   string
	}{
		"without a digest": {
			source: &fetchTestSource{body: body},
		},
		"with its digest": {
			source: &fetchTestSource{body: body},
			sha256: hex.EncodeToString(digest[:]),
		},
		"with its digest in upper case": {
			source: &fetchTestSource{body: body},
			sha256: strings.ToUpper(hex.EncodeToString(digest[:])),
		},
		"with another digest": {
			source: &fetchTestSource{body: body},
			sha256: strings.Repeat("0", sha256.Size*2),
			errIs:  errArtifactChecksum,
			errMsg: "test-source lists " + strings.Repeat("0", sha256.Size*2),
		},
		"a failed fetch": {
			source: &fetchTestSource{body: []byte("partial bundle"), err: errSimulatedNetworkError},
			errIs:  errSimulatedNetworkError,
		},
		"a destination taken by a file": {
			source:   &fetchTestSource{body: body},
			name:     "conflict_dir/bundle.tar.gz",
			conflict: true,
			errMsg:   "failed to create destination file",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()

			fileName := test.name
			if fileName == "" {
				fileName = "bundle.tar.gz"
			}

			if test.conflict {
				require.NoError(t, os.WriteFile(filepath.Join(dir, "conflict_dir"), nil, 0o644))
			}

			artifact := bundle.Artifact{
				Version: "1.3.1-0-g074b5ffa", Name: "bundle.tar.gz", SHA256: test.sha256,
				Locator: "test:bundle",
			}
			destination := filepath.Join(dir, fileName)

			err := fetchArtifact(t.Context(), test.source, artifact, destination)
			if test.errIs == nil && test.errMsg == "" {
				require.NoError(t, err)
				assert.Equal(t, artifact, test.source.fetched)

				content, err := os.ReadFile(destination)
				require.NoError(t, err)
				assert.Equal(t, body, content)

				return
			}

			if test.errIs != nil {
				require.ErrorIs(t, err, test.errIs)
			}

			require.ErrorContains(t, err, test.errMsg)
			require.NoFileExists(t, destination, "a failed fetch must leave no file")
		})
	}
}
