// Package console runs an interactive console over a command processor.
//
// A [Console] reads statements from the user - with line editing,
// completion and history when stdin is a terminal, line by line when it is
// a pipe - hands each complete statement to a [Handler] and prints what the
// handler returns in a [Format]. A [HistoryKeeper], such as a [History] kept
// in a file, remembers the statements between sessions.
//
// The console ends the process itself when the connection behind the
// handler closes, through the Exit function the caller passes in
// [ConsoleOpts]: the prompt library it runs on gives it no other way out.
package console
