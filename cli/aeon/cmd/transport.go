package cmd

import (
	"fmt"

	"golang.org/x/exp/maps"
)

// AeonTransport is an auxiliary type to constrain the list of stored value.
type AeonTransport string

// String is used both by fmt.Print and by Cobra in help text
func (t AeonTransport) String() string {
	return string(t)
}

// Type is only used in Cobra help text
func (t AeonTransport) Type() string {
	return "MODE"
}

// AeonTransportDefault used as a default transport mode.
var AeonTransportDefault AeonTransport = "plain"

// AeonValidTransport is a list of supported transports with its Cobra helping information.
var AeonValidTransport = map[AeonTransport]string{
	"plain": "unsafe connection mode",
	"ssl":   "secure encrypted connection",
}

// Set ensures valid value is applied.
func (t *AeonTransport) Set(v string) error {
	_, ok := AeonValidTransport[AeonTransport(v)]
	if !ok {
		return fmt.Errorf(`must be %v`, maps.Keys(AeonValidTransport))
	}
	*t = AeonTransport(v)
	return nil
}

// ListValidTransports returns string representation with list of supported transport modes.
func ListValidTransports() string {
	return fmt.Sprintf("%v", maps.Keys(AeonValidTransport))
}
