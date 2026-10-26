package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

// lookupTopology is the cluster config of the applications of the tests of
// --scope and --template: storage-1, storage-2 and router-1.
const lookupTopology = "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n" +
	"          storage-1: {}\n          storage-2: {}\n          router-1: {}\n"

// writeFiles writes files, relative paths to contents, under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		path := filepath.Join(dir, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
}

// lookupFixture installs the application myapp into a new user root, with
// the default template of storage-1 and storage-2, and lays out the project
// myapp, the -C directory and the working directory, with two templates:
// default of storage-1 and other of router-1. The system scope is empty. It
// returns the version directory of the user application and the project
// directory.
func lookupFixture(t *testing.T) (string, string) {
	t.Helper()

	user := t.TempDir()
	t.Setenv("TT_USER_ROOT", user)
	t.Setenv("TT_SYSTEM_ROOT", t.TempDir())

	version := filepath.Join(user, "versions", "myapp", "myapp_1.0.0")
	writeFiles(t, version, map[string]string{
		".templates/default/instances.yaml": "storage-1:\nstorage-2:\n",
		".templates/default/config.yaml":    lookupTopology,
	})
	require.NoError(t, os.MkdirAll(filepath.Join(user, "apps"), 0o755))
	require.NoError(t, os.Symlink(version, filepath.Join(user, "apps", "myapp")))

	project := filepath.Join(t.TempDir(), "myapp")
	writeFiles(t, project, map[string]string{
		".templates/default/instances.yaml": "storage-1:\n",
		".templates/default/config.yaml":    lookupTopology,
		".templates/other/instances.yaml":   "router-1:\n",
		".templates/other/config.yaml":      lookupTopology,
	})

	withProjectDir(t, project)
	t.Chdir(project)

	t.Cleanup(func() { instanceFlags.Scope, instanceFlags.Template = "", "" })

	version, err := filepath.EvalSymlinks(version)
	require.NoError(t, err)

	return version, project
}

// findCommand returns the command path names under root, its flags parsed
// from args.
func findCommand(t *testing.T, root *cobra.Command, path []string, args ...string) *cobra.Command {
	t.Helper()

	found, _, err := root.Find(path)
	require.NoError(t, err)
	require.NoError(t, found.ParseFlags(args))

	return found
}

// TestAppCommandsTakeLookupFlags pins that every command that takes an
// application, beside those of tt instance, takes --scope and --template as
// they do: the subcommands of tt replicaset, tt cluster show and publish,
// and tt play.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestAppCommandsTakeLookupFlags(t *testing.T) {
	replicaset := NewReplicasetCmd()
	cluster := NewClusterCmd()

	commands := map[string]*cobra.Command{"play": NewPlayCmd()}

	for _, path := range [][]string{
		{"upgrade"}, {"downgrade"}, {"status"}, {"promote"}, {"demote"}, {"expel"},
		{"bootstrap"}, {"vshard", "bootstrap"}, {"rebootstrap"}, {"roles", "add"},
		{"roles", "remove"},
	} {
		commands["replicaset "+filepath.Join(path...)] = findCommand(t, replicaset, path)
	}

	for _, name := range []string{"show", "publish"} {
		commands["cluster "+name] = findCommand(t, cluster, []string{name})
	}

	instanceStatus := findCommand(t, NewInstanceCmd(), []string{"status"})

	for name, command := range commands {
		for _, flagName := range []string{"scope", "template"} {
			flag := command.Flags().Lookup(flagName)
			require.NotNil(t, flag, "%s --%s", name, flagName)

			want := instanceStatus.Flags().Lookup(flagName)
			assert.Equal(t, want.Usage, flag.Usage, "%s --%s", name, flagName)
			assert.Empty(t, flag.Shorthand, "%s --%s", name, flagName)
		}
	}
}

// TestLookupFlagsMatchResolver pins that --scope and --template, which the
// SDK declares, say what the resolver takes: the scopes the completion
// offers are those app.ParseScope accepts, and the template a command runs
// from without --template is the one the help names.
//
//nolint:paralleltest // The constructors bind flags to package state.
func TestLookupFlagsMatchResolver(t *testing.T) {
	status := findCommand(t, NewInstanceCmd(), []string{"status"})

	complete, ok := status.GetFlagCompletionFunc(running.ScopeFlag)
	require.True(t, ok)

	got, _ := complete(status, nil, "")
	assert.Equal(t, []string{
		app.ScopeProject.String(), app.ScopeUser.String(), app.ScopeSystem.String(),
	}, got)

	template := status.Flags().Lookup(running.TemplateFlag)
	require.NotNil(t, template)
	assert.Contains(t, template.Usage, `(default "`+app.DefaultTemplate+`")`)
}

// TestReplicasetLookup pins that tt replicaset finds the application of its
// target as --scope and --template say: myapp of the user scope, which the
// project of the same name hides without the flag.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestReplicasetLookup(t *testing.T) {
	version, project := lookupFixture(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	findCommand(t, NewReplicasetCmd(), []string{"status"}, "--scope", "user")

	var ctx replicasetCtx

	require.NoError(t, replicasetFillCtx(cmdCtx, &ctx, "myapp", false, skipConfig))
	assert.Equal(t, []string{"storage-1", "storage-2"}, instanceNames(ctx.RunningCtx.Instances))
	assert.Equal(t, version, ctx.RunningCtx.Instances[0].AppDir)

	findCommand(t, NewReplicasetCmd(), []string{"status"}, "--template", "other")

	application, insts, _, err := resolveAppTarget(cmdCtx, "myapp", readConfig)
	require.NoError(t, err)
	assert.Equal(t, project, application.Dir)
	assert.Equal(t, []string{"router-1"}, instanceNames(insts))

	findCommand(t, NewReplicasetCmd(), []string{"status"}, "--scope", "global")

	_, insts, _, err = resolveAppTarget(cmdCtx, "myapp", skipConfig)
	require.ErrorIs(t, err, app.ErrUnknownScope)
	assert.Empty(t, insts)
}

// TestClusterLookup pins that tt cluster show and publish read the config of
// the pair --scope and --template select, and that the completion of their
// target offers the applications of the scope --scope names.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestClusterLookup(t *testing.T) {
	version, project := lookupFixture(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	show := findCommand(t, NewClusterCmd(), []string{"show"}, "--template", "other")

	path, instName, err := parseAppStr(cmdCtx, "myapp:router-1")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(project, ".templates", "other", "config.yaml"), path)
	assert.Equal(t, "router-1", instName)

	got, _ := internal.ClusterValidArgsFunction(cmdCtx, show, "myapp:", internal.Any)
	assert.Equal(t, []string{"myapp:router-1"}, got)

	show = findCommand(t, NewClusterCmd(), []string{"show"}, "--scope", "user")

	path, _, err = parseAppStr(cmdCtx, "myapp")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(version, ".templates", "default", "config.yaml"), path)

	got, _ = internal.ClusterValidArgsFunction(cmdCtx, show, "myapp:", internal.Any)
	assert.Equal(t, []string{"myapp:storage-1", "myapp:storage-2"}, got)

	show = findCommand(t, NewClusterCmd(), []string{"show"}, "--scope", "system")

	got, _ = internal.ClusterValidArgsFunction(cmdCtx, show, "", internal.Any)
	assert.Empty(t, got, "the system scope holds nothing")

	findCommand(t, NewClusterCmd(), []string{"publish"}, "--scope", "user")

	path, _, err = parseAppStr(cmdCtx, "myapp:storage-2")
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(version, ".templates", "default", "config.yaml"), path)
}

// TestPlayLookup pins that tt play finds the instance of its target as
// --scope and --template say.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestPlayLookup(t *testing.T) {
	version, project := lookupFixture(t)

	cmdCtx := &cmdcontext.CmdCtx{}

	require.NoError(t, NewPlayCmd().ParseFlags([]string{"--scope", "user"}))

	insts, err := resolveInstances(cmdCtx, []string{"myapp:storage-2"})
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.Equal(t, version, insts[0].AppDir)

	require.NoError(t, NewPlayCmd().ParseFlags([]string{"--template", "other"}))

	insts, err = resolveInstances(cmdCtx, []string{"myapp:router-1"})
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.Equal(t, project, insts[0].AppDir)
}
