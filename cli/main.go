package main

import (
	"os"

	"github.com/tarantool/tt/modules/aeon"
	"github.com/tarantool/tt/v3/core"
)

func main() {
	os.Exit(core.Main(core.Modules{"builtin": core.Builtin, "aeon": aeon.New}))
}
