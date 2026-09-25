package layout

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMultiInstLayout(t *testing.T) {
	miLayout := &MultiInstLayout{
		baseDir:      "/home/user/app1",
		appName:      "app1",
		instanceName: "master",
	}

	// Relative paths.
	assert.Equal(t, "/home/user/app1/var/run/master/tt.pid", miLayout.PidFile("var/run"))
	assert.Equal(t, "/home/user/app1/var/run/master/tarantool.control",
		miLayout.ConsoleSocket("var/run"))
	assert.Equal(t, "/home/user/app1/var/log/master/tt.log", miLayout.LogFile("var/log"))
	assert.Equal(t, "/home/user/app1/var/lib/master", miLayout.DataDir("var/lib"))

	// Absolute paths.
	assert.Equal(t, "/var/run/app1/master/tt.pid", miLayout.PidFile("/var/run"))
	assert.Equal(t, "/var/run/app1/master/tarantool.control",
		miLayout.ConsoleSocket("/var/run"))
	assert.Equal(t, "/var/log/app1/master/tt.log", miLayout.LogFile("/var/log"))
	assert.Equal(t, "/var/lib/app1/master", miLayout.DataDir("/var/lib"))
	assert.Equal(t, "/var/run/app1/master/tarantool.sock", miLayout.BinaryPort("/var/run"))
}

func TestMultiIntLayoutNewErr(t *testing.T) {
	for _, args := range [...][]string{
		{"/home", "", "master"},
		{"", "app", "inst"},
		{"/home", "app", ""},
	} {
		tntCtlLayout, err := NewMultiInstLayout(args[0], args[1], args[2])
		assert.Nil(t, tntCtlLayout)
		assert.ErrorContains(t, err, "cannot be empty")
	}
}
