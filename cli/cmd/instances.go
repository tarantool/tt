package cmd

import (
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
)

// instanceOptions are the options an instance command finds the
// applications of its targets with: the project search starts in the -C
// directory, where the command has the flag, or else in the working
// directory, and the files the resolver reads go through the integrity
// checks. tt.yaml plays no part.
func instanceOptions(cmdCtx *cmdcontext.CmdCtx) (app.Options, error) {
	start, err := absoluteWorkingDir()
	if err != nil {
		return app.Options{}, err
	}

	return app.Options{
		Scope:     "",
		Template:  "",
		Start:     start,
		Dir:       "",
		Integrity: cmdCtx.Integrity,
	}, nil
}
