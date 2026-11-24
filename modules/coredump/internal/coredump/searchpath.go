package coredump

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// defaultSearchPath is the PATH tt looks the programs it runs up in, and
// gives them, when its own PATH is empty or unset. The kernel runs the pipe
// of kernel.core_pattern with no environment at all: fs/coredump.c gives
// call_usermodehelper_setup no envp, which kernel_execve of kernel/umh.c
// runs the program with. bash and dash of Debian and Ubuntu take this PATH
// when they are given none, and systemd gives it to its units of a host of
// a split /usr; Go has none of its own, and tt found no bash there.
const defaultSearchPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// pathVariable is the name of the variable of PATH.
const pathVariable = "PATH"

// executableBits are the bits of the mode of a file anyone may run.
const executableBits = 0o111

// lookPath finds the program name as exec.LookPath does in PATH, or, when
// PATH is empty or unset, in defaultSearchPath. A name with a slash is not
// looked up.
func lookPath(name string) (string, error) {
	if os.Getenv(pathVariable) != "" || filepath.Base(name) != name {
		path, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("%w", err)
		}

		return path, nil
	}

	for _, dir := range filepath.SplitList(defaultSearchPath) {
		path := filepath.Join(dir, name)

		info, err := os.Stat(path)
		if err == nil && !info.IsDir() && info.Mode().Perm()&executableBits != 0 {
			return path, nil
		}
	}

	return "", &exec.Error{
		Name: name,
		Err:  fmt.Errorf("%w, which is empty, nor in %s", exec.ErrNotFound, defaultSearchPath),
	}
}

// commandEnv returns environ, an environment of the form of os.Environ,
// for a program tt runs: as it is when it has a PATH that is not empty,
// and otherwise with PATH defaultSearchPath, as the programs the pack
// script and gdb.sh run are looked up by their names, and so are those of
// tar and gdb.
func commandEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+1)
	prefix := pathVariable + "="

	for _, variable := range environ {
		value, found := strings.CutPrefix(variable, prefix)
		if found && value != "" {
			return environ
		}

		if !found {
			env = append(env, variable)
		}
	}

	return append(env, prefix+defaultSearchPath)
}

// command returns the command that runs the program name with args, as
// exec.CommandContext does, in the environment of tt with a PATH (see
// commandEnv). With PATH empty or unset, the program is looked up in
// defaultSearchPath (see lookPath), and a command of a program not found
// there fails to start with the error of the lookup, as one of
// exec.CommandContext does; its first argument is name all the same.
func command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)

	cmd.Env = commandEnv(os.Environ())

	if os.Getenv(pathVariable) == "" && filepath.Base(name) == name {
		path, err := lookPath(name)
		if err == nil {
			cmd.Path = path
		}

		cmd.Err = err
	}

	return cmd
}
