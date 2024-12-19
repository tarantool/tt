package console

import "github.com/tarantool/go-prompt"

// HandlerResult structure of data records.
// Map keys is names of columns. And map value is content of column.
// TODO: Solve what need return to make easy apply Formatter.
// Possible: can we make it return an interface with methods to be handled in Formatter?
type HandlerResult map[string]any

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
	Execute(input string) HandlerResult

	// Stop notify handler to terminate execution and close any opened streams.
	Stop() // Q: А нужно ли иметь такой метод?
}
