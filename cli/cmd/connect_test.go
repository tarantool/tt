package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/connect"
)

func TestGenMaxHistoryOption(t *testing.T) {
	t.Run("not configured", func(t *testing.T) {
		opts := config.CliOpts{}
		actual := getMaxHistoryOutputLen(&opts)
		assert.Equal(t, connect.DefaultOutputHistorySize, actual)
	})
	t.Run("invalid configuration", func(t *testing.T) {
		opts := config.CliOpts{Env: &config.TtEnvOpts{OutputHistoryMax: 0}}
		actual := getMaxHistoryOutputLen(&opts)
		assert.Equal(t, connect.DefaultOutputHistorySize, actual)
	})
	t.Run("correct configuration", func(t *testing.T) {
		opts := config.CliOpts{Env: &config.TtEnvOpts{OutputHistoryMax: 2}}
		actual := getMaxHistoryOutputLen(&opts)
		assert.Equal(t, 2, actual)
	})
}
