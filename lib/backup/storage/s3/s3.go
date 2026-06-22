package s3

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/tarantool/tt/lib/backup/storage"
)

var (
	errEndpointRequired        = errors.New("s3 endpoint is required")
	errBucketRequired          = errors.New("s3 bucket is required")
	errAccessKeyIDRequired     = errors.New("s3 access_key_id is required")
	errSecretAccessKeyRequired = errors.New("s3 secret_access_key is required")
	errNegativeObjectSize      = errors.New("s3 object size must be non-negative")
)

// Config describes S3-compatible storage configuration.
type Config struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	Prefix          string
}

// Storage is an S3-compatible backup storage backend.
type Storage struct {
	client *minio.Client
	bucket string
	prefix string
}

// New opens S3-compatible backup storage using minio-go.
func New(cfg Config) (*Storage, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, fmt.Errorf("failed to validate s3 config: %w", err)
	}

	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(cfg.AccessKeyID, cfg.SecretAccessKey, ""),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create s3 client: %w", err)
	}

	prefix, err := storage.CleanPrefix(cfg.Prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to clean storage prefix %q: %w", cfg.Prefix, err)
	}

	return &Storage{
		client: client,
		bucket: cfg.Bucket,
		prefix: storage.PrefixWithSlash(prefix),
	}, nil
}

// List returns objects under the given prefix (joined with the storage prefix), sorted by key.
func (s *Storage) List(ctx context.Context, prefix string) ([]storage.ObjectInfo, error) {
	cleanPrefix, err := storage.CleanPrefix(prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to clean list prefix %q: %w", prefix, err)
	}

	objectPrefix := s.objectName(cleanPrefix)

	objectsCh := s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    objectPrefix,
		Recursive: true,
	})

	objects := make([]storage.ObjectInfo, 0)

	for obj := range objectsCh {
		if obj.Err != nil {
			return nil, fmt.Errorf("failed to list s3 prefix %q: %w", cleanPrefix, obj.Err)
		}
		key, ok := strings.CutPrefix(obj.Key, s.prefix)
		if !ok {
			continue
		}

		objects = append(objects, storage.ObjectInfo{
			Key:          key,
			Size:         obj.Size,
			LastModified: obj.LastModified,
		})
	}

	sort.Slice(objects, func(i, j int) bool {
		return objects[i].Key < objects[j].Key
	})

	return objects, nil
}

// Get opens the object for reading, returning storage.ErrKeyNotFound if it is absent.
func (s *Storage) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	cleanKey, err := storage.CleanKey(key)
	if err != nil {
		return nil, fmt.Errorf("failed to clean object key %q: %w", key, err)
	}

	objectName := s.objectName(cleanKey)

	object, err := s.client.GetObject(ctx, s.bucket, objectName, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get s3 object %q: %w", cleanKey, err)
	}

	if _, err := object.Stat(); err != nil {
		_ = object.Close()
		if isKeyNotFound(err) {
			return nil, storage.ErrKeyNotFound
		}

		return nil, fmt.Errorf("failed to get s3 object %q: %w", cleanKey, err)
	}

	return object, nil
}

// Put uploads the object; size must be non-negative and is passed through to minio-go.
func (s *Storage) Put(ctx context.Context, key string, r io.Reader, size int64) error {
	cleanKey, err := storage.CleanKey(key)
	if err != nil {
		return fmt.Errorf("failed to clean object key %q: %w", key, err)
	}

	if size < 0 {
		return errNegativeObjectSize
	}

	objectName := s.objectName(cleanKey)

	_, err = s.client.PutObject(ctx, s.bucket, objectName, r, size, minio.PutObjectOptions{})
	if err != nil {
		return fmt.Errorf("failed to put s3 object %q: %w", cleanKey, err)
	}

	return nil
}

// Delete removes the object; a missing object is not an error.
func (s *Storage) Delete(ctx context.Context, key string) error {
	cleanKey, err := storage.CleanKey(key)
	if err != nil {
		return fmt.Errorf("failed to clean object key %q: %w", key, err)
	}

	objectName := s.objectName(cleanKey)

	err = s.client.RemoveObject(ctx, s.bucket, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		if isKeyNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to delete s3 object %q: %w", cleanKey, err)
	}

	return nil
}

// objectName prepends the storage prefix to a key to form the full bucket object name.
func (s *Storage) objectName(key string) string {
	return s.prefix + key
}

// validateConfig ensures the required S3 connection fields are present.
func validateConfig(cfg Config) error {
	switch {
	case strings.TrimSpace(cfg.Endpoint) == "":
		return errEndpointRequired
	case strings.TrimSpace(cfg.Bucket) == "":
		return errBucketRequired
	case strings.TrimSpace(cfg.AccessKeyID) == "":
		return errAccessKeyIDRequired
	case strings.TrimSpace(cfg.SecretAccessKey) == "":
		return errSecretAccessKeyRequired
	default:
		return nil
	}
}

// isKeyNotFound reports whether err means the object key does not exist.
func isKeyNotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchKey" || resp.StatusCode == http.StatusNotFound
}
