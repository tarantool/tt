package status

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// YAMLPrinter implements InstanceStatusPrinter for YAML output.
type YAMLPrinter struct{}

// NewYAMLPrinter creates a new YAMLPrinter instance.
func NewYAMLPrinter() *YAMLPrinter {
	return &YAMLPrinter{}
}

// Print outputs the instance status map in YAML format.
func (y YAMLPrinter) Print(instances map[string]*instanceStatus) error {
	yamlData, err := yaml.Marshal(instances)
	if err != nil {
		return fmt.Errorf("failed to marshal instances to YAML: %w", err)
	}
	_, _ = fmt.Fprintln(os.Stdout, string(yamlData))
	return nil
}
