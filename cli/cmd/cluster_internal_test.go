package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigFileTarget(t *testing.T) {
	dir := t.TempDir()
	colon := filepath.Join(dir, "odd:name")
	require.NoError(t, os.WriteFile(colon, []byte("{}\n"), 0o600))

	cases := []struct {
		target string
		path   string
		inst   string
		isFile bool
	}{
		{"app", "", "", false},
		{"app:inst", "", "", false},
		{"config.yaml", "config.yaml", "", true},
		{"config.YML", "config.YML", "", true},
		{"config.yaml:inst", "config.yaml", "inst", true},
		{"./config", "./config", "", true},
		{"./config:inst", "./config", "inst", true},
		{"/etc/app/config.yaml:inst", "/etc/app/config.yaml", "inst", true},
		{"/etc/a:b/config.yaml", "/etc/a:b/config.yaml", "", true},
		{"/etc/a:b/config", "/etc/a:b/config", "", true},
		{"./config.yaml:", "./config.yaml:", "", true},
		// An existing file is itself, colon or not.
		{colon, colon, "", true},
	}

	for _, tc := range cases {
		t.Run(tc.target, func(t *testing.T) {
			path, inst, isFile := configFileTarget(tc.target)
			assert.Equal(t, tc.isFile, isFile)
			assert.Equal(t, tc.path, path)
			assert.Equal(t, tc.inst, inst)
		})
	}
}

func TestConfigFileTargetNotAnyFile(t *testing.T) {
	// A file of the current directory named as an application, its binary
	// say, does not take the place of the application.
	t.Chdir(t.TempDir())
	require.NoError(t, os.WriteFile("app", []byte("binary"), 0o600))

	_, _, isFile := configFileTarget("app")
	assert.False(t, isFile)

	path, _, isFile := configFileTarget("./app")
	assert.True(t, isFile)
	assert.Equal(t, "./app", path)
}
