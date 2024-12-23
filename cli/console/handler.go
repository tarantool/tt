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
	// Expecting that result type implements Formatter interface.
	Execute(input string) any

	// Close notify handler to terminate execution and close any opened streams.
	Close()
}
