package backup

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/tarantool/tt/sdk/backup/storage"
	"github.com/tarantool/tt/sdk/backup/storage/fs"
	"github.com/tarantool/tt/sdk/backup/storage/s3"
	"gopkg.in/yaml.v3"
)

var (
	errMissingAWSCredential = errors.New("is not set")
	errInvalidStorageScope  = errors.New("invalid")
	errStorageEnvironment   = errors.New("environment")
	errEmptyStorageURI      = errors.New("storage URI is empty")
	errStorageScheme        = errors.New("unsupported storage scheme")
	errFileStorageHost      = errors.New("file storage URI must not contain a host")
	errRelativeStoragePath  = errors.New(
		"file storage URI must contain an absolute path",
	)
	errStoragePath       = errors.New("file storage path")
	errMissingS3Endpoint = errors.New(
		"s3 storage URI must contain an endpoint (host[:port])",
	)
	errMissingS3BucketPath = errors.New(
		"s3 storage URI must contain a bucket name in the path",
	)
	errMissingS3BucketName = errors.New("s3 storage URI must contain a bucket name")
	errMissingS3Parameter  = errors.New("s3 storage URI must contain")
	errS3URICredentials    = errors.New(
		"s3 storage URI must not carry credentials: they are read from",
	)
	errS3URIQuery = errors.New(
		"s3 storage URI must not carry query parameters: use " +
			"s3+http(s)://<endpoint>/<bucket> to write the endpoint and the credentials " +
			"in the URI",
	)
	errMissingS3Bucket = errors.New(
		"s3 storage URI must contain a bucket: s3://<bucket>/<prefix>",
	)
	errS3URIEndpoint = errors.New(
		"s3 storage URI names a bucket, not an endpoint: set",
	)
	errUnresolvedS3Endpoint     = errors.New("cannot resolve the s3 endpoint: set")
	errMissingStorageConfigPath = errors.New(
		"\"@\" must be followed by the path of a yaml file",
	)
	errEmptyStorageConfig       = errors.New("config is empty")
	errStorageConfigLine        = errors.New("line")
	errStorageConfigType        = errors.New("unsupported type")
	errMissingStorageConfigType = errors.New("config must set \"type\" (")
	errMissingStorageBucket     = errors.New("\"bucket\" is required for type")
	errConflictingStorageTLS    = errors.New(
		"\"ca_cert\" and \"skip_verify\" are mutually exclusive",
	)
	errStorageTLSWithoutHTTPS = errors.New(
		"\"ca_cert\" and \"skip_verify\" need an https endpoint",
	)
	errMissingStorageRoot = errors.New("\"root\" is required for type")
	errStorageRoot        = errors.New("root")
	errMissingS3SecretKey = errors.New(
		"\"access_key_id\" is set without \"secret_access_key\"",
	)
	errMissingS3AccessKey = errors.New(
		"\"secret_access_key\" is set without \"access_key_id\"",
	)
	errMissingStorageEndpoint = errors.New("\"endpoint\" is required for type")
	errStorageEndpointPath    = errors.New(
		"endpoint must be a host[:port], without a path",
	)
	errInvalidStorageEndpoint     = errors.New("endpoint is not a valid url")
	errStorageEndpointCredentials = errors.New(
		"endpoint must not carry credentials, use access_key_id/secret_access_key",
	)
	errMissingStorageEndpointHost = errors.New("endpoint must contain a host")
	errStorageEndpointForm        = errors.New(
		"endpoint must be a scheme and a host, without a path",
	)
	errStorageEndpointScheme   = errors.New("unsupported endpoint scheme")
	errStorageEnvVariable      = errors.New("environment variable")
	errUnterminatedEnvVariable = errors.New("unterminated \"${\": expected a closing \"}\"")
	errInvalidEnvVariableName  = errors.New("invalid environment variable name")
	errMissingEnvVariableName  = errors.New(
		"a \"$\" must be followed by a variable name; write \"$$\" for a literal \"$\"",
	)
	errUnknownStorageType = errors.New("unknown storage type")
)

// StorageType is the type of a backup storage backend.
type StorageType string

const (
	StorageTypeFile StorageType = "file"
	StorageTypeS3   StorageType = "s3"
)

// Schemes of the short (URI) form. s3+http(s) is an extension beyond the
// spec: it writes in the URI the endpoint and the credentials that plain
// s3:// takes from the environment.
const (
	schemeS3HTTP  = "s3+http"
	schemeS3HTTPS = "s3+https"
	schemeFTP     = "ftp"
)

// Query parameters of the s3+http(s) form.
const (
	paramAccessKey = "AccessKeyID"
	paramSecretKey = "SecretAccessKey"
	paramRegion    = "Region"
	paramPrefix    = "Prefix"
)

// storageFileMark introduces the full form: --backup-storage=@<path>.yaml.
const storageFileMark = "@"

// Backends of the full form, named by its "type" field. They match the URI
// schemes except for the filesystem, which the spec spells "fs" in a file and
// "file" in a URI.
const (
	fileTypeFS = "fs"
)

// Standard AWS variables the short s3:// form is configured by. The
// service-specific endpoint override wins over the global one, as in the AWS
// SDKs themselves.
const (
	envAccessKeyID     = "AWS_ACCESS_KEY_ID"
	envSecretAccessKey = "AWS_SECRET_ACCESS_KEY" //nolint:gosec // Environment variable name.
	envSessionToken    = "AWS_SESSION_TOKEN"     //nolint:gosec // Environment variable name.
	envRegion          = "AWS_REGION"
	envDefaultRegion   = "AWS_DEFAULT_REGION"
	envEndpointURLS3   = "AWS_ENDPOINT_URL_S3"
	envEndpointURL     = "AWS_ENDPOINT_URL"
	envCABundle        = "AWS_CA_BUNDLE"
)

// errFTPNotSupported answers an ftp storage by name. The spec defines the
// backend, tt does not implement it yet, and an ignored "type: ftp" would send
// the backups to whatever the rest of the config happens to describe.
var errFTPNotSupported = errors.New("ftp storage is not supported yet")

// StorageConfig is a parsed backup storage configuration.
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
	SessionToken    string
	UseSSL          bool
	// CACert is a PEM file holding the certificate authority of an endpoint the
	// system roots do not cover; SkipVerify drops certificate checks entirely.
	CACert     string
	SkipVerify bool
	// Common.
	Prefix string
}

// Scope narrows the config to one cluster in one environment, the
// <storage_root>/<cluster_name>/<environment>/ layout of the RFC. It appends
// the two segments to whatever prefix the URI already carried, so a storage
// pointed at a subtree and a storage scoped by the flags compose.
//
// Every command that touches a storage takes the same two flags, because a
// backup written into the subtree has to be readable from it: a verify or a gc
// aimed at the root of a storage laid out this way sees nothing at all, and
// reports a healthy storage or a clean sweep for it.
//
// An environment without a cluster name is refused rather than dropped: the
// pair names one directory level each, and honouring only the second one would
// silently read or write somewhere else entirely. So is a value that is not
// one path component, or is nothing but whitespace -- a variable the caller
// meant to expand and did not would otherwise name a directory of blanks that
// only a run with the same unexpanded variable ever finds again.
//
// The two are named as fields rather than as flags, because the values reach
// here from a flag on one command and out of a plan file on another; the
// caller says which by wrapping the error.
func (cfg *StorageConfig) Scope(clusterName, environment string) error {
	for _, field := range []struct{ name, value string }{
		{"cluster_name", clusterName},
		{"environment", environment},
	} {
		trimmed := strings.TrimSpace(field.value)

		switch {
		case field.value == "":
			continue
		case trimmed == "":
			return fmt.Errorf("%w %s %q: a storage path component cannot be "+
				"blank; an unexpanded variable looks like this",
				errInvalidStorageScope,
				field.name,
				field.value)
		case strings.ContainsAny(trimmed, `/\`), trimmed == ".", trimmed == "..":
			return fmt.Errorf("%w %s %q: it is one storage path component, so it "+
				"must not contain a path separator", errInvalidStorageScope, field.name, trimmed)
		}
	}

	clusterName = strings.TrimSpace(clusterName)
	environment = strings.TrimSpace(environment)

	if clusterName == "" {
		if environment != "" {
			return fmt.Errorf("%w %q needs a cluster name: the two name one "+
				"storage path component each", errStorageEnvironment, environment)
		}

		return nil
	}

	cfg.Prefix = path.Join(cfg.Prefix, clusterName, environment)

	return nil
}

// ParseStorageURI parses a --backup-storage value into a StorageConfig.
//
// The value is either a URI naming the backend by its scheme, or "@" followed
// by the path of a YAML config file (see ParseStorageFile).
//
//	file://<abs_path>[?Prefix=<subdir>]
//	    Local filesystem storage. The path must be absolute. Query parameters:
//	      Prefix           - subdirectory within the path (optional).
//
//	s3://<bucket>[/<prefix>]
//	    S3 storage configured by the standard AWS_* variables: credentials and
//	    TLS are not written in the URI, which is what a command line, a process
//	    listing and a cron log all show.
//
//	s3+https://<endpoint>/<bucket>[/<prefix>]?Region=...&AccessKeyID=...&SecretAccessKey=...
//	s3+http://<endpoint>/<bucket>[/<prefix>]?Region=...&AccessKeyID=...&SecretAccessKey=...
//	    S3-compatible storage with the endpoint and the credentials in the URI.
//	    Use s3+https for TLS, s3+http for plain TCP. The first path segment
//	    after the host is the bucket name, the rest is the optional key prefix.
//	    Query parameters:
//	      Region           - AWS region (optional).
//	      AccessKeyID      - access key ID (required).
//	      SecretAccessKey  - secret access key (required).
func ParseStorageURI(rawURI string) (*StorageConfig, error) {
	rawURI = strings.TrimSpace(rawURI)
	if rawURI == "" {
		return nil, errEmptyStorageURI
	}

	if path, ok := strings.CutPrefix(rawURI, storageFileMark); ok {
		cfg, err := ParseStorageFile(path)
		if err != nil {
			return nil, fmt.Errorf("parse storage config file: %w", err)
		}

		return cfg, nil
	}

	uValue, err := url.Parse(rawURI)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}

		return nil, fmt.Errorf("failed to parse storage URI: %w", err)
	}

	switch uValue.Scheme {
	case string(StorageTypeFile):
		cfg, err := parseFileURI(uValue)
		if err != nil {
			return nil, fmt.Errorf("parse file storage URI: %w", err)
		}

		return cfg, nil
	case string(StorageTypeS3):
		cfg, err := parseS3EnvURI(uValue)
		if err != nil {
			return nil, fmt.Errorf("parse s3 storage URI: %w", err)
		}

		return cfg, nil
	case schemeS3HTTPS, schemeS3HTTP:
		cfg, err := parseS3URI(uValue)
		if err != nil {
			return nil, fmt.Errorf("parse s3 storage URI: %w", err)
		}

		return cfg, nil
	case schemeFTP:
		return nil, errFTPNotSupported
	default:
		return nil, fmt.Errorf(
			"%w %q: expected %q, %q, %q, or @<path> naming a "+
				"config file", errStorageScheme,
			uValue.Scheme, StorageTypeFile, StorageTypeS3, "s3+http(s)")
	}
}

func parseFileURI(parsedURL *url.URL) (*StorageConfig, error) {
	if parsedURL.Host != "" {
		return nil, fmt.Errorf(
			"%w %q, use file:///<absolute-path>", errFileStorageHost,
			parsedURL.Host)
	}

	if parsedURL.Path == "" {
		return nil, errRelativeStoragePath
	}

	if !strings.HasPrefix(parsedURL.Path, "/") {
		return nil,
			fmt.Errorf("%w %q must be absolute",
				errStoragePath,
				parsedURL.Path)
	}

	return &StorageConfig{
		Type:            StorageTypeFile,
		Path:            parsedURL.Path,
		Prefix:          parsedURL.Query().Get(paramPrefix),
		Endpoint:        "",
		Bucket:          "",
		Region:          "",
		AccessKeyID:     "",
		SecretAccessKey: "",
		SessionToken:    "",
		UseSSL:          false,
		CACert:          "",
		SkipVerify:      false,
	}, nil
}

func parseS3URI(parsedURL *url.URL) (*StorageConfig, error) {
	endpoint := parsedURL.Host
	if endpoint == "" {
		return nil, errMissingS3Endpoint
	}

	trimmed := strings.TrimPrefix(parsedURL.Path, "/")
	if trimmed == "" {
		return nil, errMissingS3BucketPath
	}

	bucket, prefix, _ := strings.Cut(trimmed, "/")
	if bucket == "" {
		return nil, errMissingS3BucketName
	}

	query := parsedURL.Query()
	accessKeyID := query.Get(paramAccessKey)
	secretAccessKey := query.Get(paramSecretKey)

	if accessKeyID == "" {
		return nil,
			fmt.Errorf("%w %s query parameter",
				errMissingS3Parameter,
				paramAccessKey)
	}

	if secretAccessKey == "" {
		return nil,
			fmt.Errorf("%w %s query parameter",
				errMissingS3Parameter,
				paramSecretKey)
	}

	return &StorageConfig{
		Type:            StorageTypeS3,
		Endpoint:        endpoint,
		Bucket:          bucket,
		Region:          query.Get(paramRegion),
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		UseSSL:          parsedURL.Scheme == schemeS3HTTPS,
		Prefix:          prefix,
		Path:            "",
		SessionToken:    "",
		CACert:          "",
		SkipVerify:      false,
	}, nil
}

// parseS3EnvURI parses the short s3://<bucket>[/<prefix>] form, where the URI
// carries the bucket and the prefix and nothing else: the endpoint, the region,
// the credentials and the CA bundle come from the standard AWS_* variables.
func parseS3EnvURI(parsedURL *url.URL) (*StorageConfig, error) {
	switch {
	case parsedURL.User != nil:
		return nil, fmt.Errorf(
			"%w %s and %s", errS3URICredentials,
			envAccessKeyID, envSecretAccessKey)
	case parsedURL.RawQuery != "":
		// Never echoed back: such a value is usually an s3+http(s) URI with the
		// scheme mistyped, and its parameters carry the secret key.
		return nil, errS3URIQuery
	case parsedURL.Host == "":
		return nil, errMissingS3Bucket
	case parsedURL.Port() != "":
		return nil, fmt.Errorf(
			"%w %s for an s3-compatible storage, or use "+
				"s3+http(s)://<endpoint>/<bucket>", errS3URIEndpoint,
			envEndpointURL)
	}

	accessKeyID, secretAccessKey, sessionToken, err := awsEnvCredentials()
	if err != nil {
		return nil, err
	}

	_, region := lookupEnv(envRegion, envDefaultRegion)

	endpoint, useSSL, err := awsEnvEndpoint(region)
	if err != nil {
		return nil, err
	}

	// Unlike the config file's ca_cert, the bundle is ambient configuration, so
	// it is only honored where it can do anything at all: a plaintext endpoint
	// is not a misconfiguration to report.
	caCert := ""
	if useSSL {
		caCert = os.Getenv(envCABundle)
	}

	return &StorageConfig{
		Type:            StorageTypeS3,
		Endpoint:        endpoint,
		Bucket:          parsedURL.Host,
		Region:          region,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
		UseSSL:          useSSL,
		CACert:          caCert,
		Prefix:          strings.Trim(parsedURL.Path, "/"), Path: "", SkipVerify: false,
	}, nil
}

// awsEnvCredentials reads the credentials from the standard AWS_* variables.
// A missing one is reported by name: an empty credential is not rejected by the
// storage as missing, it comes back as an opaque 403.
func awsEnvCredentials() (string, string, string, error) {
	var (
		accessKeyID     string
		secretAccessKey string
	)

	accessKeyID = os.Getenv(envAccessKeyID)
	secretAccessKey = os.Getenv(envSecretAccessKey)

	switch {
	case accessKeyID == "":
		return "", "", "", fmt.Errorf("%s %w", envAccessKeyID, errMissingAWSCredential)
	case secretAccessKey == "":
		return "", "", "", fmt.Errorf("%s %w", envSecretAccessKey, errMissingAWSCredential)
	}

	return accessKeyID, secretAccessKey, os.Getenv(envSessionToken), nil
}

// awsEnvEndpoint resolves what the short form connects to: the explicit
// override of an S3-compatible storage, the regional AWS endpoint otherwise.
func awsEnvEndpoint(region string) (string, bool, error) {
	if name, raw := lookupEnv(envEndpointURLS3, envEndpointURL); raw != "" {
		endpoint, useSSL, err := parseS3Endpoint(raw)
		if err != nil {
			return "", false, fmt.Errorf("invalid %s: %w", name, err)
		}

		return endpoint, useSSL, nil
	}

	if region == "" {
		return "", false, fmt.Errorf(
			"%w %s for aws, or %s for an s3-compatible storage",
			errUnresolvedS3Endpoint,
			envRegion,
			envEndpointURL)
	}

	return "s3." + region + ".amazonaws.com", true, nil
}

// lookupEnv returns the first variable of names that is set to a non-empty
// value, together with its name for the error message that reports it.
func lookupEnv(names ...string) (string, string) {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return name, value
		}
	}

	return names[len(names)-1], ""
}

// ParseStorageFile reads the full form of a storage configuration: a YAML file
// whose "type" field names the backend and decides what the other fields mean.
func ParseStorageFile(path string) (*StorageConfig, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errMissingStorageConfigPath
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read storage config: %w", err)
	}

	cfg, err := parseStorageConfig(data)
	if err != nil {
		return nil, fmt.Errorf("invalid storage config %q: %w", path, err)
	}

	return cfg, nil
}

// parseStorageConfig decodes the full form from YAML.
func parseStorageConfig(data []byte) (*StorageConfig, error) {
	var doc yaml.Node

	unmarshalErr := yaml.Unmarshal(data, &doc)
	if unmarshalErr != nil {
		return nil, fmt.Errorf("failed to decode yaml: %w", unmarshalErr)
	}

	if doc.Kind == 0 || len(doc.Content) == 0 {
		return nil, errEmptyStorageConfig
	}

	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil,
			fmt.Errorf("%w %d: config must be a mapping of fields",
				errStorageConfigLine,
				root.Line)
	}

	// Substitution happens before any field is read, so a value is what the
	// backend gets whether it was written in the file or in the environment.
	expandNodeEnvErr := expandNodeEnv(root)
	if expandNodeEnvErr != nil {
		return nil, expandNodeEnvErr
	}

	backend, err := storageConfigType(root)
	if err != nil {
		return nil, err
	}

	switch backend {
	case string(StorageTypeS3):
		return parseS3Config(root)
	case fileTypeFS:
		return parseFSConfig(root)
	case schemeFTP:
		return nil, errFTPNotSupported
	default:
		return nil, fmt.Errorf("%w %q: expected %q or %q", errStorageConfigType,
			backend, StorageTypeS3, fileTypeFS)
	}
}

// storageConfigType reads the "type" discriminator.
func storageConfigType(root *yaml.Node) (string, error) {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "type" {
			continue
		}

		value := root.Content[i+1]
		if value.Kind != yaml.ScalarNode {
			return "",
				fmt.Errorf("%w %d: \"type\" must be a backend name",
					errStorageConfigLine,
					value.Line)
		}

		return value.Value, nil
	}

	return "",
		fmt.Errorf("%w%q or %q)",
			errMissingStorageConfigType,
			StorageTypeS3,
			fileTypeFS)
}

// s3Config is the full form of an S3-compatible storage configuration.
type s3Config struct {
	Type            string `yaml:"type"`
	Endpoint        string `yaml:"endpoint"`
	Region          string `yaml:"region"`
	Bucket          string `yaml:"bucket"`
	Prefix          string `yaml:"prefix"`
	AccessKeyID     string `yaml:"access_key_id"`
	SecretAccessKey string `yaml:"secret_access_key"`
	CACert          string `yaml:"ca_cert"`
	SkipVerify      bool   `yaml:"skip_verify"`
}

// fsConfig is the full form of a local filesystem storage configuration.
type fsConfig struct {
	Type   string `yaml:"type"`
	Root   string `yaml:"root"`
	Prefix string `yaml:"prefix"`
}

func parseS3Config(root *yaml.Node) (*StorageConfig, error) {
	var config s3Config

	decodeStorageConfigErr := decodeStorageConfig(root, &config)
	if decodeStorageConfigErr != nil {
		return nil, decodeStorageConfigErr
	}

	endpoint, useSSL, err := parseS3Endpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(config.Bucket) == "" {
		return nil, fmt.Errorf("%w %q", errMissingStorageBucket, StorageTypeS3)
	}

	accessKeyID, secretAccessKey, sessionToken, err := s3ConfigCredentials(config)
	if err != nil {
		return nil, err
	}

	switch {
	case config.CACert != "" && config.SkipVerify:
		return nil, errConflictingStorageTLS
	case !useSSL && (config.CACert != "" || config.SkipVerify):
		return nil, errStorageTLSWithoutHTTPS
	}

	return &StorageConfig{
		Type:            StorageTypeS3,
		Endpoint:        endpoint,
		Bucket:          config.Bucket,
		Region:          config.Region,
		AccessKeyID:     accessKeyID,
		SecretAccessKey: secretAccessKey,
		SessionToken:    sessionToken,
		UseSSL:          useSSL,
		CACert:          config.CACert,
		SkipVerify:      config.SkipVerify,
		Prefix:          config.Prefix, Path: "",
	}, nil
}

func parseFSConfig(root *yaml.Node) (*StorageConfig, error) {
	var config fsConfig

	err := decodeStorageConfig(root, &config)
	if err != nil {
		return nil, err
	}

	path := strings.TrimSpace(config.Root)
	switch {
	case path == "":
		return nil, fmt.Errorf("%w %q", errMissingStorageRoot, fileTypeFS)
	case !filepath.IsAbs(path):
		// A relative root resolves against the working directory of whoever
		// runs the command rather than against the config file, so the same
		// config would name a different storage per caller.
		return nil, fmt.Errorf("%w %q must be absolute", errStorageRoot, path)
	}

	return &StorageConfig{
		Type:            StorageTypeFile,
		Path:            path,
		Prefix:          config.Prefix,
		Endpoint:        "",
		Bucket:          "",
		Region:          "",
		AccessKeyID:     "",
		SecretAccessKey: "",
		SessionToken:    "",
		UseSSL:          false,
		CACert:          "",
		SkipVerify:      false,
	}, nil
}

// s3ConfigCredentials takes the credentials from the config, or, when it names
// neither, from the standard AWS_* variables. Half a pair is an error: pairing
// a written key with a secret from the environment is nobody's intent.
func s3ConfigCredentials(config s3Config) (string, string, string, error) {
	switch {
	case config.AccessKeyID != "" && config.SecretAccessKey != "":
		return config.AccessKeyID, config.SecretAccessKey, "", nil
	case config.AccessKeyID != "":
		return "", "", "", errMissingS3SecretKey
	case config.SecretAccessKey != "":
		return "", "", "", errMissingS3AccessKey
	}

	return awsEnvCredentials()
}

// parseS3Endpoint splits an endpoint into the host[:port] the client connects
// to and whether it speaks TLS. A bare host means TLS: plaintext has to be
// asked for with an explicit http://.
func parseS3Endpoint(endpoint string) (string, bool, error) {
	var err error

	endpoint = strings.TrimSpace(endpoint)

	if endpoint == "" {
		return "",
			false,
			fmt.Errorf("%w %q",
				errMissingStorageEndpoint,
				StorageTypeS3)
	}

	if !strings.Contains(endpoint, "://") {
		if strings.ContainsRune(endpoint, '/') {
			return "", false, errStorageEndpointPath
		}

		return endpoint, true, nil
	}

	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return "", false, errInvalidStorageEndpoint
	}

	switch {
	case parsedURL.User != nil:
		return "", false, errStorageEndpointCredentials
	case parsedURL.Host == "":
		return "", false, errMissingStorageEndpointHost
	case parsedURL.Path != "" && parsedURL.Path != "/", parsedURL.RawQuery != "":
		return "", false, errStorageEndpointForm
	}

	switch parsedURL.Scheme {
	case "https":
		return parsedURL.Host, true, nil
	case "http":
		return parsedURL.Host, false, nil
	default:
		return "", false, fmt.Errorf(
			"%w %q: expected \"http\" or \"https\"",
			errStorageEndpointScheme,
			parsedURL.Scheme)
	}
}

// decodeStorageConfig decodes the config mapping into out, rejecting any field
// out does not declare. A typo'd secret_access_key would otherwise decode into
// a config with no credentials at all, which is the mistake the full form
// exists to prevent.
func decodeStorageConfig(root *yaml.Node, out any) error {
	known := yamlFieldNames(out)

	for i := 0; i+1 < len(root.Content); i += 2 {
		key := root.Content[i]
		if _, ok := known[key.Value]; !ok {
			return fmt.Errorf("%w %d: unknown field %q", errStorageConfigLine, key.Line, key.Value)
		}
	}

	err := root.Decode(out)
	if err != nil {
		return fmt.Errorf("failed to decode config: %w", err)
	}

	return nil
}

// yamlFieldNames returns the field names the struct behind out accepts, so that
// the known set cannot drift away from the struct it is checked against.
func yamlFieldNames(out any) map[string]struct{} {
	structType := reflect.TypeOf(out).Elem()

	names := make(map[string]struct{}, structType.NumField())
	for field := range structType.Fields() {
		name, _, _ := strings.Cut(field.Tag.Get("yaml"), ",")

		if name == "" {
			name = strings.ToLower(field.Name)
		}

		names[name] = struct{}{}
	}

	return names
}

// expandNodeEnv substitutes the environment into every string value of the
// tree. Keys are left alone: a field named by the environment would be a
// puzzle rather than a feature.
//
// Only values the parser already read as strings are touched, and they stay
// strings afterwards: re-resolving the type of a substituted value would turn
// a numeric access key into an integer that no longer decodes into its field.
func expandNodeEnv(node *yaml.Node) error {
	switch node.Kind {
	case yaml.AliasNode:
		return nil
	case yaml.MappingNode:
		for i := 1; i < len(node.Content); i += 2 {
			err := expandNodeEnv(node.Content[i])
			if err != nil {
				return err
			}
		}
	case yaml.DocumentNode, yaml.SequenceNode:
		for _, child := range node.Content {
			err := expandNodeEnv(child)
			if err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		if node.Tag != "!!str" {
			return nil
		}

		value, err := expandEnv(node.Value)
		if err != nil {
			return fmt.Errorf("line %d: %w", node.Line, err)
		}

		node.Value = value
	}

	return nil
}

// expandEnv substitutes ${VAR} and $VAR with the environment, so that a secret
// stays out of a config file kept under git. An unset variable is an error
// rather than an empty string: an empty credential is not refused as missing by
// the storage, it comes back as an opaque 403. A literal dollar is "$$".
func expandEnv(value string) (string, error) {
	var expanded strings.Builder

	for index := 0; index < len(value); {
		if value[index] != '$' {
			expanded.WriteByte(value[index])

			index++

			continue
		}

		if index+1 < len(value) && value[index+1] == '$' {
			expanded.WriteByte('$')

			index += 2

			continue
		}

		name, width, err := envVarName(value[index+1:])
		if err != nil {
			return "", err
		}

		// An empty value is refused as loudly as a missing one. A config that
		// names a credential through ${VAR} and gets nothing back would fall
		// through to the ambient AWS_* pair, so the storage would be reached
		// with credentials the config does not mention. Somebody who wants a
		// field empty leaves it out.
		value, ok := os.LookupEnv(name)
		switch {
		case !ok:
			return "", fmt.Errorf("%w %q is not set", errStorageEnvVariable, name)
		case value == "":
			return "", fmt.Errorf("%w %q is empty", errStorageEnvVariable, name)
		}

		expanded.WriteString(value)

		index += 1 + width
	}

	return expanded.String(), nil
}

// envVarName reads the variable name that follows a "$", in either the ${VAR}
// or the $VAR form, and returns how many bytes of s it spans.
func envVarName(value string) (string, int, error) {
	var (
		name  string
		width int
	)

	if strings.HasPrefix(value, "{") {
		end := strings.IndexByte(value, '}')
		if end < 0 {
			return "", 0, errUnterminatedEnvVariable
		}

		name = value[1:end]
		if !isEnvVarName(name) {
			return "", 0, fmt.Errorf("%w %q", errInvalidEnvVariableName, name)
		}

		return name, end + 1, nil
	}

	width = 0
	for width < len(value) && isEnvVarChar(value[width]) {
		width++
	}

	name = value[:width]
	if !isEnvVarName(name) {
		// Deliberately quotes nothing of the value around it: this fires on
		// secrets holding a dollar, which is what "$$" is for.
		return "", 0, errMissingEnvVariableName
	}

	return name, width, nil
}

// isEnvVarName reports whether name is a shell-portable variable name.
func isEnvVarName(name string) bool {
	if name == "" || (name[0] >= '0' && name[0] <= '9') {
		return false
	}

	for i := range len(name) {
		if !isEnvVarChar(name[i]) {
			return false
		}
	}

	return true
}

func isEnvVarChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_':
		return true
	default:
		return false
	}
}

// OpenStorage creates a backup storage backend from a StorageConfig.
func OpenStorage(cfg *StorageConfig) (storage.Storage, error) {
	switch cfg.Type {
	case StorageTypeFile:
		stValue, err := fs.New(fs.Config{Path: cfg.Path, Prefix: cfg.Prefix})
		if err != nil {
			return nil, fmt.Errorf("create file storage: %w", err)
		}

		return stValue, nil
	case StorageTypeS3:
		store, err := s3.New(s3.Config{
			Endpoint:        cfg.Endpoint,
			Bucket:          cfg.Bucket,
			Region:          cfg.Region,
			AccessKeyID:     cfg.AccessKeyID,
			SecretAccessKey: cfg.SecretAccessKey,
			SessionToken:    cfg.SessionToken,
			UseSSL:          cfg.UseSSL,
			CACert:          cfg.CACert,
			SkipVerify:      cfg.SkipVerify,
			Prefix:          cfg.Prefix,
		})
		if err != nil {
			return nil, fmt.Errorf("create s3 storage: %w", err)
		}

		return store, nil
	default:
		return nil, fmt.Errorf("%w %q", errUnknownStorageType, cfg.Type)
	}
}
