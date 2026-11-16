package app_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/cli/app"
)

// TestAppFiles: the app.file each instance of a cluster config runs with,
// from whichever level sets it, each once; app.module and a level no
// instance inherits from give none.
func TestAppFiles(t *testing.T) {
	t.Parallel()

	config := "app:\n  file: init.lua\n" +
		"groups:\n" +
		"  b:\n    app:\n      file: b.lua\n" +
		"    replicasets:\n      rb:\n        instances:\n          b-1: {}\n" +
		"  a:\n" +
		"    replicasets:\n      r:\n        app:\n          file: r.lua\n" +
		"        instances:\n" +
		"          i-1:\n            app:\n              file: i.lua\n" +
		"          i-2: {}\n" +
		"      s:\n        instances:\n          s-1: {}\n" +
		"  m:\n    app:\n      module: kv\n" +
		"    replicasets:\n      rm:\n        instances:\n          m-1: {}\n"

	files := app.AppFiles(t.Context(), []byte(config))
	assert.ElementsMatch(t, []string{"init.lua", "b.lua", "r.lua", "i.lua"}, files)

	modules := "app:\n  module: kv\n" +
		"groups:\n  g:\n    replicasets:\n      r:\n        instances:\n          i-1: {}\n"
	assert.Empty(t, app.AppFiles(t.Context(), []byte(modules)))
	assert.Empty(t, app.AppFiles(t.Context(), []byte("groups: [")))
	assert.Empty(t, app.AppFiles(t.Context(), nil))
}
