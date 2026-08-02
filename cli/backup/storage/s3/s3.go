package s3

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/tarantool/tt/cli/backup/storage"
)

var (
	errEndpointRequired        = errors.New("s3 endpoint is required")
	errBucketRequired          = errors.New("s3 bucket is required")
	errAccessKeyIDRequired     = errors.New("s3 access_key_id is required")
	errSecretAccessKeyRequired = errors.New("s3 secret_access_key is required")
	errNegativeObjectSize      = errors.New("s3 object size must be non-negative")
	errTLSWithoutSSL           = errors.New(
		"s3 ca_cert and skip_verify need an https endpoint")
)

// Config describes S3-compatible storage configuration.
type Config struct {
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	// SessionToken accompanies temporary credentials; empty for permanent ones.
	SessionToken string
	UseSSL       bool
	// CACert is a PEM file with the certificate authority of an endpoint the
	// system roots do not cover - the usual shape of an on-premise S3.
	CACert string
	// SkipVerify accepts any certificate the endpoint presents.
	SkipVerify bool
	Prefix     string
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

	transport, err := newTLSTransport(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to configure s3 tls: %w", err)
	}

	options := &minio.Options{
		Creds: credentials.NewStaticV4(
			cfg.AccessKeyID, cfg.SecretAccessKey, cfg.SessionToken),
		Secure: cfg.UseSSL,
		Region: cfg.Region,
	}
	// A typed nil in Options.Transport is not nil as an interface, and minio-go
	// would use it instead of building its own transport, so it is only set
	// when the config actually asked for one.
	if transport != nil {
		options.Transport = transport
	}

	client, err := minio.New(cfg.Endpoint, options)
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

	// GetObject is lazy: the request is issued on the first read. Force it here
	// with a one-byte probe so a missing object is detected up front, and use a
	// read (GET) rather than object.Stat (HEAD): a GET 404 carries a body, so
	// minio-go reports NoSuchKey vs NoSuchBucket accurately, whereas a bodyless
	// HEAD 404 is synthesized as NoSuchKey and would hide a missing bucket. The
	// probed byte is replayed to the caller so no data is lost.
	var probe [1]byte
	n, err := object.Read(probe[:])
	if err != nil && err != io.EOF {
		_ = object.Close()
		if isKeyNotFound(err) {
			return nil, storage.ErrKeyNotFound
		}

		return nil, fmt.Errorf("failed to get s3 object %q: %w", cleanKey, err)
	}

	return &objectReadCloser{
		Reader: io.MultiReader(bytes.NewReader(probe[:n]), object),
		closer: object,
	}, nil
}

// objectReadCloser rejoins the probed first byte with the rest of the object
// stream (via the embedded Reader) while delegating Close to the underlying
// object. Read is promoted from the embedded Reader so that io.EOF is passed
// through unwrapped.
type objectReadCloser struct {
	io.Reader
	closer io.Closer
}

func (o *objectReadCloser) Close() error {
	if err := o.closer.Close(); err != nil {
		return fmt.Errorf("failed to close s3 object: %w", err)
	}

	return nil
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

// newTLSTransport builds the transport minio-go talks through when the
// endpoint's certificate is not one the system roots cover, and returns nil
// when the defaults are enough. minio.DefaultTransport is the base rather than
// http.DefaultTransport: it carries the connection pooling, the timeouts and
// the disabled gzip decoding the client is written against.
func newTLSTransport(cfg Config) (*http.Transport, error) {
	if !cfg.UseSSL || (cfg.CACert == "" && !cfg.SkipVerify) {
		return nil, nil
	}

	transport, err := minio.DefaultTransport(true)
	if err != nil {
		return nil, fmt.Errorf("failed to create s3 transport: %w", err)
	}

	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}

	transport.TLSClientConfig.InsecureSkipVerify = cfg.SkipVerify

	if cfg.CACert != "" {
		pool, err := certPool(cfg.CACert)
		if err != nil {
			return nil, err //nolint:wrapcheck
		}

		transport.TLSClientConfig.RootCAs = pool
	}

	return transport, nil
}

// certPool returns the system roots plus the certificates of the PEM file at
// path. The file adds to the system pool rather than replacing it, as minio-go
// itself does for SSL_CERT_FILE: trusting a private CA must not untrust
// everything else.
func certPool(path string) (*x509.CertPool, error) {
	pemData, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read s3 ca_cert: %w", err)
	}

	pool, err := x509.SystemCertPool()
	if err != nil {
		// A platform with no readable system store still gets a working trust
		// anchor for this endpoint out of the file it was pointed at.
		pool = x509.NewCertPool()
	}

	if !pool.AppendCertsFromPEM(pemData) {
		return nil, fmt.Errorf("no certificates found in s3 ca_cert %q", path)
	}

	return pool, nil
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
	case !cfg.UseSSL && (cfg.CACert != "" || cfg.SkipVerify):
		// Silently dropping them would leave an operator convinced the private
		// CA is in use over a plaintext connection.
		return errTLSWithoutSSL
	default:
		return nil
	}
}

// isKeyNotFound reports whether err means the object key does not exist.
//
// It matches only the NoSuchKey code, never a bare 404: a missing bucket also
// returns 404 (as NoSuchBucket), and treating that as "key not found" would make
// Get report "not found" and Delete report success against a misconfigured bucket.
// Callers must pass errors from body-bearing requests (GET, DELETE), where minio-go
// decodes the real code; a HEAD 404 has no body and is synthesized as NoSuchKey
// regardless of the actual cause, so Get probes with a read rather than a Stat.
func isKeyNotFound(err error) bool {
	return minio.ToErrorResponse(err).Code == "NoSuchKey"
}
