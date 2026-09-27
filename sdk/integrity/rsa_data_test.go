package integrity_test

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

// verificationError is the text of the error crypto/rsa returns for a
// signature that does not verify.
const verificationError = "crypto/rsa: verification error"

// checkContext returns a context of the RSA provider that checks with the
// public half of key. Its environment is unsigned and lists no files.
func checkContext(t *testing.T, key *rsa.PrivateKey) integrity.IntegrityCtx {
	t.Helper()

	ctx, err := initializeCheck(t, key, t.TempDir())
	require.NoError(t, err)

	return ctx
}

// checkFunction returns the check function of the RSA provider for the
// public half of key.
func checkFunction(
	t *testing.T, key *rsa.PrivateKey,
) func(data []byte, hashes map[string][]byte, sign []byte) error {
	t.Helper()

	check, err := integrity.NewRSAProvider().GetCheckFunction(checkContext(t, key))
	require.NoError(t, err)

	return check
}

// signFunction returns the sign function of the RSA provider for key.
func signFunction(
	t *testing.T, key *rsa.PrivateKey,
) func(data []byte) (map[string][]byte, []byte, error) {
	t.Helper()

	sign, err := integrity.NewRSAProvider().GetSignFunction(
		writePrivateKey(t, t.TempDir(), key))
	require.NoError(t, err)

	return sign
}

func TestRSASignFunctionFormat(t *testing.T) {
	key := signingKey(t)
	data := []byte("cluster configuration")

	hashes, signature, err := signFunction(t, key)(data)
	require.NoError(t, err)

	// The digest is lowercase hex text under the name of its algorithm.
	assert.Equal(t, map[string][]byte{"sha256": []byte(sha256Hex(string(data)))}, hashes)

	// The signature is lowercase hex text of RSASSA-PSS over the SHA-256
	// digest with a 32-byte salt.
	assert.Equal(t, bytes.ToLower(signature), signature)

	raw, err := hex.DecodeString(string(signature))
	require.NoError(t, err)

	digest := sha256.Sum256(data)
	require.NoError(t, rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, digest[:], raw, pssOptions()))
}

func TestRSACheckFunction(t *testing.T) {
	key := signingKey(t)
	sign := signFunction(t, key)
	check := checkFunction(t, key)

	data := []byte("cluster configuration")
	other := []byte("another configuration")

	hashes, signature, err := sign(data)
	require.NoError(t, err)

	otherHashes, otherSignature, err := sign(other)
	require.NoError(t, err)

	t.Run("round trip", func(t *testing.T) {
		require.NoError(t, check(data, hashes, signature))
	})

	t.Run("tampered data", func(t *testing.T) {
		require.Error(t, check([]byte("cluster configuratioN"), hashes, signature))
	})

	t.Run("digest of other data", func(t *testing.T) {
		err := check(data, otherHashes, signature)
		require.ErrorContains(t, err, `"sha256"`)
	})

	t.Run("signature of other data", func(t *testing.T) {
		err := check(data, hashes, otherSignature)
		require.ErrorContains(t, err, verificationError)
	})

	t.Run("extra hashes", func(t *testing.T) {
		extra := map[string][]byte{"sha256": hashes["sha256"], "md5": []byte("not even hex")}
		require.NoError(t, check(data, extra, signature))
	})

	t.Run("missing hash", func(t *testing.T) {
		require.EqualError(t, check(data, map[string][]byte{"md5": []byte("00")}, signature),
			`hash "sha256" not found`)
		require.EqualError(t, check(data, nil, signature), `hash "sha256" not found`)
	})

	t.Run("missing signature", func(t *testing.T) {
		require.ErrorContains(t, check(data, hashes, nil), verificationError)
	})

	t.Run("uppercase hex", func(t *testing.T) {
		upper := map[string][]byte{"sha256": bytes.ToUpper(hashes["sha256"])}
		require.NoError(t, check(data, upper, bytes.ToUpper(signature)))
	})

	t.Run("digest not hex", func(t *testing.T) {
		notHex := map[string][]byte{"sha256": []byte("zz" + string(hashes["sha256"][2:]))}
		require.ErrorContains(t, check(data, notHex, signature), `"sha256"`)
	})

	t.Run("signature not hex", func(t *testing.T) {
		require.ErrorContains(t, check(data, hashes, []byte("zz"+string(signature[2:]))),
			"signature")
	})

	t.Run("another key", func(t *testing.T) {
		foreign := checkFunction(t, foreignKey(t))
		require.ErrorContains(t, foreign(data, hashes, signature), verificationError)
	})

	t.Run("another salt length", func(t *testing.T) {
		longSalt := hex.EncodeToString(pssSign(t, key, data, 2*sha256.Size))
		require.ErrorContains(t, check(data, hashes, []byte(longSalt)), verificationError)
	})
}

func TestRSAProviderWithoutPublicKey(t *testing.T) {
	provider := integrity.NewRSAProvider()

	ctx, err := provider.InitializeIntegrityCheck("", t.TempDir())
	require.NoError(t, err)

	disabledCtx, err := integrity.NewDisabledProvider().InitializeIntegrityCheck("", t.TempDir())
	require.NoError(t, err)

	contexts := map[string]integrity.IntegrityCtx{
		"no public key":     ctx,
		"disabled provider": disabledCtx,
		"zero":              {},
	}

	for name, ctx := range contexts {
		t.Run(name, func(t *testing.T) {
			check, err := provider.GetCheckFunction(ctx)
			assert.Nil(t, check)
			require.ErrorIs(t, err, integrity.ErrNotConfigured)

			hashers, verifiers, err := provider.GetStorageVerifiers(ctx)
			assert.Nil(t, hashers)
			assert.Nil(t, verifiers)
			require.ErrorIs(t, err, integrity.ErrNotConfigured)
		})
	}
}

func TestRSAStoragePrimitives(t *testing.T) {
	key := signingKey(t)
	provider := integrity.NewRSAProvider()
	data := []byte("cluster configuration")

	hashers, verifiers, err := provider.GetStorageVerifiers(checkContext(t, key))
	require.NoError(t, err)
	require.Len(t, hashers, 1)
	require.Len(t, verifiers, 1)
	assert.Equal(t, "sha256", hashers[0].Name())
	assert.Equal(t, "rsapss", verifiers[0].Name())

	storageHashers, signers, err := provider.GetStorageSigners(
		writePrivateKey(t, t.TempDir(), key))
	require.NoError(t, err)
	require.Len(t, storageHashers, 1)
	require.Len(t, signers, 1)
	assert.Equal(t, "sha256", storageHashers[0].Name())
	assert.Equal(t, "rsapss", signers[0].Name())

	t.Run("signers produce hex", func(t *testing.T) {
		digest, err := storageHashers[0].Hash(data)
		require.NoError(t, err)
		assert.Equal(t, sha256Hex(string(data)), string(digest))

		signature, err := signers[0].Sign(data)
		require.NoError(t, err)

		raw, err := hex.DecodeString(string(signature))
		require.NoError(t, err)

		sum := sha256.Sum256(data)
		require.NoError(t, rsa.VerifyPSS(&key.PublicKey, crypto.SHA256, sum[:], raw,
			pssOptions()))
	})

	t.Run("verifiers accept raw and hex", func(t *testing.T) {
		sum := sha256.Sum256(data)
		require.NoError(t, hashers[0].Verify(data, sum[:]))
		require.NoError(t, hashers[0].Verify(data, []byte(sha256Hex(string(data)))))

		raw := pssSign(t, key, data, sha256.Size)
		require.NoError(t, verifiers[0].Verify(data, raw))
		require.NoError(t, verifiers[0].Verify(data, []byte(hex.EncodeToString(raw))))
	})

	t.Run("verifiers refuse", func(t *testing.T) {
		require.Error(t, hashers[0].Verify(data, []byte(sha256Hex("other data"))))

		foreign := pssSign(t, foreignKey(t), data, sha256.Size)
		require.ErrorContains(t, verifiers[0].Verify(data, foreign), verificationError)
	})

	t.Run("sign function passes storage verifiers", func(t *testing.T) {
		hashes, signature, err := signFunction(t, key)(data)
		require.NoError(t, err)

		require.NoError(t, hashers[0].Verify(data, hashes[hashers[0].Name()]))
		require.NoError(t, verifiers[0].Verify(data, signature))
	})

	t.Run("storage signers pass check function", func(t *testing.T) {
		digest, err := storageHashers[0].Hash(data)
		require.NoError(t, err)

		signature, err := signers[0].Sign(data)
		require.NoError(t, err)

		hashes := map[string][]byte{storageHashers[0].Name(): digest}
		require.NoError(t, checkFunction(t, key)(data, hashes, signature))
	})
}
