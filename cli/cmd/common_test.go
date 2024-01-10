package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/configure"
)

func TestCheckConfig(t *testing.T) {
	const expected = configure.ConfigName +
		" not found, you need to create tt environment config with 'tt init'" +
		" or provide exact config location with --cfg option"

	cases := []struct {
		name string
		err  error
	}{
		{"binaries list", internalListModule(&cmdcontext.CmdCtx{}, nil)},
		{"binaries switch", internalSwitchModule(&cmdcontext.CmdCtx{}, nil)},
		{"build", internalBuildModule(&cmdcontext.CmdCtx{}, nil)},
		{"check", internalCheckModule(&cmdcontext.CmdCtx{}, nil)},
		{"clean", internalCleanModule(&cmdcontext.CmdCtx{}, nil)},
		{"create", internalCreateModule(&cmdcontext.CmdCtx{}, nil)},
		{"install", internalInstallModule(&cmdcontext.CmdCtx{}, nil)},
		{"instances", internalInstancesModule(&cmdcontext.CmdCtx{}, nil)},
		{"logrotate", internalLogrotateModule(&cmdcontext.CmdCtx{}, nil)},
		{"pack", internalPackModule(&cmdcontext.CmdCtx{}, nil)},
		{"restart", internalRestartModule(&cmdcontext.CmdCtx{}, nil)},
		{"run", internalRunModule(&cmdcontext.CmdCtx{}, nil)},
		{"start", internalStartModule(&cmdcontext.CmdCtx{}, nil)},
		{"status", internalStatusModule(&cmdcontext.CmdCtx{}, nil)},
		{"stop", internalStopModule(&cmdcontext.CmdCtx{}, nil)},
		{"uninstall", InternalUninstallModule(&cmdcontext.CmdCtx{}, nil)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.ErrorContains(t, tc.err, expected)
		})
	}
}
