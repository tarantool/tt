package backend

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/scope"
	"github.com/tarantool/tt/v3/internal/binary"
)

// pathKey is the environment variable programs are looked up in.
const pathKey = "PATH"

// ProjectBinDir is the directory a project keeps the programs it installs for
// itself in: <project>/.tarantool/bin, where tt binary --scope project links
// its binaries and tt wasm prepare links tarawasm. A build looks there first,
// so a component or a hook can name such a program as a bare command without
// the shell having sourced .env/activate.sh.
func ProjectBinDir(projectRoot string) string {
	return filepath.Join(projectRoot, scope.ProjectDirName, binary.BinDirName)
}

// withProjectBin returns environ with the project's bin directory in front of
// its PATH, when the project has one. The entry is appended rather than
// edited in place: exec.Cmd keeps the last of duplicate keys, so the value
// below wins over the inherited one and over a PATH from [build].env, which
// it extends rather than replaces.
func withProjectBin(environ []string, projectRoot string) []string {
	if projectRoot == "" {
		return environ
	}

	dir := ProjectBinDir(projectRoot)

	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return environ
	}

	value := dir

	inherited, ok := lookupPath(environ)
	if ok && inherited != "" {
		value += string(os.PathListSeparator) + inherited
	}

	return append(environ, pathKey+"="+value)
}

// lookupPath returns the last PATH of environ, the one exec.Cmd uses.
func lookupPath(environ []string) (string, bool) {
	return lookupEnv(environ, pathKey)
}

// lookupEnv returns the last value of key in environ, the one exec.Cmd uses.
func lookupEnv(environ []string, key string) (string, bool) {
	for _, entry := range slices.Backward(environ) {
		if value, ok := strings.CutPrefix(entry, key+"="); ok {
			return value, true
		}
	}

	return "", false
}

// lookPathIn finds the program name the way exec.LookPath does, but in the
// PATH of environ, the child's environment, rather than in tt's own: that is
// where the project's bin directory was put. A name with a separator is a
// path and is returned as it is, and so is a name found nowhere, so that
// exec reports the failure in its usual words. Relative PATH entries are
// skipped: a program must not be picked up from wherever the build runs.
func lookPathIn(name string, environ []string) string {
	if strings.ContainsRune(name, os.PathSeparator) {
		return name
	}

	value, ok := lookupPath(environ)
	if !ok {
		return name
	}

	for _, dir := range filepath.SplitList(value) {
		if !filepath.IsAbs(dir) {
			continue
		}

		candidate := filepath.Join(dir, name)

		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}

	return name
}
