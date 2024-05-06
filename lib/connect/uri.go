package connect

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/tarantool/tt/cli/connector"
)

const (
	// userPathRe is a regexp for a username:password pair.
	userpassRe = `[^@:/]+:[^@:/]+`

	// uriPathPrefixRe is a regexp for a path prefix in uri, such as `scheme://path``.
	uriPathPrefixRe = `((~?/+)|((../+)*))?`

	// systemPathPrefixRe is a regexp for a path prefix to use without scheme.
	systemPathPrefixRe = `(([\.~]?/+)|((../+)+))`
)

// IsBaseURI returns true if a string is a valid URI.
func IsBaseURI(str string) bool {
	// tcp://host:port
	// host:port
	tcpReStr := `(tcp://)?([\w\\.-]+:\d+)`
	// unix://../path
	// unix://~/path
	// unix:///path
	// unix://path
	unixReStr := `unix://` + uriPathPrefixRe + `[^\./@]+[^@]*`
	// ../path
	// ~/path
	// /path
	// ./path
	pathReStr := systemPathPrefixRe + `[^\./].*`

	uriReStr := "^((" + tcpReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
	uriRe := regexp.MustCompile(uriReStr)
	return uriRe.MatchString(str)
}

// IsCredentialsURI returns true if a string is a valid credentials URI.
func IsCredentialsURI(str string) bool {
	// tcp://user:password@host:port
	// user:password@host:port
	tcpReStr := `(tcp://)?` + userpassRe + `@([\w\.-]+:\d+)`
	// unix://user:password@../path
	// unix://user:password@~/path
	// unix://user:password@/path
	// unix://user:password@path
	unixReStr := `unix://` + userpassRe + `@` + uriPathPrefixRe + `[^\./@]+.*`
	// user:password@../path
	// user:password@~/path
	// user:password@/path
	// user:password@./path
	pathReStr := userpassRe + `@` + systemPathPrefixRe + `[^\./].*`

	uriReStr := "^((" + tcpReStr + ")|(" + unixReStr + ")|(" + pathReStr + "))$"
	uriRe := regexp.MustCompile(uriReStr)
	return uriRe.MatchString(str)
}

// ParseBaseURI parses an URI and returns:
// (network, address)
func ParseBaseURI(uri string) (string, string) {
	var network, address string
	uriLen := len(uri)

	switch {
	case uriLen > 0 && (uri[0] == '.' || uri[0] == '/' || uri[0] == '~'):
		network = connector.UnixNetwork
		address = uri
	case uriLen >= 7 && uri[0:7] == "unix://":
		network = connector.UnixNetwork
		address = uri[7:]
	case uriLen >= 6 && uri[0:6] == "tcp://":
		network = connector.TCPNetwork
		address = uri[6:]
	default:
		network = connector.TCPNetwork
		address = uri
	}

	// In the case of a complex uri, shell expansion does not occur, so do it manually.
	if network == connector.UnixNetwork &&
		strings.HasPrefix(address, "~/") {
		if homeDir, err := os.UserHomeDir(); err == nil {
			address = filepath.Join(homeDir, address[2:])
		}
	}

	return network, address
}

// ParseCredentialsURI parses a URI with credentials and returns:
// (URI without credentials, user, password)
func ParseCredentialsURI(str string) (string, string, string) {
	if !IsCredentialsURI(str) {
		return str, "", ""
	}

	re := regexp.MustCompile(userpassRe + `@`)
	// Split the string into two parts by credentials to create a string
	// without the credentials.
	split := re.Split(str, 2)
	newStr := split[0] + split[1]

	// Parse credentials.
	credentialsStr := re.FindString(str)
	credentialsLen := len(credentialsStr) - 1 // We don't need a last '@'.
	credentialsSlice := strings.Split(credentialsStr[:credentialsLen], ":")

	return newStr, credentialsSlice[0], credentialsSlice[1]
}
