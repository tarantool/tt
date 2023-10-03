package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRootFlags(t *testing.T) {
	rootCmd = NewCmdRoot()
	rootCmd.ParseFlags([]string{"--cfg", "one.yaml", "rocks", "--cfg", "second.yaml"})
	assert.Equal(t, cmdCtx.Cli.ConfigPath, "one.yaml")
}
