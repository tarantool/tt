package console

import "github.com/tarantool/go-prompt"

// Handler is a auxiliary abstraction to isolate the console from
// the implementation of a particular instruction processor.
type Handler interface {
	// Title return name of instruction processor instance.
	Title() string
	// Validate the input string.
	Validate(input string) bool
	// Complete checks the input and return available variants to continue typing.
	Complete(input prompt.Document) []prompt.Suggest
	// Execute accept input to perform actions defined by client implementation.
	// The type of the resulting object can be anything, and no special processing is expected.
	// It must provide one of the following interfaces:
	// - Formatter (for the best case).
	// - Stringer
	// - error
	// Otherwise, when displaying the object in the console, a message that the object
	// cannot be rendered correctly will be displayed.
	Execute(input string) any
	// Close notify handler to terminate execution and close any opened streams.
	Close()
}
