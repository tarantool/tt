package layout

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMultiInstLayout(t *testing.T) {
	tntCtlLayout := &MultiInstLayout{
		baseDir:      "/home/user",
		appName:      "app1",
		instanceName: "master",
	}

	assert.Equal(t, "/home/user/var/run/app1/master/master.pid",
		tntCtlLayout.PidFile("var/run"))
	assert.Equal(t, "/home/user/var/run/app1/master/master.control",
		tntCtlLayout.ConsoleSocket("var/run"))
	assert.Equal(t, "/home/user/var/log/app1/master/master.log",
		tntCtlLayout.LogFile("var/log"))
	assert.Equal(t, "/home/user/var/lib/app1/master",
		tntCtlLayout.DataDir("var/lib"))
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
