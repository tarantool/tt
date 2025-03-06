package dial

import "fmt"

const (
	transportDefault string = ""
	transportPlain   string = "plain"
	transportSsl     string = "ssl"
)

// Transport is a type, with a restriction on the list of supported connection modes.
type Transport int

const (
	// TransportDefault used as default value.
	TransportDefault Transport = iota
	// TransportPlain used for insecure connection mode.
	TransportPlain
	// TransportSSL used for encrypted connection mode.
	TransportSsl
)

// Parse returns Transport type according to the input string.
// Under normal circumstances input should be "ssl" or "plain".
func Parse(tr string) (Transport, error) {
	switch tr {
	case transportDefault:
		return TransportDefault, nil
	case transportPlain:
		return TransportPlain, nil
	case transportSsl:
		return TransportSsl, nil
	default:
		return TransportDefault, fmt.Errorf("unknown Transport type: %s", tr)
	}
}

// String returns a string representation of the Transport.
func (t Transport) String() string {
	switch t {
	case TransportDefault:
		return transportDefault
	case TransportPlain:
		return transportPlain
	case TransportSsl:
		return transportSsl
	default:
		return fmt.Sprintf("Transport(%d)", t)
	}
}
