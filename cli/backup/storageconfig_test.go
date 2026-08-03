package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/backup/storage"
)

// awsEnvNames is every variable the parser reads. A case starts from a known
// empty set of them: an AWS credential in the developer's shell must not be
// what decides whether a config file parses.
var awsEnvNames = []string{
	envAccessKeyID,
	envSecretAccessKey,
	envSessionToken,
	envRegion,
	envDefaultRegion,
	envEndpointURLS3,
	envEndpointURL,
	envCABundle,
}

// unsetEnv removes name for the duration of the test. t.Setenv registers the
// restore, os.Unsetenv then makes the variable genuinely absent rather than set
// to an empty string, which the parser tells apart.
func unsetEnv(t *testing.T, name string) {
	t.Helper()

	t.Setenv(name, "")
	require.NoError(t, os.Unsetenv(name))
}

// clearAWSEnv empties the ambient AWS configuration.
func clearAWSEnv(t *testing.T) {
	t.Helper()

	for _, name := range awsEnvNames {
		unsetEnv(t, name)
	}
}

// awsEnv installs exactly the AWS variables a case declares.
func awsEnv(t *testing.T, vars map[string]string) {
	t.Helper()

	clearAWSEnv(t)
	for name, value := range vars {
		t.Setenv(name, value)
	}
}

// storageConfigPath writes a storage config file and returns its path.
func storageConfigPath(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "storage.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	return path
}

// storageConfigURI is the --backup-storage value naming such a file.
func storageConfigURI(t *testing.T, body string) string {
	t.Helper()

	return storageFileMark + storageConfigPath(t, body)
}

// TestParseStorageFile_S3 checks that every field of the full form reaches the
// config, and that a bare endpoint means TLS.
func TestParseStorageFile_S3(t *testing.T) {
	clearAWSEnv(t)

	cfg, err := ParseStorageURI(storageConfigURI(t, `
type: s3
endpoint: https://storage.yandexcloud.net
region: ru-central1
bucket: payments-backups
prefix: tarantool
access_key_id: AKIAEXAMPLE
secret_access_key: s3cr3t
ca_cert: /etc/ssl/private-ca.pem
`))

	require.NoError(t, err)
	assert.Equal(t, &StorageConfig{
		Type:            StorageTypeS3,
		Endpoint:        "storage.yandexcloud.net",
		Bucket:          "payments-backups",
		Region:          "ru-central1",
		AccessKeyID:     "AKIAEXAMPLE",
		SecretAccessKey: "s3cr3t",
		UseSSL:          true,
		CACert:          "/etc/ssl/private-ca.pem",
		Prefix:          "tarantool",
	}, cfg)
}

// TestParseStorageFile_S3Endpoint pins how the endpoint decides TLS: a bare
// host is https, plaintext has to be asked for.
func TestParseStorageFile_S3Endpoint(t *testing.T) {
	clearAWSEnv(t)

	cases := []struct {
		name         string
		endpoint     string
		wantEndpoint string
		wantSSL      bool
		wantErr      string
	}{
		{
			name:         "bare host",
			endpoint:     "s3.example.com",
			wantEndpoint: "s3.example.com",
			wantSSL:      true,
		},
		{
			name:         "host and port",
			endpoint:     "s3.example.com:9000",
			wantEndpoint: "s3.example.com:9000",
			wantSSL:      true,
		},
		{
			name:         "https",
			endpoint:     "https://s3.example.com",
			wantEndpoint: "s3.example.com",
			wantSSL:      true,
		},
		{
			name:         "http",
			endpoint:     "http://localhost:9000",
			wantEndpoint: "localhost:9000",
			wantSSL:      false,
		},
		{name: "with a path", endpoint: "s3.example.com/bucket", wantErr: "without a path"},
		{name: "unsupported scheme", endpoint: "ftp://s3.example.com", wantErr: "endpoint scheme"},
		{
			name:     "with credentials",
			endpoint: "https://key:s3cr3t@s3.example.com",
			wantErr:  "must not carry credentials",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(storageConfigURI(t, "type: s3\n"+
				"endpoint: "+tc.endpoint+"\n"+
				"bucket: b\naccess_key_id: k\nsecret_access_key: s\n"))

			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Nil(t, cfg)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.NotContains(t, err.Error(), "s3cr3t")
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantEndpoint, cfg.Endpoint)
			assert.Equal(t, tc.wantSSL, cfg.UseSSL)
		})
	}
}

// TestParseStorageFile_S3TLSOptions checks the two ways of trusting an endpoint
// are not silently combined or silently dropped.
func TestParseStorageFile_S3TLSOptions(t *testing.T) {
	clearAWSEnv(t)

	cases := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name:    "ca_cert with skip_verify",
			config:  "endpoint: s3.example.com\nca_cert: /ca.pem\nskip_verify: true\n",
			wantErr: "mutually exclusive",
		},
		{
			name:    "ca_cert over http",
			config:  "endpoint: http://s3.example.com\nca_cert: /ca.pem\n",
			wantErr: "need an https endpoint",
		},
		{
			name:    "skip_verify over http",
			config:  "endpoint: http://s3.example.com\nskip_verify: true\n",
			wantErr: "need an https endpoint",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(storageConfigURI(t,
				"type: s3\nbucket: b\naccess_key_id: k\nsecret_access_key: s\n"+tc.config))

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestParseStorageFile_S3TakesEnvCredentials checks a config that names no
// credentials falls back to the standard AWS variables, so a file kept under
// git holds no secret at all.
func TestParseStorageFile_S3TakesEnvCredentials(t *testing.T) {
	awsEnv(t, map[string]string{
		envAccessKeyID:     "AKIAENV",
		envSecretAccessKey: "envsecret",
		envSessionToken:    "envtoken",
	})

	cfg, err := ParseStorageURI(storageConfigURI(t,
		"type: s3\nendpoint: s3.example.com\nbucket: b\n"))

	require.NoError(t, err)
	assert.Equal(t, "AKIAENV", cfg.AccessKeyID)
	assert.Equal(t, "envsecret", cfg.SecretAccessKey)
	assert.Equal(t, "envtoken", cfg.SessionToken)
}

func TestParseStorageFile_FS(t *testing.T) {
	cfg, err := ParseStorageURI(storageConfigURI(t, `
type: fs
root: /mnt/backups/payments
prefix: mycluster
`))

	require.NoError(t, err)
	assert.Equal(t, &StorageConfig{
		Type:   StorageTypeFile,
		Path:   "/mnt/backups/payments",
		Prefix: "mycluster",
	}, cfg)
}

// TestParseStorageFile_FSOpensStorage checks the full form reaches a working
// storage rather than only a struct that looks right.
func TestParseStorageFile_FSOpensStorage(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	cfg, err := ParseStorageURI(storageConfigURI(t,
		"type: fs\nroot: "+root+"\nprefix: mycluster\n"))
	require.NoError(t, err)

	store, err := OpenStorage(cfg)
	require.NoError(t, err)

	key := storage.ManifestKey("2026-01-01-full")
	require.NoError(t, storage.PutBytes(ctx, store, key, []byte("{}")))

	objects, err := store.List(ctx, storage.ManifestsPrefix())
	require.NoError(t, err)
	require.Len(t, objects, 1)
	assert.Equal(t, key, objects[0].Key)
	// The prefix is a subdirectory of the root, not a part of the object key.
	assert.FileExists(t, filepath.Join(root, "mycluster", "manifests", "2026-01-01-full.json"))
}

// TestParseStorageFile_FTP checks the backend the spec defines and tt does not
// implement is answered by name: an ignored "type: ftp" would send the backups
// to whatever the rest of the config happens to describe.
func TestParseStorageFile_FTP(t *testing.T) {
	cfg, err := ParseStorageURI(storageConfigURI(t, `
type: ftp
host: backups.example.com
root: /payments
`))

	require.ErrorIs(t, err, errFTPNotSupported)
	assert.Nil(t, cfg)
}

func TestParseStorageFile_UnknownType(t *testing.T) {
	cases := []struct {
		name   string
		config string
	}{
		{name: "another backend", config: "type: gcs\nbucket: b\n"},
		{name: "uri scheme spelling", config: "type: s3+https\nbucket: b\n"},
		{name: "file instead of fs", config: "type: file\nroot: /backups\n"},
		{name: "empty", config: `type: ""` + "\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(storageConfigURI(t, tc.config))

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), "unsupported type")
		})
	}
}

func TestParseStorageFile_MalformedYAML(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		wantErr string
	}{
		{name: "unindented block", config: "type: s3\n  bucket: b\n", wantErr: "decode yaml"},
		{name: "unterminated quote", config: "type: \"s3\nbucket: b\n", wantErr: "decode yaml"},
		{name: "unterminated flow", config: "type: [s3\n", wantErr: "decode yaml"},
		{name: "tab indentation", config: "type: s3\n\tbucket: b\n", wantErr: "decode yaml"},
		{name: "empty file", config: "", wantErr: "config is empty"},
		{name: "comments only", config: "# nothing here\n", wantErr: "config is empty"},
		{name: "a list", config: "- type: s3\n", wantErr: "must be a mapping"},
		{name: "a scalar", config: "s3\n", wantErr: "must be a mapping"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(storageConfigURI(t, tc.config))

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestParseStorageFile_Missing checks a path that names nothing is reported as
// such, rather than read as an empty config.
func TestParseStorageFile_Missing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.yaml")

	cfg, err := ParseStorageURI(storageFileMark + path)

	require.ErrorIs(t, err, os.ErrNotExist)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), path)
}

// TestParseStorageFile_MissingPath checks a bare "@" is answered with what it
// has to be followed by.
func TestParseStorageFile_MissingPath(t *testing.T) {
	for _, uri := range []string{"@", "@   "} {
		t.Run(uri, func(t *testing.T) {
			cfg, err := ParseStorageURI(uri)

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), "yaml file")
		})
	}
}

// TestParseStorageFile_UnknownField checks a field the backend does not declare
// is an error. A typo'd secret_access_key would otherwise decode into a config
// with no credentials at all, which is the mistake the full form exists to
// prevent.
func TestParseStorageFile_UnknownField(t *testing.T) {
	clearAWSEnv(t)

	cases := []struct {
		name   string
		config string
		field  string
	}{
		{
			name: "misspelled secret",
			config: "type: s3\nendpoint: s3.example.com\nbucket: b\n" +
				"access_key_id: k\nsecret_acces_key: s3cr3t\n",
			field: "secret_acces_key",
		},
		{
			name:   "fs field in an s3 config",
			config: "type: s3\nendpoint: s3.example.com\nbucket: b\nroot: /backups\n",
			field:  "root",
		},
		{
			name:   "s3 field in an fs config",
			config: "type: fs\nroot: /backups\nbucket: b\n",
			field:  "bucket",
		},
		{
			name:   "a comment written as a field",
			config: "type: fs\nroot: /backups\ndescription: nightly\n",
			field:  "description",
		},
		{
			name:   "uri query parameter spelling",
			config: "type: fs\nroot: /backups\nPrefix: mycluster\n",
			field:  "Prefix",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(storageConfigURI(t, tc.config))

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), "unknown field")
			assert.Contains(t, err.Error(), tc.field)
			assert.NotContains(t, err.Error(), "s3cr3t")
		})
	}
}

// TestParseStorageFile_RequiredFieldMissing drops each required field in turn:
// the error has to name the field, because the operator's next action is to
// write it into the file.
func TestParseStorageFile_RequiredFieldMissing(t *testing.T) {
	clearAWSEnv(t)

	cases := []struct {
		name    string
		config  string
		wantErr string
	}{
		{
			name:    "no type",
			config:  "endpoint: s3.example.com\nbucket: b\n",
			wantErr: `must set "type"`,
		},
		{
			name:    "type is a mapping",
			config:  "type:\n  name: s3\n",
			wantErr: `"type" must be a backend name`,
		},
		{
			name:    "s3 without endpoint",
			config:  "type: s3\nbucket: b\naccess_key_id: k\nsecret_access_key: s\n",
			wantErr: `"endpoint" is required`,
		},
		{
			name: "s3 with a blank endpoint",
			config: "type: s3\nendpoint: \"   \"\nbucket: b\n" +
				"access_key_id: k\nsecret_access_key: s\n",
			wantErr: `"endpoint" is required`,
		},
		{
			name:    "s3 without bucket",
			config:  "type: s3\nendpoint: s3.example.com\naccess_key_id: k\nsecret_access_key: s\n",
			wantErr: `"bucket" is required`,
		},
		{
			name: "s3 with a blank bucket",
			config: "type: s3\nendpoint: s3.example.com\nbucket: \"  \"\n" +
				"access_key_id: k\nsecret_access_key: s\n",
			wantErr: `"bucket" is required`,
		},
		{
			name:    "s3 without secret_access_key",
			config:  "type: s3\nendpoint: s3.example.com\nbucket: b\naccess_key_id: k\n",
			wantErr: `"access_key_id" is set without "secret_access_key"`,
		},
		{
			name:    "s3 without access_key_id",
			config:  "type: s3\nendpoint: s3.example.com\nbucket: b\nsecret_access_key: s\n",
			wantErr: `"secret_access_key" is set without "access_key_id"`,
		},
		{
			name:    "s3 without any credential",
			config:  "type: s3\nendpoint: s3.example.com\nbucket: b\n",
			wantErr: envAccessKeyID + " is not set",
		},
		{
			name:    "fs without root",
			config:  "type: fs\nprefix: mycluster\n",
			wantErr: `"root" is required`,
		},
		{
			name:    "fs with a blank root",
			config:  "type: fs\nroot: \"  \"\n",
			wantErr: `"root" is required`,
		},
		{
			name:    "fs with a relative root",
			config:  "type: fs\nroot: backups/payments\n",
			wantErr: "must be absolute",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := ParseStorageURI(storageConfigURI(t, tc.config))

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

// TestParseStorageFile_ExpandsEnv covers the substitution grammar on the field
// it exists for.
func TestParseStorageFile_ExpandsEnv(t *testing.T) {
	const envName = "TT_TEST_BACKUP_SECRET"

	cases := []struct {
		name  string
		value string
		want  string
	}{
		{name: "braced", value: "${" + envName + "}", want: "s3cr3t"},
		{name: "bare", value: "$" + envName, want: "s3cr3t"},
		{name: "bare before a separator", value: "$" + envName + "-1", want: "s3cr3t-1"},
		{name: "only partly a variable", value: "AKIA${" + envName + "}==", want: "AKIAs3cr3t=="},
		{
			name:  "two variables",
			value: "${" + envName + "}${" + envName + "}",
			want:  "s3cr3ts3cr3t",
		},
		{name: "no variable", value: "written-in-the-file", want: "written-in-the-file"},
		{name: "a literal dollar", value: "pay$$day", want: "pay$day"},
		{name: "only a dollar", value: "$$", want: "$"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			awsEnv(t, nil)
			t.Setenv(envName, "s3cr3t")

			cfg, err := ParseStorageURI(storageConfigURI(t, "type: s3\n"+
				"endpoint: s3.example.com\nbucket: b\naccess_key_id: k\n"+
				"secret_access_key: \""+tc.value+"\"\n"))

			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.SecretAccessKey)
		})
	}
}

// TestParseStorageFile_ExpandsEnvEverywhere checks the substitution is not
// limited to the credentials: a value is what the backend gets whether it was
// written in the file or in the environment.
func TestParseStorageFile_ExpandsEnvEverywhere(t *testing.T) {
	awsEnv(t, nil)
	t.Setenv("TT_TEST_BACKUP_BUCKET", "payments-backups")
	t.Setenv("TT_TEST_BACKUP_ENV", "prod")
	t.Setenv("TT_TEST_BACKUP_HOST", "s3.example.com")

	cfg, err := ParseStorageURI(storageConfigURI(t, `
type: s3
endpoint: https://${TT_TEST_BACKUP_HOST}
bucket: ${TT_TEST_BACKUP_BUCKET}
prefix: tarantool/${TT_TEST_BACKUP_ENV}
access_key_id: k
secret_access_key: s
`))

	require.NoError(t, err)
	assert.Equal(t, "s3.example.com", cfg.Endpoint)
	assert.Equal(t, "payments-backups", cfg.Bucket)
	assert.Equal(t, "tarantool/prod", cfg.Prefix)
}

// TestParseStorageFile_UnsetEnv is the reason the form substitutes at all: an
// unset variable must stop the command and name itself, because an empty
// credential is not refused as missing by the storage, it comes back as an
// opaque 403 much later.
func TestParseStorageFile_UnsetEnv(t *testing.T) {
	const envName = "TT_TEST_BACKUP_ABSENT"

	cases := []struct {
		name  string
		value string
	}{
		{name: "braced", value: "${" + envName + "}"},
		{name: "bare", value: "$" + envName},
		{name: "only partly a variable", value: "AKIA${" + envName + "}"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			awsEnv(t, nil)
			unsetEnv(t, envName)

			cfg, err := ParseStorageURI(storageConfigURI(t, "type: s3\n"+
				"endpoint: s3.example.com\nbucket: b\naccess_key_id: k\n"+
				"secret_access_key: \""+tc.value+"\"\n"))

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), envName)
			assert.Contains(t, err.Error(), "not set")
		})
	}
}

// TestParseStorageFile_EmptyEnvCredential is the failure the substitution
// exists to prevent, one step removed: a variable that is set to an empty
// string - a systemd unit with a blank Environment=, a mounted secret that
// resolved to nothing - expands to "", the pair then reads as "the config names
// no credential", and the ambient AWS_* pair is used instead of the one the
// config points at. Skipped rather than fixed: this phase does not touch
// storageconfig.go, and whether an empty expansion is an error is its call.
func TestParseStorageFile_EmptyEnvCredential(t *testing.T) {
	awsEnv(t, map[string]string{
		envAccessKeyID:     "AKIAAMBIENT",
		envSecretAccessKey: "ambientsecret",
	})
	t.Setenv("TT_TEST_BACKUP_EMPTY", "")

	cfg, err := ParseStorageURI(storageConfigURI(t, "type: s3\n"+
		"endpoint: s3.example.com\nbucket: b\n"+
		"access_key_id: \"${TT_TEST_BACKUP_EMPTY}\"\n"+
		"secret_access_key: \"${TT_TEST_BACKUP_EMPTY}\"\n"))

	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "TT_TEST_BACKUP_EMPTY")
}

// TestParseStorageFile_MalformedEnvReference checks a "$" that names no
// variable is an error rather than a credential with a dollar in it. A "$"
// that does name one is a reference, however it was meant: that is the case
// TestParseStorageFile_UnsetEnv covers, and what "$$" is for.
func TestParseStorageFile_MalformedEnvReference(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		wantErr string
	}{
		{name: "unterminated", value: "${TT_TEST_BACKUP", wantErr: "unterminated"},
		{name: "empty name", value: "${}", wantErr: "invalid environment variable name"},
		{name: "invalid name", value: "${TT-TEST}", wantErr: "invalid environment variable name"},
		{name: "trailing dollar", value: "s3cr3t$", wantErr: `write "$$"`},
		{name: "dollar before a separator", value: "pay$ day", wantErr: `write "$$"`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			awsEnv(t, nil)

			cfg, err := ParseStorageURI(storageConfigURI(t, "type: s3\n"+
				"endpoint: s3.example.com\nbucket: b\naccess_key_id: k\n"+
				"secret_access_key: \""+tc.value+"\"\n"))

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tc.wantErr)
			// The value that fires this is a secret holding a dollar.
			assert.NotContains(t, err.Error(), "s3cr3t")
			assert.NotContains(t, err.Error(), "pay$")
		})
	}
}

// TestParseStorageFile_KeysAreNotExpanded checks the substitution stays in the
// values: a field named by the environment would be a puzzle rather than a
// feature, and the field it does not name is reported as unknown.
func TestParseStorageFile_KeysAreNotExpanded(t *testing.T) {
	awsEnv(t, nil)
	t.Setenv("TT_TEST_BACKUP_FIELD", "root")

	cfg, err := ParseStorageURI(storageConfigURI(t,
		"type: fs\n${TT_TEST_BACKUP_FIELD}: /backups\n"))

	require.Error(t, err)
	assert.Nil(t, cfg)
	assert.Contains(t, err.Error(), "unknown field")
	assert.Contains(t, err.Error(), "${TT_TEST_BACKUP_FIELD}")
}

// TestParseStorageURI_S3Env covers the short form: the URI carries the bucket
// and the prefix, everything else comes from the standard AWS variables, so no
// credential is written in a command line, a process listing or a cron log.
func TestParseStorageURI_S3Env(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		env  map[string]string
		want *StorageConfig
	}{
		{
			name: "bucket only",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envRegion:          "eu-north-1",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "s3.eu-north-1.amazonaws.com",
				Bucket:          "payments-backups",
				Region:          "eu-north-1",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				UseSSL:          true,
			},
		},
		{
			name: "bucket and prefix",
			uri:  "s3://payments-backups/tarantool/prod",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envRegion:          "eu-north-1",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "s3.eu-north-1.amazonaws.com",
				Bucket:          "payments-backups",
				Region:          "eu-north-1",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				UseSSL:          true,
				Prefix:          "tarantool/prod",
			},
		},
		{
			name: "trailing slash is not a prefix",
			uri:  "s3://payments-backups/",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envDefaultRegion:   "us-east-1",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "s3.us-east-1.amazonaws.com",
				Bucket:          "payments-backups",
				Region:          "us-east-1",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				UseSSL:          true,
			},
		},
		{
			name: "temporary credentials",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envSessionToken:    "envtoken",
				envRegion:          "eu-north-1",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "s3.eu-north-1.amazonaws.com",
				Bucket:          "payments-backups",
				Region:          "eu-north-1",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				SessionToken:    "envtoken",
				UseSSL:          true,
			},
		},
		{
			name: "s3-compatible endpoint",
			uri:  "s3://payments-backups/tarantool",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envEndpointURL:     "http://localhost:9000",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "localhost:9000",
				Bucket:          "payments-backups",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				Prefix:          "tarantool",
			},
		},
		{
			name: "the s3 endpoint wins over the global one",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envEndpointURL:     "https://other.example.com",
				envEndpointURLS3:   "https://s3.example.com",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "s3.example.com",
				Bucket:          "payments-backups",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				UseSSL:          true,
			},
		},
		{
			name: "the region does not override an explicit endpoint",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envRegion:          "eu-north-1",
				envEndpointURL:     "https://s3.example.com",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "s3.example.com",
				Bucket:          "payments-backups",
				Region:          "eu-north-1",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				UseSSL:          true,
			},
		},
		{
			name: "a ca bundle for a tls endpoint",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envEndpointURL:     "https://s3.example.com",
				envCABundle:        "/etc/ssl/private-ca.pem",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "s3.example.com",
				Bucket:          "payments-backups",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
				UseSSL:          true,
				CACert:          "/etc/ssl/private-ca.pem",
			},
		},
		{
			name: "a ca bundle is ignored over http",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envEndpointURL:     "http://localhost:9000",
				envCABundle:        "/etc/ssl/private-ca.pem",
			},
			want: &StorageConfig{
				Type:            StorageTypeS3,
				Endpoint:        "localhost:9000",
				Bucket:          "payments-backups",
				AccessKeyID:     "AKIAENV",
				SecretAccessKey: "envsecret",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			awsEnv(t, tc.env)

			cfg, err := ParseStorageURI(tc.uri)

			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg)
		})
	}
}

// TestParseStorageURI_S3EnvErrors checks the short form fails at parse time,
// naming what is missing: an empty credential reaching the storage comes back as
// an opaque 403 after the backup has already run.
func TestParseStorageURI_S3EnvErrors(t *testing.T) {
	credentials := map[string]string{
		envAccessKeyID:     "AKIAENV",
		envSecretAccessKey: "envsecret",
		envRegion:          "eu-north-1",
	}

	cases := []struct {
		name    string
		uri     string
		env     map[string]string
		wantErr string
	}{
		{
			name: "no access key id",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envSecretAccessKey: "envsecret",
				envRegion:          "eu-north-1",
			},
			wantErr: envAccessKeyID + " is not set",
		},
		{
			name: "an empty access key id",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "",
				envSecretAccessKey: "envsecret",
				envRegion:          "eu-north-1",
			},
			wantErr: envAccessKeyID + " is not set",
		},
		{
			name: "no secret access key",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID: "AKIAENV",
				envRegion:      "eu-north-1",
			},
			wantErr: envSecretAccessKey + " is not set",
		},
		{
			name: "no region and no endpoint",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
			},
			wantErr: "cannot resolve the s3 endpoint",
		},
		{
			name: "an unparsable endpoint",
			uri:  "s3://payments-backups",
			env: map[string]string{
				envAccessKeyID:     "AKIAENV",
				envSecretAccessKey: "envsecret",
				envEndpointURL:     "http://localhost:9000/bucket",
			},
			wantErr: "invalid " + envEndpointURL,
		},
		{name: "no bucket", uri: "s3://", env: credentials, wantErr: "must contain a bucket"},
		{
			name:    "an endpoint instead of a bucket",
			uri:     "s3://localhost:9000/bucket",
			env:     credentials,
			wantErr: "names a bucket, not an endpoint",
		},
		{
			name:    "credentials in the uri",
			uri:     "s3://AKIA:s3cr3t@payments-backups",
			env:     credentials,
			wantErr: "must not carry credentials",
		},
		{
			name:    "query parameters of the s3+http(s) form",
			uri:     "s3://payments-backups?AccessKeyID=AKIA&SecretAccessKey=s3cr3t",
			env:     credentials,
			wantErr: "must not carry query parameters",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			awsEnv(t, tc.env)

			cfg, err := ParseStorageURI(tc.uri)

			require.Error(t, err)
			assert.Nil(t, cfg)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.NotContains(t, err.Error(), "s3cr3t")
		})
	}
}

// The tests below guard the grammars that predate the storage config file. They
// are what every existing --backup-storage value in a script, a cron job or the
// integration suite is written in, so they must keep parsing unchanged.

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
		{
			name:       "with a nested prefix",
			uri:        "file:///var/backups?Prefix=mycluster/prod",
			wantPath:   "/var/backups",
			wantPrefix: "mycluster/prod",
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

// TestParseStorageURI_S3IgnoresEnv checks the s3+http(s) form is decided by the
// URI alone: an ambient AWS variable must not change what it connects to.
func TestParseStorageURI_S3IgnoresEnv(t *testing.T) {
	awsEnv(t, map[string]string{
		envAccessKeyID:     "AKIAENV",
		envSecretAccessKey: "envsecret",
		envRegion:          "eu-north-1",
		envEndpointURL:     "https://other.example.com",
	})

	cfg, err := ParseStorageURI(
		"s3+http://localhost:9000/bucket?AccessKeyID=uri&SecretAccessKey=urisecret")

	require.NoError(t, err)
	assert.Equal(t, &StorageConfig{
		Type:            StorageTypeS3,
		Endpoint:        "localhost:9000",
		Bucket:          "bucket",
		AccessKeyID:     "uri",
		SecretAccessKey: "urisecret",
	}, cfg)
}

func TestParseStorageURI_InvalidScheme(t *testing.T) {
	cases := []struct {
		name string
		uri  string
	}{
		{"unsupported scheme", "ftp://host/path"},
		{
			"s3 with the query parameters of s3+http(s)",
			"s3://host:9000/bucket?AccessKeyID=k&SecretAccessKey=secret",
		},
		{"malformed URI", "s3+https://host/bucket?SecretAccessKey=secret%ZZ"},
		{"file URI without path", "file://user:secret@localhost"},
		{"a bare path", "/var/backups"},
		{"no scheme", "backups.example.com/bucket"},
		{"empty string", ""},
		{"blanks", "   "},
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

// TestParseStorageURI_FTP checks the URI form answers the unimplemented backend
// by name, exactly as the config file does.
func TestParseStorageURI_FTP(t *testing.T) {
	cfg, err := ParseStorageURI("ftp://backups.example.com/payments")

	require.ErrorIs(t, err, errFTPNotSupported)
	assert.Nil(t, cfg)
}

func TestStorageConfigScope(t *testing.T) {
	cases := []struct {
		name        string
		prefix      string
		clusterName string
		environment string
		want        string
		wantErr     string
	}{
		{name: "neither", want: ""},
		{name: "cluster only", clusterName: "payments", want: "payments"},
		{
			name:        "cluster and environment",
			clusterName: "payments",
			environment: "production",
			want:        "payments/production",
		},
		{
			name:        "appended to the prefix the URI carried",
			prefix:      "tarantool",
			clusterName: "payments",
			environment: "production",
			want:        "tarantool/payments/production",
		},
		{
			name:        "environment without a cluster name",
			environment: "production",
			wantErr:     "--environment \"production\" needs --cluster-name",
		},
		{
			name:        "a cluster name that is not one path component",
			clusterName: "payments/production",
			wantErr:     "must not contain a path separator",
		},
		{
			name:        "an environment that climbs out of the storage",
			clusterName: "payments",
			environment: "..",
			wantErr:     "must not contain a path separator",
		},
		{
			name:        "surrounding whitespace is not part of the name",
			clusterName: "  payments  ",
			environment: " production ",
			want:        "payments/production",
		},
		{
			name:        "a cluster name of nothing but whitespace",
			clusterName: "   ",
			wantErr:     "cannot be blank",
		},
		{
			name:        "both flags invalid names the first one",
			clusterName: "a/b",
			environment: "c/d",
			wantErr:     "--cluster-name",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &StorageConfig{Type: StorageTypeFile, Path: "/var/backups", Prefix: tc.prefix}

			err := cfg.Scope(tc.clusterName, tc.environment)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
				assert.Equal(t, tc.prefix, cfg.Prefix,
					"a refused scope must not move the storage")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, cfg.Prefix)
		})
	}
}
