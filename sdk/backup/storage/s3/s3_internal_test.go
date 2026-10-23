package s3

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/backup/storage"
)

func TestObjectName(t *testing.T) {
	s := &Storage{prefix: "base/prefix/"}

	require.Equal(t, "base/prefix/manifests/", s.objectName("manifests/"))
	require.Equal(t, "base/prefix/manifests/backup.json", s.objectName("manifests/backup.json"))
}

func TestNewUsesPrefixWithSlash(t *testing.T) {
	store, err := New(Config{
		Endpoint:        "localhost:3900",
		Bucket:          "backups",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		Prefix:          "cluster/prod",
	})
	require.NoError(t, err)
	require.Equal(t, "cluster/prod/", store.prefix)
}

func TestNewEmptyPrefix(t *testing.T) {
	store, err := New(Config{
		Endpoint:        "localhost:3900",
		Bucket:          "backups",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
	})
	require.NoError(t, err)
	require.Empty(t, store.prefix)
}

func TestNewPrefixWithTrailingSlash(t *testing.T) {
	store, err := New(Config{
		Endpoint:        "localhost:3900",
		Bucket:          "backups",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		Prefix:          "cluster/prod/",
	})
	require.NoError(t, err)
	require.Equal(t, "cluster/prod/", store.prefix)
}

func TestValidateConfig(t *testing.T) {
	valid := Config{
		Endpoint:        "localhost:3900",
		Bucket:          "backups",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
	}

	testCases := []struct {
		name string
		cfg  Config
		err  error
	}{
		{name: "empty endpoint", cfg: Config{}, err: errEndpointRequired},
		{name: "empty bucket", cfg: Config{Endpoint: valid.Endpoint}, err: errBucketRequired},
		{
			name: "empty access key id",
			cfg:  Config{Endpoint: valid.Endpoint, Bucket: valid.Bucket},
			err:  errAccessKeyIDRequired,
		},
		{
			name: "empty secret access key",
			cfg: Config{
				Endpoint:    valid.Endpoint,
				Bucket:      valid.Bucket,
				AccessKeyID: valid.AccessKeyID,
			},
			err: errSecretAccessKeyRequired,
		},
		{name: "valid", cfg: valid},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := validateConfig(testCase.cfg)
			if testCase.err == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, testCase.err)
			}
		})
	}
}

func TestPutRejectsNegativeSize(t *testing.T) {
	s := &Storage{}
	err := s.Put(t.Context(), "key", strings.NewReader("data"), -1)
	require.ErrorIs(t, err, errNegativeObjectSize)
}

func TestPutRejectsInvalidKey(t *testing.T) {
	s := &Storage{}
	err := s.Put(t.Context(), "../escape", strings.NewReader("data"), 4)
	require.ErrorIs(t, err, storage.ErrInvalidKey)
}

func TestGetRejectsInvalidKey(t *testing.T) {
	s := &Storage{}
	_, err := s.Get(t.Context(), "../escape")
	require.ErrorIs(t, err, storage.ErrInvalidKey)
}

func TestDeleteRejectsInvalidKey(t *testing.T) {
	s := &Storage{}
	err := s.Delete(t.Context(), "../escape")
	require.ErrorIs(t, err, storage.ErrInvalidKey)
}

func TestListRejectsInvalidPrefix(t *testing.T) {
	s := &Storage{}
	_, err := s.List(t.Context(), "../escape")
	require.ErrorIs(t, err, storage.ErrInvalidKey)
}

func TestNewRejectsInvalidPrefix(t *testing.T) {
	_, err := New(Config{
		Endpoint:        "localhost:3900",
		Bucket:          "backups",
		AccessKeyID:     "key",
		SecretAccessKey: "secret",
		Prefix:          "../escape",
	})
	require.ErrorIs(t, err, storage.ErrInvalidKey)
}
