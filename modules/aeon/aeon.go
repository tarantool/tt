// Package aeon is the tt module of the aeon commands: tt aeon connect opens
// an interactive SQL console to an Aeon router over gRPC.
//
// The module is built on the tt SDK alone. [New] is its constructor; a
// distribution of tt lists it in the table of modules it hands to the core.
package aeon

import (
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
)

// New builds the aeon module's commands: the aeon group with its connect
// command, mounted at the root of tt.
func New(services sdk.Services) []sdk.Mount {
	group := &cobra.Command{
		Use:   "aeon",
		Short: "Manage aeon application",
	}
	group.AddCommand(newConnectCmd(services))

	return []sdk.Mount{{Path: "", Cmd: group}}
}
