//go:build integration_docker

package s3

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/lib/backup/storage"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestGaragePut(t *testing.T) {
	ctx := t.Context()
	st := newGarageStorage(ctx, t)

	key := storage.ArchiveKey("garage-put", "rs1")
	data := []byte("archive payload")

	require.NoError(t, st.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	objects, err := st.List(ctx, storage.DataPrefix())
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, key, objects[0].Key)
	require.Equal(t, int64(len(data)), objects[0].Size)
}

func TestGarageGet(t *testing.T) {
	ctx := t.Context()
	st := newGarageStorage(ctx, t)

	key := storage.ManifestKey("garage-get")
	data := []byte(`{"status":"ok"}`)
	require.NoError(t, st.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	reader, err := st.Get(ctx, key)
	require.NoError(t, err)
	defer reader.Close()

	actual, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.Equal(t, data, actual)
}

func TestGarageList(t *testing.T) {
	ctx := t.Context()
	st := newGarageStorage(ctx, t)

	manifestKey := storage.ManifestKey("garage-list")
	archiveKey := storage.ArchiveKey("garage-list", "rs1")
	manifest := []byte(`{"status":"ok"}`)
	archive := []byte("archive")
	require.NoError(t, st.Put(ctx, manifestKey, bytes.NewReader(manifest), int64(len(manifest))))
	require.NoError(t, st.Put(ctx, archiveKey, bytes.NewReader(archive), int64(len(archive))))

	objects, err := st.List(ctx, storage.ManifestsPrefix())
	require.NoError(t, err)
	require.Len(t, objects, 1)
	require.Equal(t, manifestKey, objects[0].Key)
	require.False(t, objects[0].LastModified.IsZero())
}

func TestGarageDelete(t *testing.T) {
	ctx := t.Context()
	st := newGarageStorage(ctx, t)

	key := storage.ManifestKey("garage-delete")
	data := []byte(`{"status":"ok"}`)
	require.NoError(t, st.Put(ctx, key, bytes.NewReader(data), int64(len(data))))

	require.NoError(t, st.Delete(ctx, key))
	_, err := st.Get(ctx, key)
	require.True(t, errors.Is(err, storage.ErrKeyNotFound))
}

func TestGarageGetMissingBucketIsNotKeyNotFound(t *testing.T) {
	ctx := t.Context()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	garage := startGarage(ctx, t)
	st, err := New(Config{
		Endpoint:        garage.endpoint,
		Bucket:          "no-such-bucket",
		Region:          garage.region,
		AccessKeyID:     garage.accessKey,
		SecretAccessKey: garage.secretKey,
	})
	require.NoError(t, err)

	// A missing bucket must surface as a real error, not be flattened into
	// ErrKeyNotFound the way a bodyless HEAD 404 would be.
	_, err = st.Get(ctx, storage.ManifestKey("whatever"))
	require.Error(t, err)
	require.False(t, errors.Is(err, storage.ErrKeyNotFound))
}

type garageInstance struct {
	endpoint  string
	bucket    string
	region    string
	accessKey string
	secretKey string
}

func newGarageStorage(ctx context.Context, t *testing.T) *Storage {
	t.Helper()
	testcontainers.SkipIfProviderIsNotHealthy(t)

	garage := startGarage(ctx, t)
	st, err := New(Config{
		Endpoint:        garage.endpoint,
		Bucket:          garage.bucket,
		Region:          garage.region,
		AccessKeyID:     garage.accessKey,
		SecretAccessKey: garage.secretKey,
		Prefix:          "payments-cluster/production/",
	})
	require.NoError(t, err)

	return st
}

func startGarage(ctx context.Context, t *testing.T) garageInstance {
	t.Helper()

	inst := garageInstance{
		bucket:    "tt-backup-test",
		region:    "garage",
		accessKey: "GKTTBACKUPTEST000000000000000000",
		secretKey: "tt-backup-test-secret-key",
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        "dxflrs/garage:v2.3.0",
			ExposedPorts: []string{"3900/tcp"},
			Cmd:          []string{"/garage", "server", "--single-node", "--default-bucket"},
			Env: map[string]string{
				"GARAGE_DEFAULT_ACCESS_KEY": inst.accessKey,
				"GARAGE_DEFAULT_SECRET_KEY": inst.secretKey,
				"GARAGE_DEFAULT_BUCKET":     inst.bucket,
			},
			Files: []testcontainers.ContainerFile{
				{
					Reader:            strings.NewReader(garageConfig),
					ContainerFilePath: "/etc/garage.toml",
					FileMode:          0o644,
				},
			},
			WaitingFor: wait.ForListeningPort("3900/tcp").WithStartupTimeout(time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, container)
	require.NoError(t, err)

	inst.endpoint, err = container.PortEndpoint(ctx, "3900/tcp", "")
	require.NoError(t, err)

	waitForDefaultBucket(ctx, t, container, inst.bucket)
	return inst
}

func waitForDefaultBucket(
	ctx context.Context,
	t *testing.T,
	container testcontainers.Container,
	bucket string,
) {
	t.Helper()

	deadline := time.Now().Add(time.Minute)
	var lastOutput string
	for time.Now().Before(deadline) {
		exitCode, output, err := container.Exec(ctx, []string{
			"/garage", "bucket", "info", bucket,
		})
		if err == nil && exitCode == 0 {
			return
		}
		if output != nil {
			data, readErr := io.ReadAll(output)
			if readErr == nil {
				lastOutput = string(data)
			}
		}
		time.Sleep(time.Second)
	}

	logs := readContainerLogs(ctx, container)
	t.Fatalf("Garage default bucket was not created, last exec output:\n%s\ncontainer logs:\n%s",
		lastOutput, logs)
}

func readContainerLogs(ctx context.Context, container testcontainers.Container) string {
	logs, err := container.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("failed to read logs: %v", err)
	}
	defer logs.Close()

	data, err := io.ReadAll(logs)
	if err != nil {
		return fmt.Sprintf("failed to read logs: %v", err)
	}
	return string(data)
}

const garageConfig = `metadata_dir = "/tmp/meta"
data_dir = "/tmp/data"
db_engine = "sqlite"
replication_factor = 1

rpc_bind_addr = "[::]:3901"
rpc_public_addr = "127.0.0.1:3901"
rpc_secret = "0000000000000000000000000000000000000000000000000000000000000000"

[s3_api]
s3_region = "garage"
api_bind_addr = "[::]:3900"
root_domain = ".s3.garage.localhost"
`
