package console

// Formatter interface provide common interface for console Handlers to format execution results.
type Formatter interface {
	// Format result data according fmt settings and return string for printing.
	Format(fmt Format) (string, error)
}
