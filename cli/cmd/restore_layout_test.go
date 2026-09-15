package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/restore"
)

// noConfigLoader stands in wherever a call must not reach a cluster
// configuration at all.
func noConfigLoader(t *testing.T) configDirsLoader {
	t.Helper()

	return func(configPath, instance string, _ restore.Layout) (restore.ConfigDirs, error) {
		t.Fatalf("the cluster config %q was read for instance %q", configPath, instance)

		return restore.ConfigDirs{}, nil
	}
}

// configuredDirs answers with one fixed set of directories, so that a test of
// the precedence between the flags does not need a configuration file.
func configuredDirs(dirs restore.ConfigDirs) configDirsLoader {
	return func(string, string, restore.Layout) (restore.ConfigDirs, error) {
		return dirs, nil
	}
}

// The six flags a layout can be described with, and the value each carries
// when it is given. Absolute, so that resolving an accepted layout leaves it
// as the table wrote it and the expectation stays readable.
const (
	workDirFlag     = "/flag/work"
	snapshotDirFlag = "/flag/memtx"
	walDirFlag      = "/flag/wal"
	vinylDirFlag    = "/flag/vinyl"
	configFlag      = "cluster.yaml"
	instanceFlag    = "i1"
)

// restoreLayoutCase is one combination of the six flags, with the label naming
// which of them are given.
type restoreLayoutCase struct {
	label string
	flags restoreLayoutFlags
}

// restoreLayoutCombinations enumerates every subset of the six flags.
func restoreLayoutCombinations() []restoreLayoutCase {
	const flagCount = 6

	cases := make([]restoreLayoutCase, 0, 1<<flagCount)

	for mask := 0; mask < 1<<flagCount; mask++ {
		var (
			flags restoreLayoutFlags
			given []string
		)

		for bit, flag := range []struct {
			name  string
			value string
			dst   *string
		}{
			{name: "--work-dir", value: workDirFlag, dst: &flags.WorkDir},
			{name: "--snapshot-dir", value: snapshotDirFlag, dst: &flags.SnapshotDir},
			{name: "--wal-dir", value: walDirFlag, dst: &flags.WALDir},
			{name: "--vinyl-dir", value: vinylDirFlag, dst: &flags.VinylDir},
			{name: "--config", value: configFlag, dst: &flags.Config},
			{name: "--instance", value: instanceFlag, dst: &flags.Instance},
		} {
			if mask&(1<<bit) == 0 {
				continue
			}

			*flag.dst = flag.value
			given = append(given, flag.name)
		}

		label := "nothing at all"
		if len(given) > 0 {
			label = strings.Join(given, " ")
		}

		cases = append(cases, restoreLayoutCase{label: label, flags: flags})
	}

	return cases
}

// configVariant is a cluster configuration and what its three keys resolve to
// for a given launch directory. An empty field of the resolved layout is a key
// that cannot be resolved at all.
type configVariant struct {
	name    string
	dirs    restore.ConfigDirs
	resolve func(workDir string) restore.Layout
}

// expectRestoreLayout states, as a rule rather than as a table of answers,
// what resolveRestoreLayout has to make of one combination:
//
//   - --config and --instance are read together or not at all;
//   - a directory named by its own flag is that flag's value;
//   - otherwise a configuration and an instance decide it, and a key of that
//     configuration which cannot be resolved refuses the whole call;
//   - otherwise --work-dir holds every kind of file;
//   - otherwise the call does not add up and is refused, naming the directory
//     that is missing.
//
// A non-empty third return value is the diagnostic a refusal has to carry.
func expectRestoreLayout(flags restoreLayoutFlags, variant configVariant) (
	restore.Layout, restoreLayoutSources, string,
) {
	switch {
	case flags.Config != "" && flags.Instance == "":
		return restore.Layout{}, restoreLayoutSources{}, "--config needs --instance"
	case flags.Instance != "" && flags.Config == "":
		return restore.Layout{}, restoreLayoutSources{}, "--instance needs --config"
	}

	configured := restore.Layout{}
	if flags.Config != "" {
		configured = variant.resolve(flags.WorkDir)
	}

	var (
		layout  restore.Layout
		sources restoreLayoutSources
	)

	for _, dir := range []struct {
		kind       string
		key        string
		flagName   string
		flag       string
		configured string
		value      *string
		source     *string
	}{
		{
			kind:       "snapshot",
			key:        "snapshot.dir",
			flagName:   "--snapshot-dir",
			flag:       flags.SnapshotDir,
			configured: configured.Snapshot,
			value:      &layout.Snapshot,
			source:     &sources.Snapshot,
		},
		{
			kind:       "wal",
			key:        "wal.dir",
			flagName:   "--wal-dir",
			flag:       flags.WALDir,
			configured: configured.WAL,
			value:      &layout.WAL,
			source:     &sources.WAL,
		},
		{
			kind:       "vinyl",
			key:        "vinyl.dir",
			flagName:   "--vinyl-dir",
			flag:       flags.VinylDir,
			configured: configured.Vinyl,
			value:      &layout.Vinyl,
			source:     &sources.Vinyl,
		},
	} {
		switch {
		case dir.flag != "":
			*dir.value, *dir.source = dir.flag, dir.flagName
		case flags.Config != "" && dir.configured != "":
			*dir.value = dir.configured
			*dir.source = fmt.Sprintf("%s, instance %s", flags.Config, flags.Instance)
		case flags.Config != "":
			return restore.Layout{}, restoreLayoutSources{}, dir.key + " is"
		case flags.WorkDir != "":
			*dir.value, *dir.source = flags.WorkDir, "--work-dir"
		default:
			return restore.Layout{}, restoreLayoutSources{},
				"no " + dir.kind + " directory given"
		}
	}

	return layout, sources, ""
}

// Every combination of the six flags, against a configuration whose three
// directories are absolute and against one whose directories and
// process.work_dir are relative. The flags are few enough to enumerate
// exhaustively, and the interesting cases are the ones nobody thinks to write
// down: a configuration rescued by three overrides, an override that rescues
// nothing because another key is still unresolvable, --work-dir sitting unused
// beside three directory flags.
func TestResolveRestoreLayout_EveryFlagCombination(t *testing.T) {
	variants := []configVariant{
		{
			name: "a configuration whose directories are absolute",
			dirs: restore.ConfigDirs{
				Snapshot: "/cfg/memtx",
				WAL:      "/cfg/wal",
				Vinyl:    "/cfg/vinyl",
			},
			resolve: func(string) restore.Layout {
				return restore.Layout{
					Snapshot: "/cfg/memtx",
					WAL:      "/cfg/wal",
					Vinyl:    "/cfg/vinyl",
				}
			},
		},
		{
			name: "a configuration whose directories are relative",
			dirs: restore.ConfigDirs{
				Snapshot:       "memtx/{{ instance_name }}",
				WAL:            "wal/{{ instance_name }}",
				Vinyl:          "vinyl/{{ instance_name }}",
				ProcessWorkDir: "base",
			},
			resolve: func(workDir string) restore.Layout {
				// Nothing resolves a relative process.work_dir without a
				// launch directory, and so nothing resolves the directories
				// under it either.
				if workDir == "" {
					return restore.Layout{}
				}

				return restore.Layout{
					Snapshot: filepath.Join(workDir, "base", "memtx", instanceFlag),
					WAL:      filepath.Join(workDir, "base", "wal", instanceFlag),
					Vinyl:    filepath.Join(workDir, "base", "vinyl", instanceFlag),
				}
			},
		},
	}

	cases := restoreLayoutCombinations()
	require.Len(t, cases, 64, "every subset of the six flags has to be covered")

	for _, variant := range variants {
		t.Run(variant.name, func(t *testing.T) {
			for _, tc := range cases {
				t.Run(tc.label, func(t *testing.T) {
					want, wantSources, reported := expectRestoreLayout(tc.flags, variant)

					layout, sources, err := resolveRestoreLayout(
						tc.flags, configuredDirs(variant.dirs))

					if reported != "" {
						require.ErrorIs(t, err, restore.ErrValidation)
						assert.ErrorContains(t, err, reported)

						return
					}

					require.NoError(t, err)
					assert.Equal(t, want, layout)
					assert.Equal(t, wantSources, sources)
				})
			}
		})
	}
}

// A directory named by its own flag is used as it stands: taken against the
// directory tt is running in like every other path on the command line, never
// against --work-dir, and with no {{ instance_name }} substituted into it --
// the caller who typed it knows which instance this run is for.
func TestResolveRestoreLayout_RelativeDirFlags(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	layout, sources, err := resolveRestoreLayout(restoreLayoutFlags{
		WorkDir:     "/data",
		SnapshotDir: "memtx",
		WALDir:      "./wal",
		VinylDir:    "vinyl/",
	}, noConfigLoader(t))
	require.NoError(t, err)

	assert.Equal(t, restore.Layout{
		Snapshot: filepath.Join(cwd, "memtx"),
		WAL:      filepath.Join(cwd, "wal"),
		Vinyl:    filepath.Join(cwd, "vinyl"),
	}, layout)

	assert.Equal(t, restoreLayoutSources{
		Snapshot: "--snapshot-dir",
		WAL:      "--wal-dir",
		Vinyl:    "--vinyl-dir",
	}, sources)
}

// A relative --work-dir is the launch directory of the instance, and a launch
// directory is where it is relative to the directory tt runs in.
func TestResolveRestoreLayout_RelativeWorkDirInConfigMode(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	layout, _, err := resolveRestoreLayout(restoreLayoutFlags{
		Config:   "cluster.yaml",
		Instance: "i1",
		WorkDir:  "launch",
	}, configuredDirs(restore.ConfigDirs{WAL: "wal/{{ instance_name }}"}))
	require.NoError(t, err)

	assert.Equal(t, restore.Layout{
		Snapshot: filepath.Join(cwd, "launch", "var", "lib", "i1"),
		WAL:      filepath.Join(cwd, "launch", "wal", "i1"),
		Vinyl:    filepath.Join(cwd, "launch", "var", "lib", "i1"),
	}, layout)
}

func TestResolveRestoreLayout(t *testing.T) {
	configured := restore.ConfigDirs{
		Snapshot: "/srv/memtx",
		WAL:      "/srv/wal",
		Vinyl:    "/srv/vinyl",
	}

	tests := []struct {
		name    string
		flags   restoreLayoutFlags
		load    configDirsLoader
		want    restore.Layout
		sources restoreLayoutSources
	}{
		{
			name:  "the work directory holds every kind of file",
			flags: restoreLayoutFlags{WorkDir: "/data"},
			want:  restore.Layout{Snapshot: "/data", WAL: "/data", Vinyl: "/data"},
			sources: restoreLayoutSources{
				Snapshot: "--work-dir",
				WAL:      "--work-dir",
				Vinyl:    "--work-dir",
			},
		},
		{
			name:  "one snapshot directory beside the work directory",
			flags: restoreLayoutFlags{WorkDir: "/data", SnapshotDir: "/memtx"},
			want:  restore.Layout{Snapshot: "/memtx", WAL: "/data", Vinyl: "/data"},
			sources: restoreLayoutSources{
				Snapshot: "--snapshot-dir",
				WAL:      "--work-dir",
				Vinyl:    "--work-dir",
			},
		},
		{
			name:  "one wal directory beside the work directory",
			flags: restoreLayoutFlags{WorkDir: "/data", WALDir: "/wal"},
			want:  restore.Layout{Snapshot: "/data", WAL: "/wal", Vinyl: "/data"},
			sources: restoreLayoutSources{
				Snapshot: "--work-dir",
				WAL:      "--wal-dir",
				Vinyl:    "--work-dir",
			},
		},
		{
			name:  "one vinyl directory beside the work directory",
			flags: restoreLayoutFlags{WorkDir: "/data", VinylDir: "/vinyl"},
			want:  restore.Layout{Snapshot: "/data", WAL: "/data", Vinyl: "/vinyl"},
			sources: restoreLayoutSources{
				Snapshot: "--work-dir",
				WAL:      "--work-dir",
				Vinyl:    "--vinyl-dir",
			},
		},
		{
			name: "all three named leave the work directory unused",
			flags: restoreLayoutFlags{
				WorkDir:     "/data",
				SnapshotDir: "/memtx",
				WALDir:      "/wal",
				VinylDir:    "/vinyl",
			},
			want: restore.Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
			sources: restoreLayoutSources{
				Snapshot: "--snapshot-dir",
				WAL:      "--wal-dir",
				Vinyl:    "--vinyl-dir",
			},
		},
		{
			name: "all three named need no work directory",
			flags: restoreLayoutFlags{
				SnapshotDir: "/memtx",
				WALDir:      "/wal",
				VinylDir:    "/vinyl",
			},
			want: restore.Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
			sources: restoreLayoutSources{
				Snapshot: "--snapshot-dir",
				WAL:      "--wal-dir",
				Vinyl:    "--vinyl-dir",
			},
		},
		{
			name:  "the configuration names all three",
			flags: restoreLayoutFlags{Config: "cluster.yaml", Instance: "i1"},
			load:  configuredDirs(configured),
			want:  restore.Layout{Snapshot: "/srv/memtx", WAL: "/srv/wal", Vinyl: "/srv/vinyl"},
			sources: restoreLayoutSources{
				Snapshot: "cluster.yaml, instance i1",
				WAL:      "cluster.yaml, instance i1",
				Vinyl:    "cluster.yaml, instance i1",
			},
		},
		{
			name: "a directory flag overrides the configuration for that one only",
			flags: restoreLayoutFlags{
				Config:   "cluster.yaml",
				Instance: "i1",
				WALDir:   "/ssd/wal",
			},
			load: configuredDirs(configured),
			want: restore.Layout{Snapshot: "/srv/memtx", WAL: "/ssd/wal", Vinyl: "/srv/vinyl"},
			sources: restoreLayoutSources{
				Snapshot: "cluster.yaml, instance i1",
				WAL:      "--wal-dir",
				Vinyl:    "cluster.yaml, instance i1",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			load := tc.load
			if load == nil {
				load = noConfigLoader(t)
			}

			layout, sources, err := resolveRestoreLayout(tc.flags, load)
			require.NoError(t, err)

			assert.Equal(t, tc.want, layout)
			assert.Equal(t, tc.sources, sources)
		})
	}
}

// A configuration key a flag replaces is never read, so it never has to
// resolve. Refusing a call over a value that would not have been used refuses
// a call that names every directory it needs.
func TestResolveRestoreLayout_AnOverrideRescuesItsOwnKeyOnly(t *testing.T) {
	mixed := restore.ConfigDirs{
		Snapshot: "/srv/memtx",
		WAL:      "wal/{{ instance_name }}",
		Vinyl:    "/srv/vinyl",
	}

	t.Run("the relative key is the one that was named", func(t *testing.T) {
		layout, sources, err := resolveRestoreLayout(restoreLayoutFlags{
			Config:   "cluster.yaml",
			Instance: "i1",
			WALDir:   "/ssd/wal",
		}, configuredDirs(mixed))
		require.NoError(t, err)

		assert.Equal(t, restore.Layout{
			Snapshot: "/srv/memtx",
			WAL:      "/ssd/wal",
			Vinyl:    "/srv/vinyl",
		}, layout)
		assert.Equal(t, "--wal-dir", sources.WAL)
	})

	t.Run("the relative key is left to the configuration", func(t *testing.T) {
		_, _, err := resolveRestoreLayout(restoreLayoutFlags{
			Config:   "cluster.yaml",
			Instance: "i1",
		}, configuredDirs(mixed))
		require.ErrorIs(t, err, restore.ErrValidation)
		assert.ErrorContains(t, err, `wal.dir is "wal/i1"`)
	})
}

// A layout that cannot be completed is refused, because a restore that filled
// in a directory of its own accord would put a whole instance's data somewhere
// nobody named -- and the instance would come up on whichever part of it the
// configured directories happen to hold, looking healthy.
func TestResolveRestoreLayout_RejectsAnIncompleteLayout(t *testing.T) {
	tests := []struct {
		name     string
		flags    restoreLayoutFlags
		load     configDirsLoader
		reported string
	}{
		{
			name:     "nothing at all",
			reported: "no snapshot directory given",
		},
		{
			name: "two of the three directories",
			flags: restoreLayoutFlags{
				SnapshotDir: "/memtx",
				WALDir:      "/wal",
			},
			reported: "no vinyl directory given: pass --vinyl-dir",
		},
		{
			name:     "a configuration with nobody to read it for",
			flags:    restoreLayoutFlags{Config: "cluster.yaml"},
			reported: "--config needs --instance",
		},
		{
			name:     "an instance with no configuration to read it from",
			flags:    restoreLayoutFlags{Instance: "i1", WorkDir: "/data"},
			reported: "--instance needs --config",
		},
		{
			name:  "an instance the configuration does not declare",
			flags: restoreLayoutFlags{Config: "cluster.yaml", Instance: "absent"},
			load: func(string, string, restore.Layout) (restore.ConfigDirs, error) {
				return restore.ConfigDirs{}, errors.New(
					"restore: invalid input: the cluster config declares no instance")
			},
			reported: "declares no instance",
		},
		{
			name:  "a configuration whose directories stay relative",
			flags: restoreLayoutFlags{Config: "cluster.yaml", Instance: "i1"},
			load:  configuredDirs(restore.ConfigDirs{WAL: "wal"}),
			reported: "snapshot.dir is \"var/lib/i1\", which is relative to " +
				"--work-dir",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			load := tc.load
			if load == nil {
				load = noConfigLoader(t)
			}

			_, _, err := resolveRestoreLayout(tc.flags, load)
			require.Error(t, err)
			assert.ErrorContains(t, err, tc.reported)
		})
	}
}

// writeClusterConfig writes a cluster configuration and returns its path.
func writeClusterConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))

	return path
}

// The directories are read off the instantiated configuration, so a key set
// for the whole cluster, the group or the replicaset reaches the instance that
// inherits it -- which is how a deployment states one layout for all of its
// instances and lets {{ instance_name }} tell them apart.
func TestClusterConfigDirs_ReadsInheritedKeys(t *testing.T) {
	path := writeClusterConfig(t, `process:
  work_dir: base
snapshot:
  dir: var/lib/{{ instance_name }}
groups:
  storages:
    wal:
      dir: wal/{{ instance_name }}
    replicasets:
      storage-001:
        vinyl:
          dir: /srv/vinyl
        instances:
          storage-001-a: {}
          storage-001-b:
            wal:
              dir: /ssd/wal-b
`)

	tests := []struct {
		name     string
		instance string
		want     restore.ConfigDirs
	}{
		{
			name:     "every key comes from an outer scope",
			instance: "storage-001-a",
			want: restore.ConfigDirs{
				Snapshot:       "var/lib/{{ instance_name }}",
				WAL:            "wal/{{ instance_name }}",
				Vinyl:          "/srv/vinyl",
				ProcessWorkDir: "base",
			},
		},
		{
			name:     "the instance's own key wins over the group's",
			instance: "storage-001-b",
			want: restore.ConfigDirs{
				Snapshot:       "var/lib/{{ instance_name }}",
				WAL:            "/ssd/wal-b",
				Vinyl:          "/srv/vinyl",
				ProcessWorkDir: "base",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dirs, err := clusterConfigDirs(path, tc.instance, restore.Layout{})
			require.NoError(t, err)
			assert.Equal(t, tc.want, dirs)
		})
	}
}

// An instance the configuration does not declare is refused rather than served
// the cluster-wide defaults: the caller named a node, and restoring another
// node's directories is not an approximation of that.
func TestClusterConfigDirs_RejectsAnUndeclaredInstance(t *testing.T) {
	path := writeClusterConfig(t, `groups:
  storages:
    replicasets:
      storage-001:
        instances:
          storage-001-a: {}
`)

	_, err := clusterConfigDirs(path, "storage-002-a", restore.Layout{})
	require.ErrorIs(t, err, restore.ErrValidation)
	assert.ErrorContains(t, err, `declares no instance "storage-002-a"`)
}

// The whole path from a file on disk to three directories, resolved the way
// Tarantool resolves them.
func TestResolveRestoreLayout_FromAConfigFile(t *testing.T) {
	path := writeClusterConfig(t, `process:
  work_dir: base
wal:
  dir: wal/{{ instance_name }}
groups:
  storages:
    replicasets:
      storage-001:
        instances:
          storage-001-a: {}
`)

	layout, sources, err := resolveRestoreLayout(restoreLayoutFlags{
		Config:   path,
		Instance: "storage-001-a",
		WorkDir:  "/opt/tarantool",
	}, clusterConfigDirs)
	require.NoError(t, err)

	assert.Equal(t, restore.Layout{
		// snapshot.dir and vinyl.dir are unset, so both take Tarantool's
		// default, under process.work_dir, under the launch directory.
		Snapshot: "/opt/tarantool/base/var/lib/storage-001-a",
		WAL:      "/opt/tarantool/base/wal/storage-001-a",
		Vinyl:    "/opt/tarantool/base/var/lib/storage-001-a",
	}, layout)

	assert.Equal(t, path+", instance storage-001-a", sources.WAL)
}

// A configuration holding something other than a path where one of the three
// keys belongs is refused rather than served Tarantool's default: the
// configuration does say where the data goes, it says something that cannot be
// read, and filling in the default over it would put an instance's data
// somewhere the instance does not read it from.
func TestClusterConfigDirs_RejectsAKeyThatIsNotAPath(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		reported string
	}{
		{
			name: "a scalar where the section belongs",
			body: `wal: /srv/wal
groups:
  storages:
    replicasets:
      storage-001:
        instances:
          storage-001-a: {}
`,
			reported: "wal.dir of instance",
		},
		{
			name: "a number where the directory belongs",
			body: `snapshot:
  dir: 42
groups:
  storages:
    replicasets:
      storage-001:
        instances:
          storage-001-a: {}
`,
			reported: "snapshot.dir of instance",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := clusterConfigDirs(
				writeClusterConfig(t, tc.body), "storage-001-a", restore.Layout{})
			require.ErrorIs(t, err, restore.ErrValidation)
			assert.ErrorContains(t, err, tc.reported)
		})
	}
}

// A configuration file the caller named and that cannot be read is a rejected
// input: the call is what has to change, and reporting it as a failure would
// have an orchestrator retry the same call instead.
func TestClusterConfigDirs_RejectsAnUnreadableFile(t *testing.T) {
	tests := []struct {
		name string
		path func(t *testing.T) string
	}{
		{
			name: "a file that is not there",
			path: func(t *testing.T) string {
				t.Helper()

				return filepath.Join(t.TempDir(), "absent.yaml")
			},
		},
		{
			name: "a file that does not parse",
			path: func(t *testing.T) string {
				t.Helper()

				return writeClusterConfig(t, "groups: [this is not a mapping\n")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := clusterConfigDirs(tc.path(t), "storage-001-a", restore.Layout{})
			require.ErrorIs(t, err, restore.ErrValidation)
			assert.ErrorContains(t, err, "failed to load the cluster config")
		})
	}
}

// The configuration may live in etcd or a Tarantool config storage rather than
// in a file, and --config takes the same URI 'tt restore plan -c' does: a
// source that parses as one is dialed, not opened as a file name. A storage
// that does not answer is an operational failure of something else, not an
// input the caller can correct, so it is not a rejection.
func TestClusterConfigDirs_TakesAUri(t *testing.T) {
	_, err := clusterConfigDirs(
		"http://127.0.0.1:1/prefix?timeout=0.1", "storage-001-a", restore.Layout{})
	require.Error(t, err)
	assert.ErrorContains(t, err, "cluster config storage")
	assert.NotErrorIs(t, err, restore.ErrValidation)
}

// A key a flag replaces is not read out of the configuration at all, so
// whatever the configuration holds there -- something that is not a path, a
// path carrying a variable nothing can substitute -- is beside the point. The
// caller answered that question on the command line, and a call that names
// every directory it needs is complete.
func TestResolveRestoreLayout_DoesNotReadAnOverriddenKey(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		flags restoreLayoutFlags
		want  restore.Layout
	}{
		{
			name: "a key that is not a path at all",
			body: `snapshot:
  dir: /srv/memtx
wal:
  dir: 42
vinyl:
  dir: /srv/vinyl
`,
			flags: restoreLayoutFlags{WALDir: "/wal"},
			want:  restore.Layout{Snapshot: "/srv/memtx", WAL: "/wal", Vinyl: "/srv/vinyl"},
		},
		{
			name: "a process.work_dir nothing is taken against any more",
			body: `process:
  work_dir: /srv/{{ deployment_id }}
snapshot:
  dir: memtx
wal:
  dir: wal
vinyl:
  dir: vinyl
`,
			flags: restoreLayoutFlags{
				SnapshotDir: "/memtx",
				WALDir:      "/wal",
				VinylDir:    "/vinyl",
			},
			want: restore.Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			flags := tc.flags
			flags.Config = writeClusterConfig(t, tc.body+`groups:
  storages:
    replicasets:
      storage-001:
        instances:
          storage-001-a: {}
`)
			flags.Instance = "storage-001-a"

			layout, _, err := resolveRestoreLayout(flags, clusterConfigDirs)
			require.NoError(t, err)
			assert.Equal(t, tc.want, layout)
		})
	}
}

// A configuration value a flag does not replace is read as it always is: a
// process.work_dir that cannot be rendered refuses the call as soon as one
// directory is still taken against it, and says which variable it could not
// substitute.
func TestResolveRestoreLayout_ReadsAKeyNoFlagReplaces(t *testing.T) {
	path := writeClusterConfig(t, `process:
  work_dir: /srv/{{ deployment_id }}
snapshot:
  dir: memtx
wal:
  dir: wal
vinyl:
  dir: vinyl
groups:
  storages:
    replicasets:
      storage-001:
        instances:
          storage-001-a: {}
`)

	_, _, err := resolveRestoreLayout(restoreLayoutFlags{
		Config:      path,
		Instance:    "storage-001-a",
		SnapshotDir: "/memtx",
		WALDir:      "/wal",
	}, clusterConfigDirs)
	require.ErrorIs(t, err, restore.ErrValidation)
	assert.ErrorContains(t, err, "missing vars: deployment_id")
}

// Without --work-dir there is nothing to resolve a relative configuration
// against, and the reject names the key that could not be resolved.
func TestResolveRestoreLayout_FromAConfigFileWithoutAWorkDir(t *testing.T) {
	path := writeClusterConfig(t, `wal:
  dir: wal/{{ instance_name }}
groups:
  storages:
    replicasets:
      storage-001:
        instances:
          storage-001-a: {}
`)

	_, _, err := resolveRestoreLayout(restoreLayoutFlags{
		Config:   path,
		Instance: "storage-001-a",
	}, clusterConfigDirs)
	require.ErrorIs(t, err, restore.ErrValidation)
	assert.ErrorContains(t, err, "snapshot.dir")
}
