package extmod_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/core/internal/extmod"
	"github.com/tarantool/tt/v3/core/internal/mount"
)

// errRefused is what the integrity checks of these tests refuse a file
// with.
var errRefused = errors.New("refused by the integrity checks")

// tree is a root like tt's, with the commands it was made with, to mount
// external modules on. The modules write to out and errOut.
type tree struct {
	t        *testing.T
	root     *cobra.Command
	registry *mount.Registry
	log      *logtest.Recorder
	out      *bytes.Buffer
	errOut   *bytes.Buffer
	// forceInternal is -I.
	forceInternal bool
	// refused are the files the integrity checks refuse when they are opened.
	refused []string
	// refusedAtEnd are the files the integrity checks refuse only once they
	// are read to the end, as a check that hashes a file while it is read
	// does.
	refusedAtEnd []string

	mu sync.Mutex
	// opened are the files read through the integrity checks.
	opened []string
}

// newTree returns a tree with the commands of entries hung on its root.
func newTree(t *testing.T, entries ...mount.Entry) *tree {
	t.Helper()

	root := &cobra.Command{Use: "tt"}
	root.Flags().BoolP("verbose", "V", false, "")

	registry, err := mount.Hang(root, entries, reserved())
	require.NoError(t, err)

	var out, errOut bytes.Buffer

	root.SetOut(&out)
	root.SetErr(&errOut)

	_, records := logtest.New(t)

	return &tree{t: t, root: root, registry: registry, log: records, out: &out, errOut: &errOut}
}

// reserved returns what tt reserves.
func reserved() mount.Reserved {
	flags := pflag.NewFlagSet("reserved", pflag.ContinueOnError)
	flags.BoolP("help", "h", false, "")
	flags.StringP("directory", "C", "", "")

	return mount.Reserved{
		Names: []string{"help", cobra.ShellCompRequestCmd, cobra.ShellCompNoDescRequestCmd},
		Flags: flags,
	}
}

// open opens path unless it is one of the refused files, and records it.
func (tr *tree) open(path string) (io.ReadCloser, error) {
	tr.mu.Lock()

	tr.opened = append(tr.opened, path)

	tr.mu.Unlock()

	if slices.Contains(tr.refused, path) {
		return nil, errRefused
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}

	if slices.Contains(tr.refusedAtEnd, path) {
		return refusedAtEnd{file: file}, nil
	}

	return file, nil
}

// refusedAtEnd reads a file the integrity checks refuse once it is read to
// the end.
type refusedAtEnd struct {
	file *os.File
}

// Read reads the file, failing with errRefused at its end.
func (r refusedAtEnd) Read(p []byte) (int, error) {
	n, err := r.file.Read(p)
	if errors.Is(err, io.EOF) {
		return n, errRefused
	}

	return n, err
}

// Close closes the file.
func (r refusedAtEnd) Close() error {
	return r.file.Close()
}

// openedFiles returns the files read through the integrity checks so far.
func (tr *tree) openedFiles() []string {
	tr.mu.Lock()
	defer tr.mu.Unlock()

	return slices.Clone(tr.opened)
}

// mount mounts the external modules path lists on the tree.
func (tr *tree) mount(path string) ([]extmod.Manifest, error) {
	logger, records := logtest.New(tr.t)

	tr.log = records

	return extmod.Mount(tr.root, extmod.Options{
		Path:          path,
		Open:          tr.open,
		ForceInternal: tr.forceInternal,
		Registry:      tr.registry,
		Reserved:      reserved(),
		Log:           logger,
		Streams:       output.Streams{In: strings.NewReader(""), Out: tr.out, Err: tr.errOut},
	})
}

// run executes args on the tree and returns the command run and its error.
func (tr *tree) run(args ...string) (*cobra.Command, error) {
	tr.root.SetArgs(args)

	return tr.root.ExecuteC()
}

// find returns the command at the root named name, or nil.
func (tr *tree) find(name string) *cobra.Command {
	for _, cmd := range tr.root.Commands() {
		if cmd.Name() == name {
			return cmd
		}
	}

	return nil
}

// names returns the names of modules.
func names(modules []extmod.Manifest) []string {
	result := make([]string, 0, len(modules))

	for _, module := range modules {
		result = append(result, module.Name)
	}

	return result
}

// writeModule writes the module name into dir: an executable main running
// script and, unless manifest is empty, a manifest.yaml holding it. It
// returns the path of main.
func writeModule(t *testing.T, dir, name, script, manifest string) string {
	t.Helper()

	moduleDir := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(moduleDir, 0o755))

	main := filepath.Join(moduleDir, "main")
	require.NoError(t, os.WriteFile(main, []byte("#!/bin/sh\n"+script+"\n"), 0o755))

	if manifest != "" {
		require.NoError(t, os.WriteFile(filepath.Join(moduleDir, "manifest.yaml"),
			[]byte(manifest), 0o644))
	}

	return main
}

// writeEcho writes the module name into dir, with a manifest, whose main
// prints its name and its arguments. It returns the path of main.
func writeEcho(t *testing.T, dir, name string) string {
	t.Helper()

	return writeModule(t, dir, name, `echo "`+name+`" "$@"`,
		"version: 1.0.0\nhelp: External "+name+"\nmain: main\n")
}

// legacy returns a legacy entry of the module "builtin" for cmd.
func legacy(cmd *cobra.Command) mount.Entry {
	return mount.Entry{Module: "builtin", Path: "", Cmd: cmd, Legacy: true}
}

// internal returns a runnable command named name, printing "internal
// <name>" and its arguments to its output when it runs.
func internal(name string) *cobra.Command {
	return &cobra.Command{
		Use: name,
		RunE: func(cmd *cobra.Command, args []string) error {
			cmd.Println("internal " + strings.Join(append([]string{name}, args...), " "))

			return nil
		},
	}
}

// group returns a group named name of subs.
func group(name string, subs ...*cobra.Command) *cobra.Command {
	cmd := &cobra.Command{Use: name}
	cmd.AddCommand(subs...)

	return cmd
}

// TestMountAddsModule checks that a module with no command of its name is
// added: it gets its arguments as they are, flags included, its help is the
// module's and the module's one-line help describes it.
func TestMountAddsModule(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	main := writeModule(t, dir, "hello", `if [ "$1" = --help ]; then echo "help of hello"; `+
		`else echo "hello $*"; fi`, "version: 1.0.0\nhelp: Say hello\nmain: main\n")

	tree := newTree(t, legacy(internal("other")))

	modules, err := tree.mount(dir)
	require.NoError(t, err)
	assert.Equal(t, []extmod.Manifest{{
		Name: "hello", Version: "1.0.0", Main: main, Help: "Say hello",
	}}, modules)
	assert.Empty(t, tree.log.Messages())

	proxy := tree.find("hello")
	require.NotNil(t, proxy)
	assert.Equal(t, "Say hello", proxy.Short, "the module's help describes its command")

	cmd, err := tree.run("hello", "a", "--b", "-c", "--help")
	require.NoError(t, err)
	assert.Same(t, proxy, cmd)
	assert.Equal(t, "hello a --b -c --help\n", tree.out.String())

	tree.out.Reset()
	require.NoError(t, proxy.Help())
	assert.Equal(t, "help of hello\n", tree.out.String())
	assert.Equal(t, []string{filepath.Join(dir, "hello", "manifest.yaml"), main, main},
		tree.openedFiles(),
		"the manifest, the run and the help read the files through the integrity checks")
}

// TestMountReplacesCommands checks that a module takes the place of the
// command of its name, subcommands and all, whoever built it, described by
// the module's one-line help, with no warning: a debug record names the
// command's module.
func TestMountReplacesCommands(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	for _, name := range []string{"env", "demo", "grp", "injected"} {
		writeEcho(t, dir, name)
	}

	tree := newTree(t,
		legacy(internal("env")),
		mount.Entry{Module: "demo", Path: "", Cmd: group("demo", internal("ok")), Legacy: false},
		mount.Entry{Module: "sub", Path: "grp", Cmd: internal("leaf"), Legacy: false},
	)
	// A command injected into the tree is in no module.
	tree.root.AddCommand(internal("injected"))

	modules, err := tree.mount(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"demo", "env", "grp", "injected"}, names(modules))

	records := tree.log.Records()
	require.Len(t, records, 4)

	for index, replaced := range []string{
		`command "demo" of module "demo"`,
		`command "env" of module "builtin"`,
		`command "grp" of module "sub"`,
		`command "injected"`,
	} {
		name := modules[index].Name
		assert.Equal(t, slog.LevelDebug, records[index].Level)
		assert.Equal(t, `External module "`+name+`" (`+filepath.Join(dir, name, "main")+
			`) replaces the `+replaced+`; run tt with -I to keep it`, records[index].Message)
	}

	for _, args := range [][]string{
		{"env", "--x"},
		{"demo", "ok", "--x", "y"},
		{"grp", "leaf"},
		{"injected"},
	} {
		tree.out.Reset()

		cmd, err := tree.run(args...)
		require.NoError(t, err)
		assert.Empty(t, cmd.Commands(), "the module takes the whole command")
		assert.Equal(t, "External "+args[0], cmd.Short)
		assert.Equal(t, strings.Join(args, " ")+"\n", tree.out.String())
	}
}

// TestMountTakesAliases checks that a module answers to the aliases of the
// command it replaces, and that -I keeps them the command's.
func TestMountTakesAliases(t *testing.T) {
	t.Parallel()

	for _, testCase := range []struct {
		name          string
		forceInternal bool
		want          string
	}{
		{name: "replaced", forceInternal: false, want: "replicaset status x\n"},
		{name: "kept with -I", forceInternal: true, want: "internal status x\n"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeEcho(t, dir, "replicaset")

			rs := group("replicaset", internal("status"))

			rs.Aliases = []string{"rs"}

			tree := newTree(t, legacy(rs))

			tree.forceInternal = testCase.forceInternal

			_, err := tree.mount(dir)
			require.NoError(t, err)

			_, err = tree.run("rs", "status", "x")
			require.NoError(t, err)
			assert.Equal(t, testCase.want, tree.out.String())
		})
	}
}

// TestMountForceInternal checks that -I keeps every command of tt, with no
// warning, and still adds the modules that have no command of their name.
func TestMountForceInternal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeEcho(t, dir, "env")
	writeEcho(t, dir, "demo")
	writeEcho(t, dir, "hello")

	env := internal("env")
	demo := group("demo", internal("ok"))

	tree := newTree(t, legacy(env),
		mount.Entry{Module: "demo", Path: "", Cmd: demo, Legacy: false})

	tree.forceInternal = true

	modules, err := tree.mount(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"demo", "env", "hello"}, names(modules),
		"the modules kept out by -I are still listed")
	assert.Empty(t, tree.log.Messages())
	assert.Same(t, env, tree.find("env"))
	assert.Same(t, demo, tree.find("demo"))

	_, err = tree.run("demo", "ok")
	require.NoError(t, err)
	assert.Equal(t, "internal ok\n", tree.out.String())

	tree.out.Reset()

	_, err = tree.run("hello", "-I")
	require.NoError(t, err)
	assert.Equal(t, "hello -I\n", tree.out.String())
}

// TestMountProtected checks that a module named like a command no module
// may replace is ignored, with a warning, without being read or run, and is
// listed nowhere.
func TestMountProtected(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	protected := []string{"__complete", "__completeNoDesc", "completion", "help", "modules",
		"version"}

	for _, name := range protected {
		// Without a manifest the module would be asked to describe itself.
		writeModule(t, dir, name, `touch "`+marker+`"; echo "version: 1.0.0"; echo "help: x"`,
			"")
	}

	writeEcho(t, dir, "hello")

	version := internal("version")
	tree := newTree(t, legacy(version), legacy(internal("completion")))

	modules, err := tree.mount(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"hello"}, names(modules))
	assert.Same(t, version, tree.find("version"))
	assert.NoFileExists(t, marker, "an ignored module is not run")
	assert.Equal(t, []string{filepath.Join(dir, "hello", "manifest.yaml")}, tree.openedFiles(),
		"an ignored module is not read")

	want := make([]string, 0, len(protected))
	for _, name := range protected {
		want = append(want, `External module "`+name+`" (`+filepath.Join(dir, name)+
			`) is ignored: tt does not let a module replace the command "`+name+`"`)
	}

	assert.Equal(t, want, tree.log.Messages())

	_, err = tree.run("modules", "list")
	require.NoError(t, err)
	assert.Equal(t, "hello - External hello\n", tree.out.String())
}

// TestMountRefused checks that a module the tree cannot hold is ignored,
// with a warning, and is listed nowhere.
func TestMountRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeEcho(t, dir, "rs")

	replicaset := group("replicaset", internal("status"))

	replicaset.Aliases = []string{"rs"}

	tree := newTree(t, legacy(replicaset))

	modules, err := tree.mount(dir)
	require.NoError(t, err)
	assert.Empty(t, modules)
	assert.Nil(t, tree.find("rs"))
	assert.Equal(t, []string{`External module "rs" (` + filepath.Join(dir, "rs", "main") +
		`) is ignored: command "replicaset" of module "builtin" and command "rs" of module ` +
		`"rs" both answer to "rs"`}, tree.log.Messages())

	_, err = tree.run("rs", "status")
	require.NoError(t, err)
	assert.Equal(t, "internal status\n", tree.out.String())
}

// TestMountIntegrity checks that what the integrity checks refuse, when
// they open a file or only once they have read it to the end, is neither
// read nor run: a module whose manifest they refuse is ignored, one whose
// executable they refuse fails to run and has no help, and one with no
// manifest is not asked to describe itself.
func TestMountIntegrity(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "ran")
	touch := `touch "` + marker + `"; echo "version: 1.0.0"; echo "help: x"`
	manifest := "version: 1.0.0\nhelp: x\nmain: main\n"

	badMain := writeModule(t, dir, "badmain", touch, manifest)
	lateMain := writeModule(t, dir, "latemain", touch, manifest)
	noManifest := writeModule(t, dir, "nomanifest", touch, "")
	lateNoManifest := writeModule(t, dir, "latenomanifest", touch, "")

	writeModule(t, dir, "badmanifest", touch, manifest)
	writeModule(t, dir, "latemanifest", touch, manifest)

	tree := newTree(t)

	tree.refused = []string{filepath.Join(dir, "badmanifest", "manifest.yaml"), badMain,
		noManifest}
	tree.refusedAtEnd = []string{filepath.Join(dir, "latemanifest", "manifest.yaml"),
		lateMain, lateNoManifest}

	modules, err := tree.mount(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"badmain", "latemain"}, names(modules))
	assert.Equal(t, []string{
		`Failed to get information about module "badmanifest": failed to read manifest: ` +
			errRefused.Error(),
		`Failed to get information about module "latemanifest": failed to read manifest: ` +
			errRefused.Error(),
		`Failed to get information about module "latenomanifest": integrity check failed ` +
			`for "` + lateNoManifest + `": ` + errRefused.Error(),
		`Failed to get information about module "nomanifest": integrity check failed for "` +
			noManifest + `": ` + errRefused.Error(),
	}, tree.log.Messages())

	for _, main := range []string{badMain, lateMain} {
		name := filepath.Base(filepath.Dir(main))

		_, err = tree.run(name, "x")
		require.ErrorIs(t, err, errRefused)
		require.EqualError(t, err, `integrity check failed for "`+main+`": `+errRefused.Error())

		tree.errOut.Reset()
		require.NoError(t, tree.find(name).Help())
		assert.Equal(t, `failed to get help for module "`+name+`": integrity check failed `+
			`for "`+main+`": `+errRefused.Error()+"\n", tree.errOut.String())
	}

	assert.NoFileExists(t, marker, "a refused executable is not run")
	assert.Empty(t, tree.out.String())
}

// TestMountHelpFirstLine checks that a module is described by the first line
// of its help, whether the manifest gives the help or the module does when
// asked with --description: its command and tt modules list show that line,
// and a help with no line at all is refused as a missing one.
func TestMountHelpFirstLine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeModule(t, dir, "folded", `echo "folded $*"`,
		"version: 1.0.0\nmain: main\nhelp: |\n\n  First line of the help.  \n  Second line.\n")
	writeModule(t, dir, "asked", `echo "version: 1.0.0"; echo "help: |"; `+
		`echo "  Asked first line."; echo "  Asked second line."`, "")
	writeModule(t, dir, "blank", `echo "blank $*"`,
		"version: 1.0.0\nmain: main\nhelp: \"  \"\n")

	tree := newTree(t)

	modules, err := tree.mount(dir)
	require.NoError(t, err)
	assert.Equal(t, []string{"asked", "folded"}, names(modules))
	assert.Equal(t, []string{
		`Failed to get information about module "blank": ` +
			`help field is mandatory for module Manifest`,
	}, tree.log.Messages())

	for name, want := range map[string]string{
		"asked":  "Asked first line.",
		"folded": "First line of the help.",
	} {
		assert.Equal(t, want, tree.find(name).Short, name)
	}

	_, err = tree.run("modules", "list")
	require.NoError(t, err)
	assert.Equal(t, "asked - Asked first line.\nfolded - First line of the help.\n",
		tree.out.String())
}

// TestMountModulesList checks what tt modules list prints.
func TestMountModulesList(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	first := writeEcho(t, dir, "b")
	second := writeEcho(t, dir, "a")

	// A file among the modules is not one.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "file"), nil, 0o644))

	for _, testCase := range []struct {
		args []string
		want string
	}{
		{nil, "a - External a\nb - External b\n"},
		{[]string{"--version"}, "1.0.0\ta - External a\n1.0.0\tb - External b\n"},
		{[]string{"-p"}, "a - " + second + "\nb - " + first + "\n"},
		{
			[]string{"-v", "--path"},
			"1.0.0\ta - " + second + "\n1.0.0\tb - " + first + "\n",
		},
	} {
		tree := newTree(t)

		_, err := tree.mount(dir)
		require.NoError(t, err)

		_, err = tree.run(append([]string{"modules", "list"}, testCase.args...)...)
		require.NoError(t, err)
		assert.Equal(t, testCase.want, tree.out.String(), testCase.args)
	}
}

// TestMountModulesCommandTaken checks that Mount fails when the tree cannot
// hold tt modules.
func TestMountModulesCommandTaken(t *testing.T) {
	t.Parallel()

	tree := newTree(t, mount.Entry{Module: "m", Path: "", Cmd: internal("modules")})

	_, err := tree.mount("")
	require.ErrorContains(t, err, `adding tt modules: duplicate command "modules": `+
		`modules "m" and "tt"`)
}
