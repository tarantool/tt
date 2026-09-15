package restore

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLayoutFromConfig(t *testing.T) {
	tests := []struct {
		name     string
		dirs     ConfigDirs
		instance string
		workDir  string
		override Layout
		want     Layout
	}{
		{
			name: "absolute directories need nothing to resolve them",
			dirs: ConfigDirs{
				Snapshot: "/srv/memtx",
				WAL:      "/srv/wal",
				Vinyl:    "/srv/vinyl",
			},
			instance: "i1",
			want:     Layout{Snapshot: "/srv/memtx", WAL: "/srv/wal", Vinyl: "/srv/vinyl"},
		},
		{
			name: "relative directories are taken against the work directory",
			dirs: ConfigDirs{
				Snapshot: "memtx",
				WAL:      "wal",
				Vinyl:    "vinyl",
			},
			instance: "i1",
			workDir:  "/base",
			want:     Layout{Snapshot: "/base/memtx", WAL: "/base/wal", Vinyl: "/base/vinyl"},
		},
		{
			name:     "a configuration naming no directory uses Tarantool's default",
			instance: "i1",
			workDir:  "/base",
			want: Layout{
				Snapshot: "/base/var/lib/i1",
				WAL:      "/base/var/lib/i1",
				Vinyl:    "/base/var/lib/i1",
			},
		},
		{
			name: "the instance name is substituted",
			dirs: ConfigDirs{
				Snapshot: "/srv/{{ instance_name }}/memtx",
				WAL:      "wal/{{ instance_name }}",
			},
			instance: "i1",
			workDir:  "/base",
			want: Layout{
				Snapshot: "/srv/i1/memtx",
				WAL:      "/base/wal/i1",
				Vinyl:    "/base/var/lib/i1",
			},
		},
		{
			name: "a relative process.work_dir sits under the work directory",
			dirs: ConfigDirs{
				WAL:            "wal/{{ instance_name }}",
				ProcessWorkDir: "app",
			},
			instance: "i1",
			workDir:  "/base",
			want: Layout{
				Snapshot: "/base/app/var/lib/i1",
				WAL:      "/base/app/wal/i1",
				Vinyl:    "/base/app/var/lib/i1",
			},
		},
		{
			name: "an absolute process.work_dir leaves the work directory unused",
			dirs: ConfigDirs{
				Snapshot:       "memtx",
				Vinyl:          "/srv/vinyl",
				ProcessWorkDir: "/srv/app",
			},
			instance: "i1",
			workDir:  "/base",
			want: Layout{
				Snapshot: "/srv/app/memtx",
				WAL:      "/srv/app/var/lib/i1",
				Vinyl:    "/srv/vinyl",
			},
		},
		{
			name: "the instance name is substituted into process.work_dir too",
			dirs: ConfigDirs{
				ProcessWorkDir: "/srv/{{ instance_name }}",
			},
			instance: "i1",
			want: Layout{
				Snapshot: "/srv/i1/var/lib/i1",
				WAL:      "/srv/i1/var/lib/i1",
				Vinyl:    "/srv/i1/var/lib/i1",
			},
		},
		{
			name: "absolute directories under a relative process.work_dir need no work dir",
			dirs: ConfigDirs{
				Snapshot:       "/srv/memtx",
				WAL:            "/srv/wal",
				Vinyl:          "/srv/vinyl",
				ProcessWorkDir: "app",
			},
			instance: "i1",
			want:     Layout{Snapshot: "/srv/memtx", WAL: "/srv/wal", Vinyl: "/srv/vinyl"},
		},
		{
			name: "an override replaces the key it names and nothing else",
			dirs: ConfigDirs{
				Snapshot: "/srv/memtx",
				WAL:      "/srv/wal",
				Vinyl:    "/srv/vinyl",
			},
			instance: "i1",
			override: Layout{WAL: "/ssd/wal"},
			want:     Layout{Snapshot: "/srv/memtx", WAL: "/ssd/wal", Vinyl: "/srv/vinyl"},
		},
		{
			name: "an overridden key needs no work directory to resolve against",
			dirs: ConfigDirs{
				Snapshot: "/srv/memtx",
				WAL:      "wal",
				Vinyl:    "/srv/vinyl",
			},
			instance: "i1",
			override: Layout{WAL: "/ssd/wal"},
			want:     Layout{Snapshot: "/srv/memtx", WAL: "/ssd/wal", Vinyl: "/srv/vinyl"},
		},
		{
			name:     "every key overridden reads nothing out of the configuration",
			dirs:     ConfigDirs{Snapshot: "memtx", WAL: "wal", Vinyl: "vinyl"},
			instance: "i1",
			override: Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
			want:     Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
		},
		{
			name: "a process.work_dir nothing is taken against is never read",
			dirs: ConfigDirs{
				Snapshot:       "/srv/memtx",
				WAL:            "/srv/wal",
				Vinyl:          "/srv/vinyl",
				ProcessWorkDir: "/srv/{{ deployment_id }}",
			},
			instance: "i1",
			want:     Layout{Snapshot: "/srv/memtx", WAL: "/srv/wal", Vinyl: "/srv/vinyl"},
		},
		{
			name: "every key overridden leaves process.work_dir unread as well",
			dirs: ConfigDirs{
				Snapshot:       "memtx",
				WAL:            "wal",
				Vinyl:          "vinyl",
				ProcessWorkDir: "/srv/{{ deployment_id }}",
			},
			instance: "i1",
			override: Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
			want:     Layout{Snapshot: "/memtx", WAL: "/wal", Vinyl: "/vinyl"},
		},
		{
			name:     "an override is taken as it stands, with no substitution",
			dirs:     ConfigDirs{Snapshot: "/srv/memtx", WAL: "/srv/wal", Vinyl: "/srv/vinyl"},
			instance: "i1",
			override: Layout{WAL: "wal/{{ instance_name }}"},
			want: Layout{
				Snapshot: "/srv/memtx",
				WAL:      "wal/{{ instance_name }}",
				Vinyl:    "/srv/vinyl",
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			layout, err := LayoutFromConfig(tc.dirs, tc.instance, tc.workDir, tc.override)
			require.NoError(t, err)
			assert.Equal(t, tc.want, layout)
		})
	}
}

// A directory that stays relative is refused rather than resolved against
// whatever the process happens to be running in: the restore would then write
// a whole instance's data somewhere nobody named.
func TestLayoutFromConfig_RejectsWhatItCannotResolve(t *testing.T) {
	tests := []struct {
		name     string
		dirs     ConfigDirs
		instance string
		workDir  string
		override Layout
		reported string
	}{
		{
			name:     "a key an override does not cover still has to resolve",
			dirs:     ConfigDirs{Snapshot: "memtx", WAL: "wal", Vinyl: "vinyl"},
			instance: "i1",
			override: Layout{WAL: "/ssd/wal", Vinyl: "/srv/vinyl"},
			reported: `snapshot.dir is "memtx"`,
		},
		{
			name:     "a relative directory with no work directory",
			dirs:     ConfigDirs{Snapshot: "/srv/memtx", WAL: "wal", Vinyl: "/srv/vinyl"},
			instance: "i1",
			reported: `wal.dir is "wal"`,
		},
		{
			name:     "a configuration naming no directory and no work directory",
			instance: "i1",
			reported: `snapshot.dir is "var/lib/i1"`,
		},
		{
			name: "a relative process.work_dir with no work directory",
			dirs: ConfigDirs{
				Snapshot:       "memtx",
				ProcessWorkDir: "app",
			},
			instance: "i1",
			reported: `relative to process.work_dir "app"`,
		},
		{
			name:     "a directory carrying a variable that is not the instance name",
			dirs:     ConfigDirs{WAL: "/srv/{{ replicaset_name }}/wal"},
			instance: "i1",
			workDir:  "/base",
			reported: "missing vars: replicaset_name",
		},
		{
			name: "a process.work_dir a surviving directory still needs",
			dirs: ConfigDirs{
				Snapshot:       "/srv/memtx",
				WAL:            "wal",
				Vinyl:          "/srv/vinyl",
				ProcessWorkDir: "/srv/{{ deployment_id }}",
			},
			instance: "i1",
			workDir:  "/base",
			reported: "missing vars: deployment_id",
		},
		{
			name:     "no instance to substitute",
			dirs:     ConfigDirs{Snapshot: "/srv/memtx"},
			workDir:  "/base",
			reported: "no instance name given",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LayoutFromConfig(tc.dirs, tc.instance, tc.workDir, tc.override)
			require.ErrorIs(t, err, ErrValidation)
			assert.ErrorContains(t, err, tc.reported)
		})
	}
}
