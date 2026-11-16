package rocks

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"path"
	"strings"
)

// indexFiles are the files of a rock server a client reads first, which an
// error of a server names: the server is the directory they are in.
var indexFiles = map[string]bool{
	"tt-rocks-index.json": true, "manifest": true, "manifest-5.1": true,
	"manifest-5.1.zip": true, "manifest.zip": true,
}

// ServerError is a rock server that could not be reached or read, the
// failure of an operation on a rock (Op, "resolving \"checks\"" say). Its
// message is the operation, the server and the reason, one line, as a user
// reads it:
//
//	resolving "checks": cannot reach rock server http://example.invalid/: no such host
//
// and Unwrap is the whole chain of the failure, the URL, the request and the
// transport error, for errors.Is, for the exit code and for -V.
type ServerError struct {
	// Op is what was being done, "" for none.
	Op string
	// Server is the server, as the failure names it.
	Server string
	// Origin says where the server is configured, "TT_REGISTRIES" say; ""
	// when it is not known. The CLI fills it in.
	Origin string
	// Reason is why, "no such host" say.
	Reason string
	// Err is the failure.
	Err error
}

// Error returns the message.
func (e *ServerError) Error() string {
	server := e.Server
	if e.Origin != "" {
		server += " (from " + e.Origin + ")"
	}

	message := "cannot reach rock server " + server + ": " + e.Reason
	if e.Op == "" {
		return message
	}

	return e.Op + ": " + message
}

// Unwrap returns the failure.
func (e *ServerError) Unwrap() error { return e.Err }

// serverError returns err, the failure of operation, as a *ServerError when
// a rock server could not be reached or read there - its host is not found,
// it refused the connection or did not answer in time, its directory is not
// there - and as prefix: err otherwise.
func serverError(operation, prefix string, err error) error {
	if _, known := errors.AsType[*ServerError](err); known {
		return fmt.Errorf("%s: %w", prefix, err)
	}

	server, reason, ok := unreachable(err)
	if !ok {
		return fmt.Errorf("%s: %w", prefix, err)
	}

	return &ServerError{Op: operation, Server: server, Origin: "", Reason: reason, Err: err}
}

// AsServerError returns err, the failure of operation, as a *ServerError,
// and true, when a rock server could not be reached or read there, as
// serverError tells it; err and false otherwise.
func AsServerError(operation string, err error) (error, bool) {
	server, reason, ok := unreachable(err)
	if !ok {
		return err, false
	}

	return &ServerError{Op: operation, Server: server, Origin: "", Reason: reason, Err: err}, true
}

// unreachable returns the server and the reason of err when it is the
// failure to reach or read a rock server.
func unreachable(err error) (string, string, bool) {
	var (
		urlErr  *url.Error
		dnsErr  *net.DNSError
		opErr   *net.OpError
		pathErr *fs.PathError
	)

	switch {
	case errors.As(err, &urlErr):
		server := serverOf(urlErr.URL)

		switch {
		case errors.As(err, &dnsErr) && dnsErr.IsNotFound:
			return server, "no such host", true
		case urlErr.Timeout():
			return server, "it did not answer in time", true
		case errors.As(err, &opErr):
			return server, reasonOf(opErr), true
		}
	case errors.As(err, &pathErr) && errors.Is(err, fs.ErrNotExist):
		return pathErr.Path, "no such file or directory", true
	}

	return "", "", false
}

// reasonOf returns the reason of a failed dial: the error of the system,
// "connection refused" say.
func reasonOf(opErr *net.OpError) string {
	reason := opErr.Err.Error()
	if _, after, ok := strings.Cut(reason, ": "); ok && opErr.Op == "dial" {
		// "connect: connection refused" is the connect of the dial.
		return after
	}

	return reason
}

// serverOf returns the server of a URL a client asked: the URL without the
// index file it names, when it names one.
func serverOf(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || !indexFiles[path.Base(parsed.Path)] {
		return raw
	}

	parsed.Path = path.Dir(parsed.Path)
	if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}

	parsed.RawQuery = ""

	return parsed.String()
}
