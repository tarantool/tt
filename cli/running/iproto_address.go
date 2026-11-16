package running

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/log"
)

// The keys of the config a client address of an instance comes from.
const (
	advertiseClientKey = "iproto.advertise.client"
	listenKey          = "iproto.listen"
)

// The transport of a TLS listener, in iproto.listen.*.params.transport.
const sslTransport = "ssl"

// The loopback addresses a client dials for an address of every interface.
const (
	loopbackV4 = "127.0.0.1"
	loopbackV6 = "::1"
)

// Why Tarantool refuses a value of iproto.advertise.client, which it checks
// as a URI to connect to (validators.lua, validate_uri_field, and
// network.lua, uri_is_suitable_to_connect, of src/box/lua/config).
var (
	errAdvertiseURI = errors.New("is not a URI of an instance: " +
		"host:port, a port or unix/:<path>")
	errAdvertiseLogin = errors.New("has a login, which Tarantool does not take there and " +
		"refuses the config for: give the credentials with --username and --password")
	errAdvertiseParams = errors.New("has URI parameters, which Tarantool takes in the params " +
		"of iproto.listen only and refuses the config for")
	errAdvertiseEveryInterface = errors.New("is an address of every interface, which " +
		"Tarantool refuses a client address to be")
	errAdvertiseZeroPort = errors.New("has port 0, which Tarantool refuses a client address " +
		"to have")
)

// IprotoParams are the params of an entry of iproto.listen: the transport
// and the TLS settings of the listener, with the files absolute.
type IprotoParams struct {
	// Transport is "ssl" for a TLS listener, "plain" or empty otherwise.
	Transport       string
	SslKeyFile      string
	SslCertFile     string
	SslCaFile       string
	SslCiphers      string
	SslPassword     string
	SslPasswordFile string
}

// TLS reports whether the listener is a TLS one.
func (params IprotoParams) TLS() bool {
	return params.Transport == sslTransport
}

// IprotoAddress is where a client on this host reaches a Tarantool instance
// over iproto, by its config.
type IprotoAddress struct {
	// Key is the key of the config the address comes from:
	// iproto.advertise.client or iproto.listen.
	Key string
	// Unix is set when Address is the path of a unix socket.
	Unix bool
	// Address is host:port to dial, or the path of a unix socket.
	Address string
	// Params are those of the entry of iproto.listen at the address.
	Params IprotoParams
}

// plainParams are the params of a plain listener without TLS settings, and
// of an address no entry of iproto.listen is at.
var plainParams = IprotoParams{
	Transport: "", SslKeyFile: "", SslCertFile: "", SslCaFile: "",
	SslCiphers: "", SslPassword: "", SslPasswordFile: "",
}

// noAddress is the IprotoAddress of an instance the config gives none.
var noAddress = IprotoAddress{Key: "", Unix: false, Address: "", Params: plainParams}

// ClientIprotoAddress returns where a client on this host reaches the
// Tarantool instance inst over iproto by its effective config, the TT_*
// variables of its record and of the environment, the file and the config
// storage:
//
//   - iproto.advertise.client, the URI Tarantool advertises to clients, with
//     no default; it checks the URI and does not start with one it refuses,
//     and so a value it refuses is an error here: one with a login or URI
//     parameters, an address of every interface or port 0;
//   - else the first TCP address of iproto.listen with a port, unix sockets
//     listed before it or not, an address of every interface becoming the
//     loopback one of its family, since the instance is on this host. (For a
//     peer URI Tarantool takes the first listen URI it can connect to, a
//     unix socket too, and skips the addresses of every interface.)
//
// The params of the address are those of its entry of iproto.listen: for
// iproto.advertise.client, a bare URI, those of the entry at the same socket,
// or at the same port of the same host or of every interface.
//
// found is false for an instance that runs a script, a config with neither
// key, and an iproto.listen of unix sockets only, which a client reaches at
// the binary port of the instance (configBinaryPort). The env of the record
// setting TT_IPROTO_LISTEN, which tt does not parse, hides iproto.listen.
func ClientIprotoAddress(inst *InstanceCtx) (IprotoAddress, bool, error) {
	if IsScript(inst) {
		return noAddress, false, nil
	}

	listen, err := configListen(inst)
	if err != nil {
		return noAddress, false, err
	}

	advertise, found, err := effectiveString(inst, advertiseClientKey)
	if err != nil {
		return noAddress, false, err
	}

	if found {
		address, err := advertiseAddress(inst, advertise, listen)
		if err != nil {
			return noAddress, false, fmt.Errorf("instance %q of %q: %s %q %w",
				inst.InstName, inst.AppName, advertiseClientKey, log.RedactTarget(advertise), err)
		}

		return address, true, nil
	}

	for _, entry := range listen {
		if entry.uri.unix || entry.uri.port == "" || entry.uri.port == "0" {
			continue
		}

		return IprotoAddress{
			Key:     listenKey,
			Unix:    false,
			Address: entry.uri.dialAddress(),
			Params:  entry.params,
		}, true, nil
	}

	return noAddress, false, nil
}

// advertiseAddress returns the address of value, the iproto.advertise.client
// of inst, with the params of its entry of listen.
func advertiseAddress(inst *InstanceCtx, value string,
	listen []listenEntry,
) (IprotoAddress, error) {
	uri, ok := parseIprotoURI(value)

	switch {
	case !ok:
		return noAddress, errAdvertiseURI
	case uri.login:
		return noAddress, errAdvertiseLogin
	case uri.hasQuery:
		return noAddress, errAdvertiseParams
	case uri.unix:
		return IprotoAddress{
			Key:     advertiseClientKey,
			Unix:    true,
			Address: configSocketPath(inst, uri.path),
			Params:  listenParams(inst, uri, listen),
		}, nil
	case uri.port == "0":
		return noAddress, errAdvertiseZeroPort
	case uri.host != "" && uri.everyInterface():
		return noAddress, errAdvertiseEveryInterface
	}

	return IprotoAddress{
		Key:     advertiseClientKey,
		Unix:    false,
		Address: uri.dialAddress(),
		Params:  listenParams(inst, uri, listen),
	}, nil
}

// listenParams returns the params of the entry of listen at uri, an address
// of iproto.advertise.client: the same socket, or the same port of the same
// host or of every interface. An address no entry names is a plain one.
func listenParams(inst *InstanceCtx, uri iprotoURI, listen []listenEntry) IprotoParams {
	for _, entry := range listen {
		if entry.uri.unix != uri.unix {
			continue
		}

		if uri.unix {
			if configSocketPath(inst, entry.uri.path) == configSocketPath(inst, uri.path) {
				return entry.params
			}

			continue
		}

		if entry.uri.port == uri.port &&
			(entry.uri.everyInterface() || strings.EqualFold(entry.uri.host, uri.host)) {
			return entry.params
		}
	}

	return plainParams
}

// listenEntry is an entry of iproto.listen.
type listenEntry struct {
	uri    iprotoURI
	params IprotoParams
}

// configListen returns the entries of iproto.listen of the config of inst,
// with the variables of the config substituted, leaving out those whose URI
// is none. It returns none for an instance without a config, a config
// without iproto.listen, and the env of the record setting TT_IPROTO_LISTEN,
// which Tarantool ranks above the config and tt does not parse.
func configListen(inst *InstanceCtx) ([]listenEntry, error) {
	if inst.Configuration == nil {
		return nil, nil
	}

	if _, ok := inst.Env[envName(listenKey)]; ok {
		return nil, nil
	}

	var raw any

	_, err := inst.Configuration.Get(goconfig.NewKeyPath("iproto/listen"), &raw)
	if errors.Is(err, goconfig.ErrKeyNotFound) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("getting %q from the config: %w", listenKey, err)
	}

	items, _ := raw.([]any)
	entries := make([]listenEntry, 0, len(items))

	for _, item := range items {
		fields, _ := item.(map[string]any)
		rawURI, _ := fields["uri"].(string)

		uri, ok := parseIprotoURI(applyConfigVars(rawURI, inst.configVars))
		if !ok {
			continue
		}

		params, _ := fields["params"].(map[string]any)

		entries = append(entries,
			listenEntry{uri: uri, params: listenEntryParams(inst, params, uri)})
	}

	return entries, nil
}

// listenEntryParams returns the params of an entry of iproto.listen of inst:
// those of its params, with the variables of the config substituted and the
// files resolved against the application directory, as the instance runs
// there. The query of its URI, which Tarantool refuses, gives the ones the
// params do not.
func listenEntryParams(inst *InstanceCtx, params map[string]any, uri iprotoURI) IprotoParams {
	value := func(key string) string {
		raw, _ := params[key].(string)
		if raw == "" {
			raw = uri.query.Get(key)
		}

		return applyConfigVars(raw, inst.configVars)
	}

	file := func(key string) string {
		path := value(key)
		if path == "" || filepath.IsAbs(path) {
			return path
		}

		return filepath.Join(inst.AppDir, path)
	}

	return IprotoParams{
		Transport:       value("transport"),
		SslKeyFile:      file("ssl_key_file"),
		SslCertFile:     file("ssl_cert_file"),
		SslCaFile:       file("ssl_ca_file"),
		SslCiphers:      value("ssl_ciphers"),
		SslPassword:     value("ssl_password"),
		SslPasswordFile: file("ssl_password_file"),
	}
}

// configSocketPath returns the path of a unix socket of the config of inst,
// resolved against the application directory, where the instance runs.
func configSocketPath(inst *InstanceCtx, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(inst.AppDir, path)
}

// iprotoURI is a URI of iproto.listen or iproto.advertise.client cut as the
// URI parser of Tarantool cuts it (src/lib/uri/uri_parser.rl):
// [scheme://][login[:password]@](host:port | port | unix/:<path> |
// <path>)[?query], a path being one that starts with "/", "./" or "../".
// The scheme unix:// of tt URIs makes the rest a path too.
type iprotoURI struct {
	// login is set when the URI has a login, with a password or not.
	login bool
	// unix is set for a unix socket at path, as it is written.
	unix bool
	path string
	// host is empty for a bare port; an IPv6 one is without the brackets.
	host string
	port string
	// hasQuery is set when the URI has a query, query being its values.
	hasQuery bool
	query    url.Values
}

var (
	// uriScheme is the scheme of a URI, with the "://" after it.
	uriScheme = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.-]*://`)
	// uriUserinfo is the login and the password of a URI, with the "@"
	// after them, in the characters the URI parser of Tarantool takes there:
	// a socket path, with its "/", is never one.
	uriUserinfo = regexp.MustCompile(
		`^[A-Za-z0-9\-_~.%!$&'()*+,;=]+(:[A-Za-z0-9\-_~.%!$&'()*+,;=]*)?@`)
	// uriPort is a bare port.
	uriPort = regexp.MustCompile(`^[0-9]+$`)
)

// parseIprotoURI cuts raw into an iprotoURI. ok is false for one that is
// empty, or names neither a socket nor a port, as "localhost" alone.
func parseIprotoURI(raw string) (iprotoURI, bool) {
	rest, query, hasQuery := strings.Cut(strings.TrimSpace(raw), "?")
	values, _ := url.ParseQuery(query)

	uri := iprotoURI{
		login: false, unix: false, path: "", host: "", port: "",
		hasQuery: hasQuery, query: values,
	}

	scheme := uriScheme.FindString(rest)

	rest = rest[len(scheme):]

	if userinfo := uriUserinfo.FindString(rest); userinfo != "" {
		uri.login = true
		rest = rest[len(userinfo):]
	}

	if path, ok := strings.CutPrefix(rest, unixURIPrefix); ok {
		uri.unix, uri.path = true, path

		return uri, path != ""
	}

	if strings.EqualFold(scheme, "unix://") || strings.HasPrefix(rest, "/") ||
		strings.HasPrefix(rest, "./") || strings.HasPrefix(rest, "../") {
		uri.unix, uri.path = true, rest

		return uri, rest != ""
	}

	if uriPort.MatchString(rest) {
		uri.port = rest

		return uri, true
	}

	host, port, err := net.SplitHostPort(rest)
	if err != nil || port == "" {
		return uri, false
	}

	uri.host, uri.port = host, port

	return uri, true
}

// everyInterface reports whether the host of the URI is an address of every
// interface: 0.0.0.0, :: or none, as for a bare port, which Tarantool binds
// as getaddrinfo gives a host of none with AI_PASSIVE.
func (uri iprotoURI) everyInterface() bool {
	if uri.host == "" {
		return true
	}

	ip := net.ParseIP(uri.host)

	return ip != nil && ip.IsUnspecified()
}

// dialAddress returns host:port for a client on this host to dial: an
// address of every interface becomes the loopback one of its family, IPv4
// for none.
func (uri iprotoURI) dialAddress() string {
	host := uri.host

	if uri.everyInterface() {
		host = loopbackV4

		if ip := net.ParseIP(uri.host); ip != nil && ip.To4() == nil {
			host = loopbackV6
		}
	}

	return net.JoinHostPort(host, uri.port)
}
