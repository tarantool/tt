package cluster

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/hex"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gstorage "github.com/tarantool/go-storage/v2"
	gcrypto "github.com/tarantool/go-storage/v2/crypto"
	"github.com/tarantool/go-storage/v2/driver/dummy"
	"github.com/tarantool/go-storage/v2/hasher"
	"github.com/tarantool/go-storage/v2/operation"
)

// legacyTestPrefix is the prefix tt 2 and Tarantool EE publish under by
// default.
const legacyTestPrefix = "/tt"

// newTestKey returns a fresh RSA key for signing test data.
func newTestKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	return key
}

// signerOptions are the integrity options tt publishes with: a hex SHA-256
// hasher and a hex RSA-PSS signer, as the RSA provider of sdk/integrity
// gives them.
func signerOptions(key *rsa.PrivateKey) *IntegrityOptions {
	return &IntegrityOptions{
		Hashers: []hasher.Hasher{hasher.NewSHA256Hasher(hasher.WithMode(hasher.ModeHex))},
		SignerVerifiers: []gcrypto.SignerVerifier{
			gcrypto.NewRSAPSS(*key, gcrypto.WithMode(gcrypto.ModeHex)),
		},
	}
}

// verifierOptions are the integrity options tt collects with: the hasher and
// the verifier alone, the shape go-storage's WithSingleSigCompact refuses.
func verifierOptions(key *rsa.PrivateKey) *IntegrityOptions {
	return &IntegrityOptions{
		Hashers:   []hasher.Hasher{hasher.NewSHA256Hasher()},
		Verifiers: []gcrypto.Verifier{gcrypto.NewRSAPSSVerifier(key.PublicKey)},
	}
}

// legacySign returns the hash and the signature of data as tt 2 wrote them:
// the hex SHA-256 digest and the hex RSASSA-PSS signature of that digest,
// with the salt as long as the hash.
func legacySign(t *testing.T, key *rsa.PrivateKey, data []byte) (string, string) {
	t.Helper()

	digest := sha256.Sum256(data)

	signature, err := rsa.SignPSS(rand.Reader, key, crypto.SHA256, digest[:],
		&rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: crypto.SHA256})
	require.NoError(t, err)

	return hex.EncodeToString(digest[:]), hex.EncodeToString(signature)
}

// putRaw writes the keys and values given into stor, bypassing integrity.
func putRaw(t *testing.T, stor gstorage.Storage, kvs map[string]string) {
	t.Helper()

	ops := make([]operation.Operation, 0, len(kvs))
	for key, value := range kvs {
		ops = append(ops, operation.Put([]byte(key), []byte(value)))
	}

	_, err := stor.Tx(context.Background()).Then(ops...).Commit()
	require.NoError(t, err)
}

// rawKeys lists the keys stor holds, sorted.
func rawKeys(t *testing.T, stor gstorage.Storage) []string {
	t.Helper()

	kvs, err := stor.Range(context.Background(), gstorage.WithPrefix("/"))
	require.NoError(t, err)

	keys := make([]string, 0, len(kvs))
	for _, keyValue := range kvs {
		keys = append(keys, string(keyValue.Key))
	}

	slices.Sort(keys)

	return keys
}

// newTestStorage binds a RawStorage of the configuration to stor.
func newTestStorage(
	t *testing.T, stor gstorage.Storage, key string, opts *IntegrityOptions,
) *RawStorage {
	t.Helper()

	raw, err := NewStorage(stor, legacyTestPrefix, time.Second, key, etcdStorageType, opts,
		configLocation)
	require.NoError(t, err)

	return raw
}

func TestPublishLaysIntegrityKeysOutAsTt2(t *testing.T) {
	key := newTestKey(t)
	data := []byte("credentials: {}\n")

	tests := []struct {
		name     string
		key      string
		expected []string
	}{
		{
			name: "whole configuration",
			key:  "",
			expected: []string{
				legacyTestPrefix + "/config/all",
				legacyTestPrefix + "/hashes/sha256/all",
				legacyTestPrefix + "/sig/all",
			},
		},
		{
			name: "nested key",
			key:  "groups/storages",
			expected: []string{
				legacyTestPrefix + "/config/groups/storages",
				legacyTestPrefix + "/hashes/sha256/groups/storages",
				legacyTestPrefix + "/sig/groups/storages",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stor := gstorage.NewStorage(dummy.New())

			err := newTestStorage(t, stor, test.key, signerOptions(key)).Publish(0, data)
			require.NoError(t, err)
			require.Equal(t, test.expected, rawKeys(t, stor))
		})
	}
}

func TestPublishWithoutIntegrityWritesTheValueAlone(t *testing.T) {
	stor := gstorage.NewStorage(dummy.New())

	err := newTestStorage(t, stor, "", nil).Publish(0, []byte("a: 1\n"))
	require.NoError(t, err)
	require.Equal(t, []string{legacyTestPrefix + "/config/all"}, rawKeys(t, stor))
}

func TestCollectReadsDataTt2Signed(t *testing.T) {
	key := newTestKey(t)
	all := []byte("groups: {}\n")
	nested := []byte("replicasets: {}\n")

	allHash, allSig := legacySign(t, key, all)
	nestedHash, nestedSig := legacySign(t, key, nested)

	stor := gstorage.NewStorage(dummy.New())
	putRaw(t, stor, map[string]string{
		legacyTestPrefix + "/config/all":                    string(all),
		legacyTestPrefix + "/hashes/sha256/all":             allHash,
		legacyTestPrefix + "/sig/all":                       allSig,
		legacyTestPrefix + "/config/groups/storages":        string(nested),
		legacyTestPrefix + "/hashes/sha256/groups/storages": nestedHash,
		legacyTestPrefix + "/sig/groups/storages":           nestedSig,
	})

	t.Run("prefix", func(t *testing.T) {
		data, err := newTestStorage(t, stor, "", verifierOptions(key)).Collect()
		require.NoError(t, err)
		require.Equal(t, []Data{
			{Source: legacyTestPrefix + "/config/all", Value: all, Revision: data[0].Revision},
			{
				Source:   legacyTestPrefix + "/config/groups/storages",
				Value:    nested,
				Revision: data[1].Revision,
			},
		}, data)
	})

	t.Run("key", func(t *testing.T) {
		data, err := newTestStorage(t, stor, "groups/storages", verifierOptions(key)).Collect()
		require.NoError(t, err)
		require.Len(t, data, 1)
		require.Equal(t, nested, data[0].Value)
	})
}

func TestCollectReadsWhatPublishWrote(t *testing.T) {
	key := newTestKey(t)
	data := []byte("credentials: {}\n")
	stor := gstorage.NewStorage(dummy.New())

	err := newTestStorage(t, stor, "", signerOptions(key)).Publish(0, data)
	require.NoError(t, err)

	collected, err := newTestStorage(t, stor, "", verifierOptions(key)).Collect()
	require.NoError(t, err)
	require.Len(t, collected, 1)
	require.Equal(t, data, collected[0].Value)
}

func TestCollectRefusesTamperedData(t *testing.T) {
	key := newTestKey(t)
	data := []byte("groups: {}\n")
	hash, sig := legacySign(t, key, data)

	tests := []struct {
		name string
		kvs  map[string]string
		opts *IntegrityOptions
	}{
		{
			name: "changed value",
			kvs: map[string]string{
				legacyTestPrefix + "/config/all":        "groups: {evil: {}}\n",
				legacyTestPrefix + "/hashes/sha256/all": hash,
				legacyTestPrefix + "/sig/all":           sig,
			},
			opts: verifierOptions(key),
		},
		{
			name: "missing signature",
			kvs: map[string]string{
				legacyTestPrefix + "/config/all":        string(data),
				legacyTestPrefix + "/hashes/sha256/all": hash,
			},
			opts: verifierOptions(key),
		},
		{
			name: "another key",
			kvs: map[string]string{
				legacyTestPrefix + "/config/all":        string(data),
				legacyTestPrefix + "/hashes/sha256/all": hash,
				legacyTestPrefix + "/sig/all":           sig,
			},
			opts: verifierOptions(newTestKey(t)),
		},
		{
			name: "signature in the layout of tt 3 betas",
			kvs: map[string]string{
				legacyTestPrefix + "/config/all":               string(data),
				legacyTestPrefix + "/hashes/sha256/config/all": hash,
				legacyTestPrefix + "/sig/rsapss/config/all":    sig,
			},
			opts: verifierOptions(key),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stor := gstorage.NewStorage(dummy.New())
			putRaw(t, stor, test.kvs)

			_, err := newTestStorage(t, stor, "", test.opts).Collect()
			require.ErrorIs(t, err, ErrIntegrityCheckFailed)
			require.NotErrorIs(t, err, ErrNoConfiguration)
			require.ErrorContains(t, err, `key "/tt/config/all"`)

			_, err = newTestStorage(t, stor, "all", test.opts).Collect()
			require.ErrorIs(t, err, ErrIntegrityCheckFailed)
		})
	}
}

func TestPublishAllDropsTheIntegrityKeysOfOtherKeys(t *testing.T) {
	key := newTestKey(t)
	stor := gstorage.NewStorage(dummy.New())

	err := newTestStorage(t, stor, "groups/storages", signerOptions(key)).
		Publish(0, []byte("replicasets: {}\n"))
	require.NoError(t, err)

	err = newTestStorage(t, stor, "", signerOptions(key)).Publish(0, []byte("groups: {}\n"))
	require.NoError(t, err)

	require.Equal(t, []string{
		legacyTestPrefix + "/config/all",
		legacyTestPrefix + "/hashes/sha256/all",
		legacyTestPrefix + "/sig/all",
	}, rawKeys(t, stor))
}
