package console

// HistoryKeeper introduce methods to keep command history in some external place.
type HistoryKeeper interface {
	// AppendCommand add new entered command to storage.
	AppendCommand(input string)
	// Commands return list of saved commands.
	Commands() []string
	// Close method notifies the repository that there will be no new commands.
	Close()
}
