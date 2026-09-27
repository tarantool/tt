package integrity_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

// testKeyBits is the size of the RSA keys the tests generate.
const testKeyBits = 2048

// The keys are generated once per test binary: generating them is the
// slowest step of the tests.
var (
	signingKeyOnce = sync.OnceValues(generateTestKey)
	foreignKeyOnce = sync.OnceValues(generateTestKey)
)

func generateTestKey() (*rsa.PrivateKey, error) {
	return rsa.GenerateKey(rand.Reader, testKeyBits)
}

// signingKey returns the key the tests sign with.
func signingKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := signingKeyOnce()
	require.NoError(t, err)

	return key
}

// foreignKey returns a key other than signingKey.
func foreignKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := foreignKeyOnce()
	require.NoError(t, err)

	return key
}

// writePrivateKey writes key to dir as a PKCS #1 PEM file that only its
// owner may read and write, and returns its path.
func writePrivateKey(t *testing.T, dir string, key *rsa.PrivateKey) string {
	t.Helper()

	path := filepath.Join(dir, "private.pem")
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	writeFile(t, path, string(pem.EncodeToMemory(block)), 0o600)

	return path
}

// writePublicKey writes the public half of key to dir as a PKIX PEM file
// and returns its path.
func writePublicKey(t *testing.T, dir string, key *rsa.PrivateKey) string {
	t.Helper()

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	path := filepath.Join(dir, "public.pem")
	writeFile(t, path, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
		0o644)

	return path
}

// writeFile writes content to path, creating its directory, and sets the
// mode of the file to mode whatever the umask.
func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), mode))
	require.NoError(t, os.Chmod(path, mode))
}

// symlink creates a symbolic link at path to target, creating the
// directory of the link.
func symlink(t *testing.T, target, path string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.Symlink(target, path))
}

// readFile returns the content of the file at path.
func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(data)
}

// sha256Hex returns the lowercase hex SHA-256 digest of content.
func sha256Hex(content string) string {
	sum := sha256.Sum256([]byte(content))

	return hex.EncodeToString(sum[:])
}

// hashesEntry is an entry of a hashes file as the tests read and write it.
type hashesEntry struct {
	Path   string
	SHA256 string
}

// entry returns the entry of a file at path with content.
func entry(path, content string) hashesEntry {
	return hashesEntry{Path: path, SHA256: sha256Hex(content)}
}

// hashesJSON returns the exact bytes of the hashes file that lists entries
// in their order: compact JSON, the path first in each entry.
func hashesJSON(entries ...hashesEntry) string {
	items := make([]string, 0, len(entries))
	for _, item := range entries {
		items = append(items, `{"path":"`+item.Path+`","sha256":"`+item.SHA256+`"}`)
	}

	return `{"files":[` + strings.Join(items, ",") + `]}`
}

// pssOptions are the RSASSA-PSS parameters of the format: a salt as long
// as the SHA-256 digest.
func pssOptions() *rsa.PSSOptions {
	return &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256}
}

// pssSign returns the RSASSA-PSS signature of the SHA-256 digest of data
// with a salt of saltLength bytes.
func pssSign(t *testing.T, key *rsa.PrivateKey, data []byte, saltLength int) []byte {
	t.Helper()

	digest := sha256.Sum256(data)

	signature, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:],
		&rsa.PSSOptions{SaltLength: saltLength, Hash: crypto.SHA256})
	require.NoError(t, err)

	return signature
}

// writeSignedHashes writes content as the hashes file at path and its
// signature by key, both made by the test and not by the provider.
func writeSignedHashes(t *testing.T, key *rsa.PrivateKey, path, content string) {
	t.Helper()

	writeFile(t, path, content, 0o644)
	writeFile(t, path+".sig", string(pssSign(t, key, []byte(content), sha256.Size)), 0o644)
}

// readSignedHashes verifies the signature of the hashes file at path with
// the public half of key using crypto/rsa, parses the file and returns its
// entries. Each entry must have exactly a path and a SHA-256 digest.
func readSignedHashes(t *testing.T, key *rsa.PrivateKey, path string) []hashesEntry {
	t.Helper()

	data := readFile(t, path)
	signature := readFile(t, path+".sig")
	digest := sha256.Sum256([]byte(data))

	require.NoError(t, rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, digest[:],
		[]byte(signature), pssOptions()), "signature of %s", path)

	var document struct {
		Files []map[string]string `json:"files"`
	}

	require.NoError(t, json.Unmarshal([]byte(data), &document))
	require.NotNil(t, document.Files)

	entries := make([]hashesEntry, 0, len(document.Files))
	for _, item := range document.Files {
		require.Len(t, item, 2, "entry %v", item)
		require.Contains(t, item, "path")
		require.Contains(t, item, "sha256")

		entries = append(entries, hashesEntry{Path: item["path"], SHA256: item["sha256"]})
	}

	return entries
}

// assertDigests checks the digest of every entry against the content of
// the file it names relative to dir.
func assertDigests(t *testing.T, dir string, entries []hashesEntry) {
	t.Helper()

	for _, item := range entries {
		content := readFile(t, filepath.Join(dir, filepath.FromSlash(item.Path)))
		assert.Equal(t, sha256Hex(content), item.SHA256, "digest of %s", item.Path)
	}
}

// newSigner returns the signer of the RSA provider for key.
func newSigner(t *testing.T, key *rsa.PrivateKey) integrity.Signer {
	t.Helper()

	signer, err := integrity.NewRSAProvider().NewSigner(writePrivateKey(t, t.TempDir(), key))
	require.NoError(t, err)

	return signer
}

// initializeCheck checks the environment in configDir with the public half
// of key.
func initializeCheck(
	t *testing.T, key *rsa.PrivateKey, configDir string,
) (integrity.IntegrityCtx, error) {
	t.Helper()

	return integrity.NewRSAProvider().InitializeIntegrityCheck(
		writePublicKey(t, t.TempDir(), key), configDir)
}

// readThrough reads the file at path through repository. The error is the
// one of Read; reading what Read returned must succeed.
func readThrough(t *testing.T, repository integrity.Repository, path string) (string, error) {
	t.Helper()

	reader, err := repository.Read(path)
	if err != nil {
		return "", err
	}

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	return string(data), nil
}
