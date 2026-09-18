package cmd

import "github.com/spf13/cobra"

// errorsHelpText is the body of `tt help errors`. The code lines are written one
// per line with two spaces after the code, so wrapHelp continues each under its
// description rather than under the number.
const errorsHelpText = `Exit codes of the manifest commands: tt package and its subcommands, tt registry, tt new, and tt test until the tests start.

  0  Success.
  1  The request cannot be carried out as given, and the fix is on the caller's side: the manifest does not parse or is invalid, the lock is out of date under --locked, a dependency does not resolve, a package collides with one already installed, a registry URL is malformed, a path does not exist.
  2  The system the command ran on failed: a component build backend failed, a rock server could not be reached or did not answer in time, the filesystem refused an operation for want of permission or space.
  3  A multi-package operation partly succeeded: some of its targets went through and some did not.

Which failure within a code it was is carried by the error message, not by a further number.

Once tt test has started luatest, its exit code is luatest's own. tt run replaces itself with Tarantool, so its exit code is Tarantool's.`

// NewErrorsHelpTopic creates the errors help topic, which explains the exit
// codes of the manifest commands. It has no Run, which is what makes it a help
// topic rather than a command: tt help errors prints it, and the command list
// leaves it out.
func NewErrorsHelpTopic() *cobra.Command {
	return &cobra.Command{
		Use:   "errors",
		Short: "Exit codes of the manifest commands",
		Long:  errorsHelpText,
	}
}
