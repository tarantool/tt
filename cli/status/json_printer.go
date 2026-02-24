package status

import (
	"encoding/json"
	"fmt"
)

// JSONPrinter implements InstanceStatusPrinter for JSON output.
type JSONPrinter struct{}

// NewJSONPrinter creates a new JSONPrinter instance.
func NewJSONPrinter() *JSONPrinter {
	return &JSONPrinter{}
}

// Print outputs the instance status map in JSON format.
func (j JSONPrinter) Print(instances map[string]*instanceStatus) error {
	jsonData, err := json.MarshalIndent(instances, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal instances to JSON: %w", err)
	}
	fmt.Println(string(jsonData))
	return nil
}
