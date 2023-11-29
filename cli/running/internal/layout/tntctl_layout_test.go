package layout

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTntCtlLayout(t *testing.T) {
	tntCtlLayout := &TntCtlLayout{
		baseDir: "/home/user",
		appName: "app1",
	}

	assert.Equal(t, "/home/user/run/tarantool/app1.pid",
		tntCtlLayout.PidFile("./run/tarantool"))
	assert.Equal(t, "/home/user/run/tarantool/app1.control",
		tntCtlLayout.ConsoleSocket("./run/tarantool"))
	assert.Equal(t, "/home/user/log/tarantool/app1.log",
		tntCtlLayout.LogFile("./log/tarantool"))
	assert.Equal(t, "/home/user/lib/tarantool/app1",
		tntCtlLayout.DataDir("./lib/tarantool"))
	assert.Equal(t, "/home/user/run/tarantool/app1.sock",
		tntCtlLayout.BinaryPort("./run/tarantool"))
}

func TestTntCtlLayoutNewErr(t *testing.T) {
	for _, args := range [...][]string{
		{"/home", ""},
		{"", "app"},
	} {
		tntCtlLayout, err := NewTntCtlLayout(args[0], args[1])
		assert.Nil(t, tntCtlLayout)
		assert.ErrorContains(t, err, "cannot be empty")
	}
}
