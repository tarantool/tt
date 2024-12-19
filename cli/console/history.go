package console

const (
	DefaultHistoryFileName = ".tarantool_history"
	DefaultHistoryLines    = 10000
)

type History interface {
	Open(fileName string, maxCommands int) error
	AppendCommand(input string)
	Command() []string
	Stop() // Q: А нужно ли иметь такой метод?
}
