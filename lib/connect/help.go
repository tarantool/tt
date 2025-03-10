package connect

import (
	"fmt"
	"html/template"
	"strings"
	"time"
)

// nolint: lll
const EnvTarantoolCredentialsHelp = "The command supports the following Tarantool environment variables:\n" +
	"* " + TarantoolUsernameEnv + " - specifies a Tarantool username\n" +
	"* " + TarantoolPasswordEnv + " - specifies a Tarantool password"

const EnvEtcdCredentialsHelp = "The command supports the following Etcd environment variables:\n" +
	"* " + EtcdUsernameEnv + " - specifies a Etcd username\n" +
	"* " + EtcdPasswordEnv + " - specifies a Etcd password"

// nolint: lll
// MakeURLHelp returns a part of command help message related to URL arguments.
// The function uses a template to generate the message.
// Accepts following placeholders in the `data` map:
//
//	`header` - string: a header for the help message;
//	`footer` - string: a final message of the help;
//	`service` - string: name of the service to connect with URL;
//	`prefix` - string: a base path used by service application;
//	`tag` - string: description how `#fragment` part used by application;
//	`param_<name>` - string: description for an extra URL param with <name> added to help;
//	`env_tarantool` - bool: whether to include Tarantool environment variables help;
//	`env_etcd` - bool: whether to include Etcd environment variables help;
func MakeURLHelp(data map[string]any) string {
	st := `{{ if .header }}{{.header}}
{{end -}}
The URL specifies a {{.service}} connection settings in the following format:
http(s)://[username:password@]host:port{{ if .prefix }}/prefix{{end}}[?arguments]{{ if .tag }}[#tag]{{end}}
{{- if or .prefix .tag }}{{ $NL := "" }}

{{with .prefix }}* prefix - {{.}}.{{ $NL = "\n" }}{{end -}}
{{with .tag }}{{ $NL }}* tag - {{.}}.{{end -}}
{{end}}

Possible arguments:
{{ range $key, $value := . -}}
{{ if hasPrefix $key "param_" -}}
* {{ trimPrefix $key "param_" }} - {{$value}}.
{{end}}{{end -}}
* {{.timeout}} - a request timeout in seconds (default {{.default_timeout}}).
* {{.ssl_key_file}} - a path to a private SSL key file.
* {{.ssl_cert_file}} - a path to an SSL certificate file.
* {{.ssl_ca_file}} - a path to a trusted certificate authorities (CA) file.
* {{.ssl_ca_path}} - a path to a trusted certificate authorities (CA) directory.
* {{.ssl_ciphers}} - a list of allowed SSL ciphers.
* {{.verify_host}} - set off (default {{.default_verify_host}}) verification of the certificate’s name against the host.
* {{.verify_peer}} - set off (default {{.default_verify_peer}}) verification of the peer’s SSL certificate.
{{- if or .env_tarantool .env_etcd }}

The command supports the following environment variables:
{{- if .env_tarantool }}
* {{.env_tarantool_username}} - specifies a Tarantool username
* {{.env_tarantool_password}} - specifies a Tarantool password
{{- end }}
{{- if .env_etcd }}
* {{.env_etcd_username}} - specifies a Etcd username
* {{.env_etcd_password}} - specifies a Etcd password
{{- end}}
{{- end}}
{{- if .footer }}

{{.footer}}{{end}}
`
	t := template.Must(template.New("URL").Funcs(template.FuncMap{
		"hasPrefix":  strings.HasPrefix,
		"trimPrefix": strings.TrimPrefix,
	}).Parse(st))

	tm := float64(defaultTimeoutParam) / float64(time.Second)
	params := map[string]any{
		"timeout":                timeoutParam,
		"default_timeout":        fmt.Sprintf("%.1f", tm),
		"ssl_key_file":           sslKeyFileParam,
		"ssl_cert_file":          sslCertFileParam,
		"ssl_ca_file":            sslCaFileParam,
		"ssl_ca_path":            sslCaPathParam,
		"ssl_ciphers":            sslCiphersParam,
		"verify_host":            verifyHostParam,
		"verify_peer":            verifyPeerParam,
		"default_verify_host":    fmt.Sprintf("%t", defaultVerifyHostParam),
		"default_verify_peer":    fmt.Sprintf("%t", defaultVerifyPeerParam),
		"env_etcd_username":      EtcdUsernameEnv,
		"env_etcd_password":      EtcdPasswordEnv,
		"env_tarantool_username": TarantoolUsernameEnv,
		"env_tarantool_password": TarantoolPasswordEnv,
	}

	for key, value := range data {
		s, ok := value.(string)
		if ok {
			// Wrap description with `template.HTML` to avoid escaping.
			params[key] = template.HTML(s)
		} else {
			params[key] = value
		}
	}

	var sb strings.Builder
	t.Execute(&sb, params)
	return sb.String()
}
