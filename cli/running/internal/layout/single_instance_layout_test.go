package layout

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSingleInstanceLayout(t *testing.T) {
	tntCtlLayout := &SingleInstanceLayout{
		baseDir: "/home/user",
		appName: "app1",
	}

	assert.Equal(t, "/home/user/var/run/app1/app1.pid",
		tntCtlLayout.PidFile("var/run"))
	assert.Equal(t, "/home/user/var/run/app1/app1.control",
		tntCtlLayout.ConsoleSocket("var/run"))
	assert.Equal(t, "/home/user/var/log/app1/app1.log",
		tntCtlLayout.LogFile("var/log"))
	assert.Equal(t, "/home/user/var/lib/app1",
		tntCtlLayout.DataDir("var/lib"))
}

func TestSingleInstLayoutNewErr(t *testing.T) {
	for _, args := range [...][]string{
		{"/home", ""},
		{"", "app"},
	} {
		tntCtlLayout, err := NewSingleInstanceLayout(args[0], args[1])
		assert.Nil(t, tntCtlLayout)
		assert.ErrorContains(t, err, "cannot be empty")
	}
}
