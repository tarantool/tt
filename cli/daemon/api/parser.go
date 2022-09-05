package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"

	"github.com/mitchellh/mapstructure"
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
	Name string
	// Params are command parameters.
	Params []string
}

// parseCommand decodes JSON, checks the parameters
// and parses them to a "command" struct.
func parseCommand(r io.Reader, cmd *command) (string, error) {
	// Read data to log raw JSON request.
	bodyBytes, err := ioutil.ReadAll(r)
	if err != nil {
		return err.Error(), fmt.Errorf(`Failed to read request body: %s`, err.Error())
	}

	rawBody := string(bodyBytes)

	// Decode JSON.
	decoder := json.NewDecoder(bytes.NewReader(bodyBytes))
	decoder.DisallowUnknownFields()

	var cmdJSON commandJSON
	if err := decoder.Decode(&cmdJSON); err != nil {
		return rawBody, err
	}

	// Parse cmdJSON to a "command" structure.
	// Additionally, all types of parameters will be checked.
	if err := mapstructure.Decode(cmdJSON, cmd); err != nil {
		return rawBody, fmt.Errorf(`Failed to parse command params: "%v"`, err.Error())
	}

	return rawBody, nil
}
