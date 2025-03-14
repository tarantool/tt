package connect_test

import (
	"testing"

	"github.com/pmezard/go-difflib/difflib"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/lib/connect"
)

func TestMakeURLHelp(t *testing.T) {
	type args struct {
		data map[string]any
	}
	tests := map[string]struct {
		args args
		want string
	}{
		"empty_data": {
			args: args{data: map[string]any{}},
			want: `The URL specifies a  connection settings in the following format:
http(s)://[username:password@]host:port[?arguments]

Possible arguments:
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.
`,
		},

		"nil_data": {
			args: args{data: nil},
			want: `The URL specifies a  connection settings in the following format:
http(s)://[username:password@]host:port[?arguments]

Possible arguments:
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.
`,
		},

		"full_set": {
			args: args{data: map[string]any{
				"header":  "=== Header info line ===",
				"service": "etcd or tarantool config storage",
				"prefix": "a base path to Tarantool configuration in" +
					" etcd or tarantool config storage",
				"tag":                  "fragment used by application",
				"param_key":            "a target configuration key in the prefix",
				"param_name":           "a name of an instance in the cluster configuration",
				"env_TT_CLI_ETCD_auth": "Etcd",
				"env_TT_CLI_auth":      "Tarantool",
				"footer": `The priority of credentials:
environment variables < command flags < URL credentials.`,
				"not used param": true,
			}},
			want: `=== Header info line ===
The URL specifies a etcd or tarantool config storage connection settings in the following format:
http(s)://[username:password@]host:port/prefix[?arguments][#tag]

* prefix - a base path to Tarantool configuration in etcd or tarantool config storage.
* tag - fragment used by application.

Possible arguments:
* key - a target configuration key in the prefix.
* name - a name of an instance in the cluster configuration.
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.

The command supports the following environment variables:
* TT_CLI_USERNAME - specifies a Tarantool username;
* TT_CLI_PASSWORD - specifies a Tarantool password.
* TT_CLI_ETCD_USERNAME - specifies a Etcd username;
* TT_CLI_ETCD_PASSWORD - specifies a Etcd password.

The priority of credentials:
environment variables < command flags < URL credentials.
`,
		},

		"etcd_only": {
			args: args{data: map[string]any{
				"service":              "etcd",
				"prefix":               "a base path to Tarantool configuration in etcd",
				"env_TT_CLI_ETCD_auth": "Etcd",
			}},
			want: `The URL specifies a etcd connection settings in the following format:
http(s)://[username:password@]host:port/prefix[?arguments]

* prefix - a base path to Tarantool configuration in etcd.

Possible arguments:
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.

The command supports the following environment variables:
* TT_CLI_ETCD_USERNAME - specifies a Etcd username;
* TT_CLI_ETCD_PASSWORD - specifies a Etcd password.
`,
		},

		"tcs_only": {
			args: args{data: map[string]any{
				"service":         "tarantool",
				"prefix":          "a base path to Tarantool configuration in TcS",
				"env_TT_CLI_auth": "Tarantool",
			}},
			want: `The URL specifies a tarantool connection settings in the following format:
http(s)://[username:password@]host:port/prefix[?arguments]

* prefix - a base path to Tarantool configuration in TcS.

Possible arguments:
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.

The command supports the following environment variables:
* TT_CLI_USERNAME - specifies a Tarantool username;
* TT_CLI_PASSWORD - specifies a Tarantool password.
`,
		},

		"escaped_chars": {
			args: args{data: map[string]any{
				"service": "<test>",
				"footer":  "=== Two final lines with extra chars ===\n<<< \"Last\" '&&' \\line >>>",
			}},
			want: `The URL specifies a <test> connection settings in the following format:
http(s)://[username:password@]host:port[?arguments]

Possible arguments:
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.

=== Two final lines with extra chars ===
<<< "Last" '&&' \line >>>
`,
		},
		"fragment without prefix": {
			args: args{data: map[string]any{
				"service": "test",
				"tag":     "fragment used by application",
			}},
			want: `The URL specifies a test connection settings in the following format:
http(s)://[username:password@]host:port[?arguments][#tag]

* tag - fragment used by application.

Possible arguments:
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.
`,
		},

		"resort environment vars": {
			args: args{data: map[string]any{
				"env_XYZ_auth": "x & y & z",
				"env_ABC_auth": `<a "b" c>`,
				"env_KLM_auth": `k "l" m`,
				"env_SOME_LONG_NAME_VARIABLE_NAME": `Here is a very long multiline info:
	Second line & with [tab] indent,
    Third line | with {spaces} indent`,
				"env_GHI": "description for 'ghi' variable",
				"env_DEF": []any{"representation", 4, "some value", true},
			}},
			want: `The URL specifies a  connection settings in the following format:
http(s)://[username:password@]host:port[?arguments]

Possible arguments:
* timeout - a request timeout in seconds (default 3.0).
* ssl_key_file - a path to a private SSL key file.
* ssl_cert_file - a path to an SSL certificate file.
* ssl_ca_file - a path to a trusted certificate authorities (CA) file.
* ssl_ca_path - a path to a trusted certificate authorities (CA) directory.
* ssl_ciphers - a list of allowed SSL ciphers.
* verify_host - set off (default true) verification of the certificate’s name against the host.
* verify_peer - set off (default true) verification of the peer’s SSL certificate.

The command supports the following environment variables:
* ABC_USERNAME - specifies a <a "b" c> username;
* ABC_PASSWORD - specifies a <a "b" c> password.
* KLM_USERNAME - specifies a k "l" m username;
* KLM_PASSWORD - specifies a k "l" m password.
* XYZ_USERNAME - specifies a x & y & z username;
* XYZ_PASSWORD - specifies a x & y & z password.
* DEF - [representation 4 some value true].
* GHI - description for 'ghi' variable.
* SOME_LONG_NAME_VARIABLE_NAME - Here is a very long multiline info:
	Second line & with [tab] indent,
    Third line | with {spaces} indent.
`,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := connect.MakeURLHelp(tt.args.data)
			diff := difflib.UnifiedDiff{
				A:        difflib.SplitLines(tt.want),
				FromFile: "want",
				B:        difflib.SplitLines(got),
				ToFile:   "got",
				Context:  2,
			}
			u, err := difflib.GetUnifiedDiffString(diff)
			require.NoError(t, err)

			if u != "" {
				t.Errorf("[%s] mismatch (-want +got):\n%s", name, u)
			}
		})
	}
}
