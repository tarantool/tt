package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

func newTcmStartCmd() *cobra.Command {
	var tcmStartCmd = &cobra.Command{
		Use:   "start",
		Short: "Start tcm application",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("start tcm application")
		},
	}
	return tcmStartCmd
}

func NewTcmCmd() *cobra.Command {
	var tcmCmd = &cobra.Command{
		Use:   "tcm",
		Short: "Manage tcm application",
	}
	tcmCmd.AddCommand(
		newTcmStartCmd(),
	)
	return tcmCmd
}
