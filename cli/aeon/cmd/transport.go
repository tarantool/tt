package cmd

import (
	"fmt"
	"slices"

	"golang.org/x/exp/maps"
)

// Transport is a type, with a restriction on the list of supported connection modes.
type Transport string

// String is used both by fmt.Print and by Cobra in help text.
func (t Transport) String() string {
	return string(t)
}

// Type is only used in Cobra help text.
func (t Transport) Type() string {
	return "MODE"
}

const (
	// TransportPlain used as a default insecure transport mode.
	TransportPlain Transport = "plain"

	// TransportSsl used for encrypted connection mode.
	TransportSsl Transport = "ssl"
)

// ValidTransport is a list of supported transports with its Cobra helping information.
var ValidTransport = map[Transport]string{
	TransportPlain: "unsafe connection mode",
	TransportSsl:   "secure encrypted connection",
}

// Set ensures valid value is applied.
func (t *Transport) Set(v string) error {
	_, ok := ValidTransport[Transport(v)]
	if !ok {
		return fmt.Errorf(`must be %s`, ListValidTransports())
	}
	*t = Transport(v)
	return nil
}

// ListValidTransports returns string representation with list of supported transport modes.
func ListValidTransports() string {
	ks := maps.Keys(ValidTransport)
	slices.Sort(ks)
	return fmt.Sprintf("%v", ks)
}
