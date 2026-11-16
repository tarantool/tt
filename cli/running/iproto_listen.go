package running

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	goconfig "github.com/tarantool/go-config/v2"
)

// IprotoListen is a TCP address an instance listens for iproto on, as an
// entry of iproto.listen of its config names it.
type IprotoListen struct {
	// Address is host:port to dial: an address of every interface, such as
	// 0.0.0.0 or [::], is the loopback one, and a bare port is on localhost.
	Address string
	// TLS is set when the entry has transport ssl, in its params or in the
	// query of its URI.
	TLS bool
}

// noListen is the IprotoListen of an instance that has none.
var noListen = IprotoListen{Address: "", TLS: false}

// ConfigTCPListen returns the TCP address a client reaches a Tarantool
// instance at by the config: the first entry of iproto.listen, for a config
// that lists no unix socket there. A unix socket of the config is the binary
// port of the instance (configBinaryPort), which a local client takes
// first; so found is false then, and also for an instance without a config,
// a config without iproto.listen, where tarantool listens on the binary port
// of the layout, and an instance whose record sets TT_IPROTO_LISTEN, which
// tt does not parse.
func ConfigTCPListen(inst *InstanceCtx) (IprotoListen, bool, error) {
	if IsScript(inst) || inst.Configuration == nil {
		return noListen, false, nil
	}

	if _, ok := inst.Env[envName("iproto.listen")]; ok {
		return noListen, false, nil
	}

	var raw any

	_, err := inst.Configuration.Get(goconfig.NewKeyPath("iproto/listen"), &raw)
	if errors.Is(err, goconfig.ErrKeyNotFound) {
		return noListen, false, nil
	}

	if err != nil {
		return noListen, false,
			fmt.Errorf("getting %q from the config: %w", "iproto.listen", err)
	}

	entries, _ := raw.([]any)

	first, found := noListen, false

	for _, entry := range entries {
		fields, _ := entry.(map[string]any)
		rawURI, _ := fields["uri"].(string)

		uri := strings.TrimSpace(applyConfigVars(rawURI, inst.configVars))
		if isUnixListenURI(uri) {
			return noListen, false, nil
		}

		if found || uri == "" {
			continue
		}

		params, _ := fields["params"].(map[string]any)
		transport, _ := params["transport"].(string)
		address, query, _ := strings.Cut(strings.TrimPrefix(uri, "tcp://"), "?")

		values, _ := url.ParseQuery(query)
		if values.Get("transport") != "" {
			transport = values.Get("transport")
		}

		first = IprotoListen{Address: dialAddress(address), TLS: transport == "ssl"}
		found = true
	}

	return first, found, nil
}

// isUnixListenURI tells a listen URI of a unix socket: unix/:<path>,
// unix://<path>, or a bare path.
func isUnixListenURI(uri string) bool {
	for _, prefix := range []string{unixURIPrefix, "unix://", "/", "./", "../"} {
		if strings.HasPrefix(uri, prefix) {
			return true
		}
	}

	return false
}

// dialAddress turns the address of a listen URI of iproto, with no scheme
// and no query, into the address a local client dials: an address of every
// interface becomes the loopback one, and a bare port gets localhost.
func dialAddress(address string) string {
	if !strings.Contains(address, ":") {
		return net.JoinHostPort("localhost", address)
	}

	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return address
	}

	switch host {
	case "", "0.0.0.0", "*":
		host = "127.0.0.1"
	case "::", "[::]":
		host = "::1"
	}

	return net.JoinHostPort(host, port)
}
