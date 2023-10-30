package env

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/configure"
)

func Test_CreateEnvString(t *testing.T) {
	cliOpts := configure.GetDefaultCliOpts()
	cliOpts.Env.BinDir = "foo/bin/"
	cliOpts.Env.IncludeDir = "bar/include/"
	assert.Contains(t, CreateEnvString(cliOpts),
		"\nexport TARANTOOL_DIR=bar/include/include\n")
	assert.Contains(t, CreateEnvString(cliOpts),
		"export PATH=foo/bin/:")
}
