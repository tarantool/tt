package status

import (
	"fmt"

	"gopkg.in/yaml.v2"
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
	fmt.Println(string(yamlData))
	return nil
}
