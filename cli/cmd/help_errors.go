package cmd

import "github.com/spf13/cobra"

// errorsHelpText is the body of `tt help errors`. The code lines are written one
// per line with two spaces after the code, so wrapHelp continues each under its
// description rather than under the number. Each paragraph is one line of the
// text, split across literals only to keep the source lines short.
const errorsHelpText = "Exit codes of tt commands.\n" +
	"\n" +
	"  0  Success.\n" +
	"  1  The request cannot be carried out as given, and the fix is on the caller's side: " +
	"the manifest does not parse or is invalid, the lock is out of date under --locked, " +
	"a dependency does not resolve, a package collides with one already installed, " +
	"a registry URL is malformed, a path does not exist.\n" +
	"  2  The system the command ran on failed: a component build backend failed, " +
	"a server could not be reached or did not answer in time, a host name did not resolve, " +
	"the filesystem refused an operation for want of permission or space.\n" +
	"  3  A multi-package operation partly succeeded: " +
	"some of its targets went through and some did not.\n" +
	"\n" +
	"Which failure within a code it was is carried by the error message, " +
	"not by a further number.\n" +
	"\n" +
	"A command provided by an external module exits with the module's own code. " +
	"Once tt test has started luatest, its exit code is luatest's own. " +
	"tt run replaces itself with Tarantool, so its exit code is Tarantool's."

// NewErrorsHelpTopic creates the errors help topic, which explains the exit
// codes of tt commands. It has no Run, which is what makes it a help topic
// rather than a command: tt help errors prints it, and the command list
// leaves it out.
func NewErrorsHelpTopic() *cobra.Command {
	return &cobra.Command{
		Use:   "errors",
		Short: "Exit codes of tt commands",
		Long:  errorsHelpText,
	}
}
