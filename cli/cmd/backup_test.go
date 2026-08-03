package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/apex/log"
	"github.com/apex/log/handlers/memory"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/util"
)

func TestParseFromVclock_empty(t *testing.T) {
	vc, err := parseFromVclock("")
	require.NoError(t, err)
	assert.Nil(t, vc, "empty flag means a full backup")
}

func TestParseFromVclock_json(t *testing.T) {
	vc, err := parseFromVclock(`{"1":1500,"2":230}`)
	require.NoError(t, err)
	assert.Equal(t, map[uint32]uint64{1: 1500, 2: 230}, map[uint32]uint64(vc))
}

func TestParseFromVclock_invalid(t *testing.T) {
	_, err := parseFromVclock("not-json")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid --from-vclock")
}

func TestParseFromVclock_null(t *testing.T) {
	_, err := parseFromVclock("null")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-vclock")
}

func TestParseFromVclock_emptyObject(t *testing.T) {
	_, err := parseFromVclock("{}")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "--from-vclock")
}

// Execute prints an ArgError a second time and follows it with the root
// command's usage -- the list of every tt subcommand, which says nothing about
// a malformed vclock. The backup commands silence the usage block for exactly
// that reason, so none of their errors may travel as one.
func TestRunBackupStartInvalidFromVclockIsNotAnArgError(t *testing.T) {
	for _, value := range []string{"not-json", "null", "{}"} {
		t.Run(value, func(t *testing.T) {
			backupStartFromVclock = value
			t.Cleanup(func() { backupStartFromVclock = "" })

			// The parse runs before the target is dialed, so the address is
			// never reached.
			_, err := runBackupStartInner([]string{"127.0.0.1:0"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), "invalid --from-vclock")

			var argError *util.ArgError
			assert.False(t, errors.As(err, &argError),
				"the error is reported once, without the root usage")
		})
	}
}

func TestInstanceNameFromTarget(t *testing.T) {
	tests := []struct {
		target string
		want   string
	}{
		{"app:router-001", "router-001"},
		{"app:", ""},
		{"localhost:3301", "3301"},
		{"suffix", ""},
	}
	for _, tc := range tests {
		t.Run(tc.target, func(t *testing.T) {
			assert.Equal(t, tc.want, instanceNameFromTarget(tc.target))
		})
	}
}

func TestBackupSubcommandsSilenceUsage(t *testing.T) {
	for _, cmd := range NewBackupCmd().Commands() {
		t.Run(cmd.Name(), func(t *testing.T) {
			assert.True(t, cmd.SilenceUsage,
				"a runtime error must not be buried under the flag list")
		})
	}
}

// setFlag points a package-level cobra flag variable at value for one test.
// Build the command first: registering a flag resets its variable to the
// flag's default value.
func setFlag[T any](t *testing.T, target *T, value T) {
	t.Helper()

	previous := *target
	t.Cleanup(func() { *target = previous })

	*target = value
}

// captureLog redirects the global logger into a memory handler for one test.
func captureLog(t *testing.T) *memory.Handler {
	t.Helper()

	handler := memory.New()
	previous := log.Log
	t.Cleanup(func() { log.Log = previous })
	log.SetHandler(handler)

	return handler
}

// logMessages returns everything the command logged, one line per entry.
func logMessages(handler *memory.Handler) string {
	lines := make([]string, 0, len(handler.Entries))
	for _, entry := range handler.Entries {
		lines = append(lines, entry.Message)
	}

	return strings.Join(lines, "\n")
}

// storageKeys lists every object stored under a file:// storage root.
func storageKeys(t *testing.T, root string) []string {
	t.Helper()

	keys := make([]string, 0)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}

		key, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr //nolint:wrapcheck
		}
		keys = append(keys, filepath.ToSlash(key))

		return nil
	})
	require.NoError(t, err)

	return keys
}

const (
	testBackupID = "20260326T120000Z"
	testShardA   = "11111111-1111-1111-1111-111111111111"
	testShardB   = "22222222-2222-2222-2222-222222222222"
)

// testFragment builds a valid full-backup fragment for one replicaset.
func testFragment(replicasetUUID, instanceUUID string) *backup.Fragment {
	return &backup.Fragment{
		ReplicasetUUID: replicasetUUID,
		InstanceUUID:   instanceUUID,
		InstanceName:   "instance-" + instanceUUID,
		Hostname:       "host-" + instanceUUID,
		Type:           backup.BackupTypeFull,
		VclockEnd:      backup.Vclock{1: 100},
		Files:          []string{"00000000000000000000.snap"},
		ChecksumSHA256: "c",
		RecoveryPoints: &[]*backup.RecoveryPoint{},
	}
}

// writeJSON marshals value into dir/name and returns the path.
func writeJSON(t *testing.T, dir, name string, value any) string {
	t.Helper()

	data, err := json.Marshal(value)
	require.NoError(t, err)

	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, data, 0o644))

	return path
}

// writeArchive creates a stand-in .tar.zst named after the replicaset UUID the
// uploader parses out of the filename.
func writeArchive(t *testing.T, dir, replicasetUUID string) string {
	t.Helper()

	path := filepath.Join(dir, testBackupID+"-"+replicasetUUID+".tar.zst")
	require.NoError(t, os.WriteFile(path, []byte("archive"), 0o644))

	return path
}

// writeManifest stores a minimal valid manifest so a read-only command has
// something to find in the storage.
func writeManifest(t *testing.T, root string) {
	t.Helper()

	manifest := backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		Status:           backup.StatusOK,
		CreationTime:     time.Now(),
		Shards: map[string]backup.Shard{
			testShardA: {Instance: &backup.ShardInstance{
				InstanceUUID: "a1",
				InstanceName: "instance-a1",
				Hostname:     "host-a1",
				VclockEnd:    backup.Vclock{1: 100},
				Artifact: backup.Artifact{
					Path:           "data/" + testBackupID + "-" + testShardA + ".tar.zst",
					Type:           backup.BackupTypeFull,
					RecoveryPoints: []backup.RecoveryPoint{},
				},
			}},
		},
		Topology: backup.Topology{Replicasets: map[string][]backup.TopologyInstance{
			testShardA: {{InstanceUUID: "a1", InstanceName: "instance-a1", Hostname: "host-a1"}},
		}},
		Warnings: []backup.Warning{},
	}

	require.NoError(t, os.MkdirAll(filepath.Join(root, "manifests"), 0o755))
	writeJSON(t, filepath.Join(root, "manifests"), testBackupID+".json", manifest)
}

func TestStorageContext(t *testing.T) {
	tests := map[string]struct {
		timeout      time.Duration
		wantDeadline bool
	}{
		"no limit": {timeout: 0},
		"negative": {timeout: -5 * time.Second},
		"positive": {timeout: time.Minute, wantDeadline: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := storageContext(tc.timeout)
			defer cancel()

			_, hasDeadline := ctx.Deadline()
			assert.Equal(t, tc.wantDeadline, hasDeadline)
			assert.NoError(t, ctx.Err())
		})
	}
}

func TestRunBackupLastTimeout(t *testing.T) {
	tests := map[string]struct {
		timeout time.Duration
		wantErr string
	}{
		"no limit":       {timeout: 0},
		"negative":       {timeout: -5 * time.Second},
		"generous":       {timeout: time.Minute},
		"already lapsed": {timeout: time.Nanosecond, wantErr: "context deadline exceeded"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			writeManifest(t, root)

			cmd := newBackupLastCmd()
			setFlag(t, &backupStorageConfig, "file://"+root)
			setFlag(t, &backupLastFormat, formatJSON)
			setFlag(t, &backupLastTimeout, tc.timeout)

			err := runBackupLast(cmd, nil)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestRunBackupGcZeroTimeoutMeansNoLimit(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root)

	cmd := newBackupGcCmd()
	setFlag(t, &backupStorageConfig, "file://"+root)
	setFlag(t, &backupGcFormat, formatJSON)
	setFlag(t, &backupGcKeepFull, 1)
	setFlag(t, &backupGcDryRun, true)
	setFlag(t, &backupGcTimeout, time.Duration(0))

	require.NoError(t, runBackupGc(cmd, nil))
}

func TestBuildUploadManifestRejectsFragmentWithoutArchive(t *testing.T) {
	plan := &backup.BackupPlan{Type: backup.BackupTypeFull}
	fragments := []*backup.Fragment{testFragment(testShardB, "b1")}
	locations := map[string]*backup.ArtifactLocation{
		testShardA: {Path: "data/x.tar.zst", SizeBytes: 7},
	}

	manifest, data, err := buildUploadManifest(testBackupID, plan, fragments, locations)
	require.Error(t, err)
	assert.Contains(t, err.Error(), testShardB)
	assert.Nil(t, manifest)
	assert.Nil(t, data)
}

// uploadInputs prepares a single-shard upload and returns the command, the
// storage root and the local archive path.
func uploadInputs(t *testing.T, fragmentUUID, archiveUUID string) (*cobra.Command, string, string) {
	t.Helper()

	work := t.TempDir()
	root := t.TempDir()

	cmd := newBackupUploadCmd()
	archive := writeArchive(t, work, archiveUUID)
	fragment := writeJSON(t, work, "fragment.json", testFragment(fragmentUUID, "a1"))
	planPath := writeJSON(t, work, "plan.json", backup.BackupPlan{
		FormatVersion: backup.PlanFormatVersion,
		Type:          backup.BackupTypeFull,
		Replicasets: map[string]backup.ReplicasetPlan{
			fragmentUUID: {MasterInstanceUUID: "a1", MasterInstanceName: "instance-a1"},
		},
	})

	setFlag(t, &backupUploadArchives, archive)
	setFlag(t, &backupUploadFragments, fragment)
	setFlag(t, &backupUploadPlan, planPath)
	setFlag(t, &backupUploadBackupID, testBackupID)
	setFlag(t, &backupClusterName, "")
	setFlag(t, &backupEnvironment, "")
	setFlag(t, &backupUploadKeepLocal, true)
	setFlag(t, &backupUploadTimeout, time.Minute)
	setFlag(t, &backupStorageConfig, "file://"+root)

	return cmd, root, archive
}

func TestRunBackupUploadRejectsFragmentWithoutArchive(t *testing.T) {
	// The fragment names one replicaset, the archive filename another: the
	// manifest would carry an empty artifact path for a backup nothing can
	// restore from.
	cmd, root, archive := uploadInputs(t, testShardB, testShardA)
	setFlag(t, &backupUploadKeepLocal, false)

	err := runBackupUpload(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), testShardB)

	assert.Empty(t, storageKeys(t, root), "nothing may be stored for a rejected upload")
	assert.FileExists(t, archive, "the local archive is the only copy left")
}

// twoShardUpload prepares an upload of two fragments, each with its own
// archive, and returns the command and the storage root.
func twoShardUpload(t *testing.T, fragmentA, fragmentB *backup.Fragment) (
	*cobra.Command, string,
) {
	t.Helper()

	work := t.TempDir()
	root := t.TempDir()

	cmd := newBackupUploadCmd()
	archives := []string{
		writeArchive(t, work, testShardA),
		writeArchive(t, work, testShardB),
	}
	fragments := []string{
		writeJSON(t, work, "a.json", fragmentA),
		writeJSON(t, work, "b.json", fragmentB),
	}
	planPath := writeJSON(t, work, "plan.json", backup.BackupPlan{
		FormatVersion: backup.PlanFormatVersion,
		Type:          backup.BackupTypeFull,
		Replicasets: map[string]backup.ReplicasetPlan{
			fragmentA.ReplicasetUUID: {
				MasterInstanceUUID: fragmentA.InstanceUUID,
				MasterInstanceName: fragmentA.InstanceName,
			},
		},
	})

	setFlag(t, &backupUploadArchives, strings.Join(archives, ","))
	setFlag(t, &backupUploadFragments, strings.Join(fragments, ","))
	setFlag(t, &backupUploadPlan, planPath)
	setFlag(t, &backupUploadBackupID, testBackupID)
	setFlag(t, &backupClusterName, "")
	setFlag(t, &backupEnvironment, "")
	setFlag(t, &backupUploadKeepLocal, true)
	setFlag(t, &backupUploadTimeout, time.Minute)
	setFlag(t, &backupStorageConfig, "file://"+root)

	return cmd, root
}

func TestRunBackupUploadReportsStoredShardCount(t *testing.T) {
	cmd, root := twoShardUpload(t,
		testFragment(testShardA, "a1"), testFragment(testShardB, "b1"))

	handler := captureLog(t)
	require.NoError(t, runBackupUpload(cmd, nil))

	data, err := os.ReadFile(filepath.Join(root, "manifests", testBackupID+".json"))
	require.NoError(t, err)

	var manifest backup.ClusterManifest
	require.NoError(t, json.Unmarshal(data, &manifest))

	// Shards are keyed by replicaset: the count reported to the operator is the
	// one the manifest carries, not the length of the fragment list.
	assert.Contains(t, logMessages(handler),
		fmt.Sprintf("(%d shards)", len(manifest.Shards)))
}

func TestRunBackupUploadRejectsDuplicateReplicaset(t *testing.T) {
	// Two fragments naming one replicaset used to collapse into a single shard
	// and be reported as two: the rejection has to happen before anything is
	// stored, so no archive is orphaned and no local copy is removed.
	cmd, root := twoShardUpload(t,
		testFragment(testShardA, "a1"), testFragment(testShardA, "b1"))
	setFlag(t, &backupUploadKeepLocal, false)

	err := runBackupUpload(cmd, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), testShardA)

	assert.Empty(t, storageKeys(t, root), "nothing may be stored for a rejected upload")
	for _, archive := range strings.Split(backupUploadArchives, ",") {
		assert.FileExists(t, archive, "local archives are the only copies left")
	}
}

func TestRunBackupUploadRemovesLocalArchives(t *testing.T) {
	// The other half of a rejected upload keeping its inputs: once the manifest
	// is stored, the local copy is what the run is allowed to drop.
	cmd, root, archive := uploadInputs(t, testShardA, testShardA)
	setFlag(t, &backupUploadKeepLocal, false)

	require.NoError(t, runBackupUpload(cmd, nil))
	assert.Contains(t, storageKeys(t, root), "manifests/"+testBackupID+".json")
	assert.NoFileExists(t, archive)
}

func TestRunBackupUploadZeroTimeoutMeansNoLimit(t *testing.T) {
	cmd, root, _ := uploadInputs(t, testShardA, testShardA)
	setFlag(t, &backupUploadTimeout, time.Duration(0))

	require.NoError(t, runBackupUpload(cmd, nil))
	assert.Contains(t, storageKeys(t, root), "manifests/"+testBackupID+".json")
}

func TestFormatVclockIsOrderedByReplicaID(t *testing.T) {
	tests := map[string]struct {
		vclock backup.Vclock
		want   string
	}{
		"empty":  {vclock: backup.Vclock{}, want: "{}"},
		"single": {vclock: backup.Vclock{2: 7}, want: "{2:7}"},
		"many":   {vclock: backup.Vclock{3: 3, 1: 100, 2: 7}, want: "{1:100,2:7,3:3}"},
		"gaps":   {vclock: backup.Vclock{10: 1, 2: 5}, want: "{2:5,10:1}"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Once per render is not enough: Go randomises map iteration order
			// per range statement, so an unsorted render only differs sometimes.
			for range 20 {
				assert.Equal(t, tc.want, formatVclock(tc.vclock))
			}
		})
	}
}

// renderTable collects everything render logs, with the global logger restored
// afterwards.
func renderTable(t *testing.T, render func()) string {
	t.Helper()

	handler := captureLog(t)
	render()

	return logMessages(handler)
}

func TestPrintPlanTableIsDeterministic(t *testing.T) {
	plan := &backup.BackupPlan{
		Type:             backup.BackupTypeIncremental,
		PreviousBackupID: testBackupID,
		Replicasets: map[string]backup.ReplicasetPlan{
			testShardA: {
				MasterInstanceUUID: "a1",
				MasterInstanceName: "instance-a1",
				FromVclock:         backup.Vclock{3: 3, 1: 100, 2: 7},
			},
			testShardB: {
				MasterInstanceUUID: "b1",
				MasterInstanceName: "instance-b1",
				FromVclock:         backup.Vclock{2: 42, 1: 11},
			},
		},
	}

	// A cron job diffing two runs of the same plan must not see changes that
	// only the map iteration order made up.
	renders := make(map[string]struct{})
	for range 20 {
		renders[renderTable(t, func() { printPlanTable(plan) })] = struct{}{}
	}

	require.Len(t, renders, 1, "table output varies between runs")

	rendered := renderTable(t, func() { printPlanTable(plan) })
	assert.Less(t, strings.Index(rendered, testShardA), strings.Index(rendered, testShardB),
		"replicasets are listed in uuid order")
	assert.Contains(t, rendered, "{1:100,2:7,3:3}")
}

// degradedManifest is a two-replicaset backup in which the second replicaset
// produced nothing but an error.
func degradedManifest() *backup.ClusterManifest {
	return &backup.ClusterManifest{
		SchemaVersion:    backup.SchemaVersion,
		BackupID:         testBackupID,
		BaseFullBackupID: testBackupID,
		Status:           backup.StatusDegraded,
		CreationTime:     time.Now(),
		Shards: map[string]backup.Shard{
			testShardA: {Instance: &backup.ShardInstance{InstanceUUID: "a1"}},
			testShardB: {Error: "instance unreachable"},
		},
		Warnings: []backup.Warning{
			{Code: backup.WarnShardUnreachable, Message: "storage-002-a is not reachable"},
			{Code: backup.WarnStoragePartialUpload, Message: "upload was interrupted"},
		},
	}
}

func TestSplitManifestShards(t *testing.T) {
	backedUp, failed := splitManifestShards(degradedManifest())

	assert.Equal(t, []string{testShardA}, backedUp)
	assert.Equal(t, []string{testShardB}, failed)
}

func TestPrintManifestTableNamesFailedShards(t *testing.T) {
	rendered := renderTable(t, func() { printManifestTable(degradedManifest()) })

	// A shard that produced nothing counted as backed up reads as a complete
	// backup; the replicaset is then found missing at restore time.
	assert.NotContains(t, rendered, "Shards:           2")
	assert.Contains(t, rendered, "1 of 2 backed up")
	assert.Contains(t, rendered, testShardB)
	assert.Contains(t, rendered, "instance unreachable")
	assert.Contains(t, rendered, string(backup.WarnShardUnreachable))
	assert.Contains(t, rendered, string(backup.WarnStoragePartialUpload))
}

// A shard with neither an instance nor an error is malformed, not backed up.
func TestPrintManifestTableNamesShardWithoutReason(t *testing.T) {
	manifest := degradedManifest()
	manifest.Shards[testShardB] = backup.Shard{}

	rendered := renderTable(t, func() { printManifestTable(manifest) })

	assert.Contains(t, rendered, "1 of 2 backed up")
	assert.Contains(t, rendered, testShardB)
	assert.Contains(t, rendered, "no instance recorded")
}

// keepBackupGlobals restores the process-wide config state applyBackupConfig
// writes into.
func keepBackupGlobals(t *testing.T) {
	t.Helper()

	previousOpts, previousPath := cliOpts, cmdCtx.Cli.ConfigPath
	t.Cleanup(func() {
		cliOpts = previousOpts
		cmdCtx.Cli.ConfigPath = previousPath
	})
}

func TestApplyBackupConfigMissingFileNamesThePath(t *testing.T) {
	keepBackupGlobals(t)

	// A missing config is answered with the built-in defaults, so the flag is
	// ignored and the failure surfaces as "tt.yaml not found" -- pointing at the
	// cwd instead of the path the job passed.
	missing := filepath.Join(t.TempDir(), "nowhere", "tt.yaml")

	err := applyBackupConfig(missing)
	require.Error(t, err)
	assert.Contains(t, err.Error(), missing)
	assert.Nil(t, cliOpts, "a rejected config must not be applied")
}

func TestApplyBackupConfigLoadsExistingFile(t *testing.T) {
	keepBackupGlobals(t)

	dir := t.TempDir()
	path := filepath.Join(dir, "tt.yaml")
	require.NoError(t, os.WriteFile(path, []byte("env:\n  instances_enabled: .\n"), 0o644))

	require.NoError(t, applyBackupConfig(path))
	assert.Equal(t, path, cmdCtx.Cli.ConfigPath)
	require.NotNil(t, cliOpts)
}

func TestApplyBackupConfigEmptyFlagIsANoop(t *testing.T) {
	keepBackupGlobals(t)

	require.NoError(t, applyBackupConfig(""))
	assert.Nil(t, cliOpts, "without the flag the root config stays in charge")
}

func TestRunBackupPlanRejectsInvalidStorageURI(t *testing.T) {
	// Nothing in the full path reads the storage, so the URI has to be rejected
	// up front: otherwise the typo survives the plan and is only found a whole
	// backup window later, at upload.
	for _, target := range []string{
		string(backup.BackupTypeFull),
		string(backup.BackupTypeIncremental),
	} {
		t.Run(target, func(t *testing.T) {
			cmd := newBackupPlanCmd()
			setFlag(t, &backupPlanMode, target)
			setFlag(t, &backupPlanFormat, formatJSON)
			setFlag(t, &backupStorageConfig, "bogus://backups")

			err := runBackupPlan(cmd, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "unsupported storage scheme")
			assert.Contains(t, err.Error(), "bogus")
		})
	}
}

// The plan says which cluster and environment a backup belongs to, so upload
// does not have to be told again; a flag overrides it when the operator means
// to put the backup somewhere else.
func TestUploadScope(t *testing.T) {
	planned := &backup.BackupPlan{ClusterName: "payments", Environment: "production"}

	cases := []struct {
		name           string
		plan           *backup.BackupPlan
		clusterFlag    string
		envFlag        string
		wantCluster    string
		wantEnv        string
		wantFlagOrigin bool
	}{
		{
			name:        "from the plan",
			plan:        planned,
			wantCluster: "payments",
			wantEnv:     "production",
		},
		{
			name:           "environment overridden, cluster kept",
			plan:           planned,
			envFlag:        "staging",
			wantCluster:    "payments",
			wantEnv:        "staging",
			wantFlagOrigin: true,
		},
		{
			name:           "both overridden",
			plan:           planned,
			clusterFlag:    "orders",
			envFlag:        "staging",
			wantCluster:    "orders",
			wantEnv:        "staging",
			wantFlagOrigin: true,
		},
		{
			name:           "a plan carrying no scope",
			plan:           &backup.BackupPlan{},
			clusterFlag:    "payments",
			wantCluster:    "payments",
			wantFlagOrigin: true,
		},
		{
			name: "neither",
			plan: &backup.BackupPlan{},
		},
		{
			// The plan already holds trimmed names, so a flag padded with
			// whitespace names the same place and overrides nothing.
			name:        "a flag that differs from the plan only by whitespace",
			plan:        planned,
			clusterFlag: "  payments  ",
			envFlag:     " production ",
			wantCluster: "payments",
			wantEnv:     "production",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setFlag(t, &backupClusterName, tc.clusterFlag)
			setFlag(t, &backupEnvironment, tc.envFlag)

			clusterName, environment, origin := uploadScope(tc.plan)
			assert.Equal(t, tc.wantCluster, clusterName)
			assert.Equal(t, tc.wantEnv, environment)

			// The origin is what an invalid value is blamed on, so it has to
			// name the flags only when the value actually came from them.
			if tc.wantFlagOrigin {
				assert.Equal(t, scopeFromFlags, origin)
			} else {
				assert.Contains(t, origin, "the plan")
			}
		})
	}
}
