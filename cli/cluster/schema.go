package cluster

// SchemaPath describes a validation schema for a configuration.
type SchemaPath struct {
	// Path of a configuration value.
	Path []string
	// Validator to validate the configuration value.
	Validator Validator
}

// Validate validates a configuration with the schema.
func Validate(config *Config, schema []SchemaPath) error {
	var errs []error
	for _, p := range schema {
		if value, err := config.Get(p.Path); err == nil {
			if _, err := p.Validator.Validate(value); err != nil {
				errs = append(errs, wrapValidateErrors(p.Path, err))
			}
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return wrapValidateErrors(nil, errs...)
}
