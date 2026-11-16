package util

import (
	"bytes"
	"encoding/json"
)

// MarshalJSON is json.MarshalIndent, or json.Marshal for an empty indent,
// that leaves <, > and & as they are: JSON that tt prints for a user, such
// as a constraint ">=3.0.0", reads as written rather than as ">=3.0.0".
// The escapes json.Marshal makes are there for JSON embedded in HTML, which
// the output of a command is not.
func MarshalJSON(value any, indent string) ([]byte, error) {
	var buf bytes.Buffer

	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)

	if indent != "" {
		encoder.SetIndent("", indent)
	}

	err := encoder.Encode(value)
	if err != nil {
		return nil, err //nolint:wrapcheck // The error of json.Marshal, as it is.
	}

	// Encode ends the value with a newline, json.Marshal does not.
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
