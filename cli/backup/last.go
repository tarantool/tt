package backup

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/tarantool/tt/cli/backup/storage"
	"github.com/tarantool/tt/cli/backup/storage/fs"
	"github.com/tarantool/tt/cli/backup/storage/s3"
)

// StorageType is the type of a backup storage backend.
type StorageType = string

const (
	StorageTypeFile StorageType = "file"
	StorageTypeS3   StorageType = "s3"
)

const (
	schemeS3HTTPS  = "s3+https"
	schemeS3HTTP   = "s3+http"
	paramAccessKey = "AccessKeyID"
	paramSecretKey = "SecretAccessKey"
	paramRegion    = "region"
	paramPrefix    = "prefix"
)

// StorageConfig is a parsed backup storage configuration derived from a URI.
type StorageConfig struct {
	Type StorageType
	// File storage.
	Path string
	// S3 storage.
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	UseSSL          bool
	// Common.
	Prefix string
}

// ParseStorageURI parses a storage URI string into a StorageConfig.
//
// Supported schemes:
//
//	file://<abs_path>
//	    Local filesystem storage. The path must be absolute.
//
//	s3+https://endpoint:port/bucket/prefix?region=...&AccessKeyID=...&SecretAccessKey=...
//	s3+http://endpoint:port/bucket/prefix?region=...&AccessKeyID=...&SecretAccessKey=...
//	    S3-compatible storage. Use s3+https for TLS, s3+http for plain TCP.
//	    The first path segment after the host is the bucket name, the rest is
//	    the optional key prefix. Query parameters:
//	      region           - AWS region (optional).
//	      AccessKeyID      - access key ID (required).
//	      SecretAccessKey  - secret access key (required).
func ParseStorageURI(rawURI string) (*StorageConfig, error) {
	if strings.TrimSpace(rawURI) == "" {
		return nil, fmt.Errorf("storage URI is empty")
	}

	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("failed to parse storage URI %q: %w", rawURI, err)
	}

	switch u.Scheme {
	case StorageTypeFile:
		cfg, err := parseFileURI(u)
		if err != nil {
			return nil, fmt.Errorf("parse file storage URI: %w", err)
		}
		return cfg, nil
	case schemeS3HTTPS, schemeS3HTTP:
		cfg, err := parseS3URI(u)
		if err != nil {
			return nil, fmt.Errorf("parse s3 storage URI: %w", err)
		}
		return cfg, nil
	default:
		return nil, fmt.Errorf(
			"unsupported storage scheme %q in URI %q: expected %q or %q",
			u.Scheme, rawURI, StorageTypeFile, "s3+http(s)")
	}
}

func parseFileURI(u *url.URL) (*StorageConfig, error) {
	if u.Path == "" {
		return nil, fmt.Errorf("file storage URI must contain a path, got %q", u.String())
	}

	return &StorageConfig{
		Type:   StorageTypeFile,
		Path:   u.Path,
		Prefix: u.Query().Get(paramPrefix),
	}, nil
}

func parseS3URI(u *url.URL) (*StorageConfig, error) {
	endpoint := u.Host
	if endpoint == "" {
		return nil, fmt.Errorf("s3 storage URI must contain an endpoint (host[:port])")
	}

	trimmed := strings.TrimPrefix(u.Path, "/")
	if trimmed == "" {
		return nil, fmt.Errorf("s3 storage URI must contain a bucket name in the path")
	}

	bucket, prefix, _ := strings.Cut(trimmed, "/")
	if bucket == "" {
		return nil, fmt.Errorf("s3 storage URI must contain a bucket name")
	}

	query := u.Query()
	accessKeyID := query.Get(paramAccessKey)
	secretAccessKey := query.Get(paramSecretKey)

	if accessKeyID == "" {
		return nil, fmt.Errorf("s3 storage URI must contain %s query parameter", paramAccessKey)
	}
	if secretAccessKey == "" {
		return nil, fmt.Errorf("s3 storage URI must contain %s query parameter", paramSecretKey)
	}

	return &StorageConfig{
		Type:            StorageTypeS3,
		Endpoint:        endpoint,
		Bucket:          bucket,
		Region:          query.Get(paramRegion),
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		UseSSL:          u.Scheme == schemeS3HTTPS,
		Prefix:          prefix,
	}, nil
}

// OpenStorage creates a backup storage backend from a StorageConfig.
func OpenStorage(cfg *StorageConfig) (storage.Storage, error) {
	switch cfg.Type {
	case StorageTypeFile:
		st, err := fs.New(fs.Config{Path: cfg.Path, Prefix: cfg.Prefix})
		if err != nil {
			return nil, fmt.Errorf("create file storage: %w", err)
		}
		return st, nil
	case StorageTypeS3:
		st, err := s3.New(s3.Config{
			Endpoint:        cfg.Endpoint,
			Bucket:          cfg.Bucket,
			Region:          cfg.Region,
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
			UseSSL:          cfg.UseSSL,
			Prefix:          cfg.Prefix,
		})
		if err != nil {
			return nil, fmt.Errorf("create s3 storage: %w", err)
		}
		return st, nil
	default:
		return nil, fmt.Errorf("unknown storage type %q", cfg.Type)
	}
}
