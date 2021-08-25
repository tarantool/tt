package main

import (
	"log"

	"github.com/tarantool/tt/cli/cmd"
	"github.com/tarantool/tt/cli/util"
)

func main() {
	defer func() {
		// Recover is a built-in function that regains control of a panicking goroutine.
		// Is case our program panics, recover function will capture the value given to
		// panic function and resume normal execution (handling this error below).
		if r := recover(); r != nil {
			log.Fatalf(
				"%s", util.InternalError("Unhandled internal error: %s",
					func(bool, bool) string { return "Tarantool CLI" }, r))
		}
	}()

	cmd.Execute()
}
