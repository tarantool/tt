package backup

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup/storage"
)

func TestParseStorageURI_File(t *testing.T) {
	cases := []struct {
		name       string
		uri        string
		wantErr    bool
		wantPath   string
		wantPrefix string
	}{
		{name: "absolute path", uri: "file:///tmp/backups", wantPath: "/tmp/backups"},
		{
			name:     "nested absolute path",
			uri:      "file:///var/lib/tt/backups",
			wantPath: "/var/lib/tt/backups",
		},
		{
			name:       "with prefix",
			uri:        "file:///var/backups?Prefix=mycluster",
			wantPath:   "/var/backups",
			wantPrefix: "mycluster",
		},
		{name: "empty path", uri: "file://", wantErr: true},
		{name: "host in URI", uri: "file://tmp/backups", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(tc.uri)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg)
			assert.Equal(t, StorageTypeFile, cfg.Type)
			assert.Equal(t, tc.wantPath, cfg.Path)
			assert.Equal(t, tc.wantPrefix, cfg.Prefix)
		})
	}
}

func TestParseStorageURI_S3(t *testing.T) {
	cases := []struct {
		name          string
		uri           string
		wantErr       bool
		wantEndpoint  string
		wantBucket    string
		wantRegion    string
		wantAccessKey string
		wantSecretKey string
		wantUseSSL    bool
		wantPrefix    string
	}{
		{
			name: "https with all params",
			uri: "s3+https://s3.example.com:9000/mybucket/backups" +
				"?Region=us-east-1&AccessKeyID=minio&SecretAccessKey=minio123",
			wantEndpoint:  "s3.example.com:9000",
			wantBucket:    "mybucket",
			wantPrefix:    "backups",
			wantRegion:    "us-east-1",
			wantAccessKey: "minio",
			wantSecretKey: "minio123",
			wantUseSSL:    true,
		},
		{
			name:          "http without region",
			uri:           "s3+http://localhost:9000/bucket?AccessKeyID=key&SecretAccessKey=secret",
			wantEndpoint:  "localhost:9000",
			wantBucket:    "bucket",
			wantPrefix:    "",
			wantRegion:    "",
			wantAccessKey: "key",
			wantSecretKey: "secret",
			wantUseSSL:    false,
		},
		{
			name:          "https without port",
			uri:           "s3+https://s3.amazonaws.com/mybucket?AccessKeyID=k&SecretAccessKey=s",
			wantEndpoint:  "s3.amazonaws.com",
			wantBucket:    "mybucket",
			wantAccessKey: "k",
			wantSecretKey: "s",
			wantUseSSL:    true,
		},
		{
			name:          "nested prefix",
			uri:           "s3+https://host:9000/bucket/a/b/c?AccessKeyID=k&SecretAccessKey=s",
			wantEndpoint:  "host:9000",
			wantBucket:    "bucket",
			wantPrefix:    "a/b/c",
			wantAccessKey: "k",
			wantSecretKey: "s",
			wantUseSSL:    true,
		},
		{
			name:    "missing endpoint",
			uri:     "s3+https:///bucket?AccessKeyID=k&SecretAccessKey=s",
			wantErr: true,
		},
		{
			name:    "missing bucket",
			uri:     "s3+https://host:9000?AccessKeyID=k&SecretAccessKey=s",
			wantErr: true,
		},
		{
			name:    "missing AccessKeyID",
			uri:     "s3+https://host:9000/bucket?SecretAccessKey=s",
			wantErr: true,
		},
		{
			name:    "missing SecretAccessKey",
			uri:     "s3+https://host:9000/bucket?AccessKeyID=k",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(tc.uri)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, cfg)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg)
			assert.Equal(t, StorageTypeS3, cfg.Type)
			assert.Equal(t, tc.wantEndpoint, cfg.Endpoint)
			assert.Equal(t, tc.wantBucket, cfg.Bucket)
			assert.Equal(t, tc.wantRegion, cfg.Region)
			assert.Equal(t, tc.wantAccessKey, cfg.AccessKeyID)
			assert.Equal(t, tc.wantSecretKey, cfg.SecretAccessKey)
			assert.Equal(t, tc.wantUseSSL, cfg.UseSSL)
			assert.Equal(t, tc.wantPrefix, cfg.Prefix)
		})
	}
}

func TestParseStorageURI_InvalidScheme(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"unsupported scheme", "ftp://host/path"},
		{
			"plain s3 without http/https",
			"s3://host:9000/bucket?AccessKeyID=k&SecretAccessKey=secret",
		},
		{"malformed URI", "s3+https://host/bucket?SecretAccessKey=secret%ZZ"},
		{"file URI without path", "file://user:secret@localhost"},
		{"empty string", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(tc.uri)
			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.NotContains(t, err.Error(), "secret")
		})
	}
}

func TestLatestManifestReadsOnlyLastObject(t *testing.T) {
	ctx := context.Background()
	cfg, err := ParseStorageURI("file://" + t.TempDir())
	require.NoError(t, err)
	store, err := OpenStorage(cfg)
	require.NoError(t, err)

	// An older broken manifest must not affect reading the latest one.
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey("0000-broken"),
		[]byte("{"),
	))

	want := ClusterManifest{
		SchemaVersion: SchemaVersion,
		BackupID:      "2026-01-01-full",
	}
	data, err := json.Marshal(want)
	require.NoError(t, err)
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey(string(want.BackupID)),
		data,
	))

	got, err := LatestManifest(ctx, store)
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, want.BackupID, got.BackupID)
}

func TestLatestManifestEmptyStorage(t *testing.T) {
	cfg, err := ParseStorageURI("file://" + t.TempDir())
	require.NoError(t, err)
	store, err := OpenStorage(cfg)
	require.NoError(t, err)

	manifest, err := LatestManifest(context.Background(), store)
	require.NoError(t, err)
	assert.Nil(t, manifest)
}

func TestLatestManifestCorruptLatest(t *testing.T) {
	ctx := context.Background()
	cfg, err := ParseStorageURI("file://" + t.TempDir())
	require.NoError(t, err)
	store, err := OpenStorage(cfg)
	require.NoError(t, err)

	// A valid older manifest.
	valid := ClusterManifest{
		SchemaVersion: SchemaVersion,
		BackupID:      "2026-01-01-full",
	}
	data, err := json.Marshal(valid)
	require.NoError(t, err)
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey(string(valid.BackupID)),
		data,
	))

	// A corrupt latest manifest.
	require.NoError(t, storage.PutBytes(
		ctx,
		store,
		storage.ManifestKey("2026-01-02-broken"),
		[]byte("{invalid"),
	))

	_, err = LatestManifest(ctx, store)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "decode latest backup manifest")
}

func TestOpenStorage_UnknownType(t *testing.T) {
	_, err := OpenStorage(&StorageConfig{Type: "unknown"})
	require.Error(t, err)
}
