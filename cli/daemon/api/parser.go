package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/mitchellh/mapstructure"
)

var (
	errFailedToParseCommandParams = errors.New("failed to parse command params: \"")
	errFailedToReadRequestBody    = errors.New("failed to read request body: ")
)

// commandJSON describes the tt command sent using the HTTP API.
type commandJSON struct {
	// Name is a name of the command.
	// Now available: start, stop, status, restart.
	Name string `json:"command_name"`
	// Params are command parameters.
	Params []string `json:"params"`
}

// command describes the tt command.
type command struct {
	// Name is name of the command.
	Name string `mapstructure:"name"`
	// Params are command parameters.
	Params []string `mapstructure:"params"`
}

// parseCommand decodes JSON, checks the parameters
// and parses them to a "command" struct.
func parseCommand(r io.Reader, cmd *command) (string, error) {
	// Read data to log raw JSON request.
	bodyBytes, err := io.ReadAll(r)
	if err != nil {
		return err.Error(), fmt.Errorf("%w%s", errFailedToReadRequestBody, err.Error())
	}

	rawBody := string(bodyBytes)

	// Decode JSON.
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.DisallowUnknownFields()

	var cmdJSON commandJSON

	err = decoder.Decode(&cmdJSON)
	if err != nil {
		return rawBody, fmt.Errorf("failed to decode the command: %w", err)
	}

	// Parse cmdJSON to a "command" structure.
	// Additionally, all types of parameters will be checked.
	err = mapstructure.Decode(cmdJSON, cmd)
	if err != nil {
		return rawBody, fmt.Errorf("%w%v\"", errFailedToParseCommandParams, err.Error())
	}

	return rawBody, nil
}
