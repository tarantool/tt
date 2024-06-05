package formatter

// lazyMessage is used for lazy Unmarshal.
type lazyMessage struct {
	unmarshal func(any) error
}

// UnmarshalYAML makes lazyMessage an instance of yaml.Unmarshaler.
func (msg *lazyMessage) UnmarshalYAML(unmarshal func(any) error) error {
	msg.unmarshal = unmarshal
	return nil
}

// Unmarshal the lazyMessage.
func (msg *lazyMessage) Unmarshal(v any) error {
	if msg.unmarshal == nil {
		return nil
	}
	return msg.unmarshal(v)
}
