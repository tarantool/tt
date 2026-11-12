package analyzer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// projectDirName is the directory of a project that belongs to the
	// machine rather than to the repository: tt keeps the registry, the
	// configuration and the other files of this host there.
	projectDirName = ".tarantool"
	// ConfigDir is the directory, relative to the project, of the
	// configuration tt writes for EmmyLua: under .tarantool, since it names
	// the paths of this machine.
	ConfigDir = projectDirName + "/emmylua"
	// ConfigFileName is the file name of that configuration.
	ConfigFileName = "emmyrc.json"
	// ConfigEnv is the variable emmylua_ls reads a configuration from, after
	// the user's own and before the project's.
	ConfigEnv = "EMMYLUALS_CONFIG"
	// rocksDirName is the rock tree of the project.
	rocksDirName = ".rocks"
	// buildDirName is the directory tt package build builds the project in,
	// out of its sources: it holds copies of the Lua files of the project
	// (_build/rock/<product>/) and of its path dependencies, which are not
	// the code of the project. The constant of the build is in tt's root
	// module, which the module cannot import.
	buildDirName = "_build"
	// BuildDirGlob matches every file under buildDirName, relative to the
	// root of the project, for EmmyLua's ignoreGlobs and luafmt's --exclude
	// alike. luafmt matches no file with "_build/": the glob needs the **.
	BuildDirGlob = buildDirName + "/**"
	// StubsDir is where Tarantool installs its EmmyLua stubs, relative to its
	// prefix (gh-13022, Tarantool 3.9 and newer).
	StubsDir = "share/tarantool/emmylua"
	// runtimeVersion is the Lua of Tarantool, as EmmyLua's runtime.version
	// names it; FormatLevel is the same for luafmt.
	runtimeVersion = "LuaJIT"
)

// projectConfigNames are the configurations of a project, in the order
// emmylua_check reads them from the root of the workspace.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var projectConfigNames = []string{".luarc.json", ".emmyrc.json"}

// tarantoolGlobals are the globals Tarantool defines beyond those of LuaJIT,
// as Tarantool's own .emmyrc.json lists them.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var tarantoolGlobals = []string{"box", "misc", "package", "tonumber64", "utf8", "_TARANTOOL"}

// Layout is what tt knows of the project that EmmyLua needs.
type Layout struct {
	// ProjectDir is the root of the workspace: the project, or outside every
	// project the directory the command works in.
	ProjectDir string
	// Stubs is the directory of the EmmyLua stubs of the Tarantool the
	// project runs on; empty when that Tarantool has none.
	Stubs string
	// Extra are more directories of modules to read as libraries, after the
	// others: those --library names.
	Extra []string
	// Path is where WriteConfig writes the configuration; empty is the
	// project's, under ConfigDir. Outside every project it is in the cache
	// (GlobalConfigPath), so that nothing is written into the directory.
	Path string
}

// Libraries are the directories EmmyLua reads as libraries: the stubs of
// Tarantool, the modules the rocks of the project install and Extra, those
// that exist, in that order.
func (l Layout) Libraries() []string {
	candidates := append([]string{
		l.Stubs,
		filepath.Join(l.ProjectDir, rocksDirName, "share", "tarantool"),
		filepath.Join(l.ProjectDir, rocksDirName, "share", "lua", "5.1"),
	}, l.Extra...)

	libraries := make([]string, 0, len(candidates))

	for _, dir := range candidates {
		if dir == "" {
			continue
		}

		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			libraries = append(libraries, dir)
		}
	}

	return libraries
}

// Config is the configuration tt gives EmmyLua: LuaJIT, the globals of
// Tarantool, the libraries of Layout, and what of the project is not its
// code. The project's own configuration is read after it, so it can add to
// any of these: EmmyLua merges the objects and joins the lists.
func (l Layout) Config() map[string]any {
	comment := "Written by tt emmylua for this machine, anew on every run: " +
		"put the settings of the project into .emmyrc.json."
	if l.Path != "" {
		comment = "Written by tt emmylua for " + l.ProjectDir + ", outside every project, " +
			"anew on every run: put the settings of the directory into .emmyrc.json there."
	}

	return map[string]any{
		"$comment": comment,
		"runtime": map[string]any{
			"version": runtimeVersion,
		},
		"workspace": map[string]any{
			"library": l.Libraries(),
			"ignoreGlobs": []string{
				rocksDirName + "/**/*.lua",
				projectDirName + "/**/*.lua",
				BuildDirGlob,
			},
		},
		"diagnostics": map[string]any{
			"globals": tarantoolGlobals,
		},
	}
}

// ConfigPath is the path of the configuration tt writes: Path, or that of
// the project.
func (l Layout) ConfigPath() string {
	if l.Path != "" {
		return l.Path
	}

	return filepath.Join(l.ProjectDir, filepath.FromSlash(ConfigDir), ConfigFileName)
}

// WriteConfig writes Config to ConfigPath and returns the path.
func (l Layout) WriteConfig() (string, error) {
	data, err := json.MarshalIndent(l.Config(), "", "  ")
	if err != nil {
		return "", fmt.Errorf("rendering the EmmyLua configuration: %w", err)
	}

	path := l.ConfigPath()

	err = os.MkdirAll(filepath.Dir(path), dirPerm)
	if err != nil {
		return "", fmt.Errorf("writing the EmmyLua configuration: %w", err)
	}

	err = writeFileAtomic(path, append(data, '\n'))
	if err != nil {
		return "", err
	}

	return path, nil
}

// ConfigFiles are the configurations a run of emmylua_check or
// emmylua_doc_cli reads, in order: generated, then those of the project.
// Given configurations, the programs read no others, so the project's are
// named here.
func (l Layout) ConfigFiles(generated string) []string {
	files := []string{generated}

	for _, name := range projectConfigNames {
		path := filepath.Join(l.ProjectDir, name)

		info, err := os.Stat(path)
		if err == nil && info.Mode().IsRegular() {
			files = append(files, path)
		}
	}

	return files
}

// FindStubs returns the directory of the EmmyLua stubs of the Tarantool at
// executable: under the parent of the directory of the executable, as a
// package installs it, the path as it is and then with its links resolved.
// It returns "" when neither has them: a Tarantool older than 3.9, or one
// tt installed, which keeps the executable and the headers only.
func FindStubs(executable string) string {
	if executable == "" {
		return ""
	}

	resolved, err := filepath.EvalSymlinks(executable)
	if err != nil {
		resolved = executable
	}

	for _, exe := range []string{executable, resolved} {
		dir := filepath.Join(filepath.Dir(filepath.Dir(exe)), filepath.FromSlash(StubsDir))

		info, err := os.Stat(dir)
		if err == nil && info.IsDir() {
			return dir
		}
	}

	return ""
}

// ProgramPath returns the path of program in the rocks of the project:
// .rocks/bin/<program>. When it is not there, the error wraps
// ErrNotPrepared.
func ProgramPath(projectDir string, program Program) (string, error) {
	path := filepath.Join(projectDir, rocksDirName, rockBinDir, string(program))

	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: no %s in %s; run tt emmylua prepare",
			ErrNotPrepared, program, filepath.Join(rocksDirName, rockBinDir))
	}

	if err != nil {
		return "", fmt.Errorf("finding %s: %w", program, err)
	}

	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %s is not a program", ErrNotPrepared, path)
	}

	return path, nil
}

// writeFileAtomic writes data to path through a temporary file in the same
// directory and a rename.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(filePerm)
	}

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	err = os.Rename(tmp.Name(), path)
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
