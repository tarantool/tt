package main

import (
	"os"

	"github.com/tarantool/tt/v3/cli/cmd"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/cli/version"
)

func main() {
	defer func() {
		// Recover is a built-in function that regains control of a panicking goroutine.
		// Is case our program panics, recover function will capture the value given to
		// panic function and resume normal execution (handling this error below).
		if r := recover(); r != nil {
			exitcode.Exit(
				util.InternalError("Unhandled internal error: %s", version.GetVersion, r))
		}
	}()

	os.Exit(cmd.Main())
}
