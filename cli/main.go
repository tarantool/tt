package main

import (
	"os"

	"github.com/tarantool/tt/v3/core"
	"github.com/tarantool/tt/v3/standard"
)

func main() {
	modules := standard.Modules()

	modules["builtin"] = core.Builtin

	os.Exit(core.Main(modules))
}
