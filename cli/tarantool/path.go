package tarantool

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"github.com/tarantool/tt/v3/internal/binary"
)

// pathEnv is the environment variable programs are looked up in.
const pathEnv = "PATH"

// pathMu makes the look at PATH and the change of PrependPath one step, so
// that two calls at once do not both put the directory there.
var pathMu sync.Mutex

// PrependPath puts a directory whose tarantool is executable, the Tarantool
// the rocks tt builds are built against, first in PATH of tt, so that a rock
// build that runs tarantool runs that one: the cmake of a rock looks for it
// with find_program, a command or a make build runs it by name. go-luarocks
// runs the tools of a build with the environment of tt and takes none of its
// own, so PATH of tt is where they look. A project runs its tarantool,
// .tarantool/bin/tarantool, with no .env/activate.sh sourced, and the build
// of its component gets .tarantool/bin first in PATH; this gives a rock the
// same. prefix is the install prefix of executable (ResolvePrefix).
//
// The directory is <prefix>/bin when the tarantool there is executable, else
// the directory of executable when the tarantool there is. For a project,
// executable is the version tt binary installed,
// .tarantool/versions/tarantool/<version>/tarantool, which the link
// .tarantool/bin/tarantool selects, and the directory of the link goes first,
// not that of the version: it is what the build of a component finds first,
// and a link of tt binary install --from-build would put the directory of a
// build of Tarantool in PATH instead.
//
// PATH is left alone when the tarantool it finds is executable already: the
// call is idempotent, and the directory of a tarantool taken from PATH -
// <root>/bin of the user or the system scope, say - does not move ahead of
// the others, which would change the cmake or the cc found in PATH too. It is
// left alone as well when neither directory has executable as its tarantool:
// for no executable, a tt with no Tarantool, one that is not there, one of
// another name that no tarantool links to, or a path that is not absolute -
// a program must not be picked up from wherever the build runs.
func PrependPath(executable, prefix string) error {
	pathMu.Lock()
	defer pathMu.Unlock()

	dir := pathDir(executable, prefix)
	if dir == "" {
		return nil
	}

	value := dir

	inherited := os.Getenv(pathEnv)
	if inherited != "" {
		value += string(os.PathListSeparator) + inherited
	}

	err := os.Setenv(pathEnv, value)
	if err != nil {
		return fmt.Errorf("putting %s first in %s: %w", dir, pathEnv, err)
	}

	return nil
}

// pathDir returns the directory PrependPath puts first in PATH for the
// tarantool executable of prefix, or "" when PATH is to be left alone.
func pathDir(executable, prefix string) string {
	if !filepath.IsAbs(executable) {
		return ""
	}

	target, err := os.Stat(executable)
	if err != nil {
		return ""
	}

	name := binary.ProgramCe.Exec()

	found, err := exec.LookPath(name)
	if err == nil && sameFile(found, target) {
		return ""
	}

	dirs := []string{filepath.Dir(executable)}
	if filepath.IsAbs(prefix) {
		dirs = append([]string{filepath.Join(prefix, binary.BinDirName)}, dirs...)
	}

	for _, dir := range dirs {
		if sameFile(filepath.Join(dir, name), target) {
			return dir
		}
	}

	return ""
}

// sameFile reports whether path, symbolic links followed, is the file of
// target.
func sameFile(path string, target os.FileInfo) bool {
	info, err := os.Stat(path)

	return err == nil && os.SameFile(info, target)
}
