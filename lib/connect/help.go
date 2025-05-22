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
//	`env_<name>_auth` - string: service info. It will expanded to:
//		* <name>_USERNAME - specifies a <info> username;
//		* <name>_PASSWORD - specifies a <info> password.
//	`env_<name>` - string: description for an extra environment variable with <name>:
//		* <name> - <description>.
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
{{- if or .env_auth .env_vars }}

The command supports the following environment variables:
{{- range $key, $value := .env_auth }}
* {{$key}}_USERNAME - specifies a {{$value}} username;
* {{$key}}_PASSWORD - specifies a {{$value}} password.
{{- end }}
{{- range $key, $value := .env_vars }}
* {{$key}} - {{$value}}.
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
		"timeout":             timeoutParam,
		"default_timeout":     fmt.Sprintf("%.1f", tm),
		"ssl_key_file":        sslKeyFileParam,
		"ssl_cert_file":       sslCertFileParam,
		"ssl_ca_file":         sslCaFileParam,
		"ssl_ca_path":         sslCaPathParam,
		"ssl_ciphers":         sslCiphersParam,
		"verify_host":         verifyHostParam,
		"verify_peer":         verifyPeerParam,
		"default_verify_host": fmt.Sprintf("%t", defaultVerifyHostParam),
		"default_verify_peer": fmt.Sprintf("%t", defaultVerifyPeerParam),
	}

	envAuth := map[string]template.HTML{}
	envVars := map[string]template.HTML{}

	makeEnvVars := func(key, info string) {
		h := template.HTML(info)
		if strings.HasSuffix(key, "_auth") {
			envAuth[strings.TrimSuffix(key, "_auth")] = h
		} else {
			envVars[key] = h
		}
	}

	for key, value := range data {
		if strings.HasPrefix(key, "env_") {
			s, ok := value.(string)
			if !ok {
				s = fmt.Sprintf("%v", value)
			}
			makeEnvVars(strings.TrimPrefix(key, "env_"), s)
		} else {
			s, ok := value.(string)
			if ok {
				// Wrap description with `template.HTML` to avoid escaping.
				params[key] = template.HTML(s)
			} else {
				params[key] = value
			}
		}
	}
	params["env_auth"] = envAuth
	params["env_vars"] = envVars

	var sb strings.Builder
	t.Execute(&sb, params)
	return sb.String()
}
