package cluster

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// errNotChecked is what checkedRepository refuses a file it has not checked
// with.
var errNotChecked = errors.New("not checked")

// checkedRepository knows files by path, with the contents it checked them
// with, as a repository does whose files were rewritten after the check.
type checkedRepository map[string]string

// Read opens the file at path as it is on disk now, as the RSA repository
// returns the file it checked.
func (r checkedRepository) Read(path string) (io.ReadCloser, error) {
	if _, ok := r[path]; !ok {
		return nil, errNotChecked
	}

	return os.Open(path)
}

// ReadFile returns the contents path was checked with.
func (r checkedRepository) ReadFile(path string) ([]byte, error) {
	content, ok := r[path]
	if !ok {
		return nil, errNotChecked
	}

	return []byte(content), nil
}

// ValidateAll validates nothing.
func (checkedRepository) ValidateAll() error { return nil }

// checkedContext returns the integrity context of the RSA provider, set for
// the test, with a public key, so that the checks are configured, and
// repository as its repository.
func checkedContext(t *testing.T, repository integrity.Repository) integrity.IntegrityCtx {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	publicPath := filepath.Join(t.TempDir(), "public.pem")
	require.NoError(t, os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{
		Type: "PUBLIC KEY", Bytes: public,
	}), 0o644))

	integrityprovider.Set(integrity.NewRSAProvider())
	t.Cleanup(func() { integrityprovider.Set(nil) })

	integ, err := integrityprovider.Get().InitializeIntegrityCheck(publicPath)
	require.NoError(t, err)

	integ.Repository = repository

	return integ
}

// TestNewCollectorFactoryReadsCheckedBytes checks that a file collector of
// an integrity-aware factory collects the bytes the repository checked, not
// the file rewritten after the check, and that it refuses a file the
// repository refuses.
func TestNewCollectorFactoryReadsCheckedBytes(t *testing.T) {
	dir := t.TempDir()
	config := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(config, []byte("rewritten: true\n"), 0o644))

	factory, err := NewCollectorFactory(
		checkedContext(t, checkedRepository{config: "checked: true\n"}))
	require.NoError(t, err)

	data, err := factory.NewFileCollector(config).Collect()
	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, "checked: true\n", string(data[0].Value))

	_, err = factory.NewFileCollector(filepath.Join(dir, "other.yaml")).Collect()
	require.ErrorIs(t, err, errNotChecked)
}
