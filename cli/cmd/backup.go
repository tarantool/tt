package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/backup/chain"
	"github.com/tarantool/tt/cli/backup/gc"
	"github.com/tarantool/tt/cli/backup/verify"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
)

// tt backup start / finalize / last / verify / gc flags. They are package-level because
// cobra flag bindings need stable addresses; only one backup subcommand runs per process.
var (
	backupStartCfg        string
	backupStartID         string
	backupStartFromVclock string
	backupStartTTL        time.Duration

	backupFinalizeCfg string
	backupFinalizeID  string

	backupStorageConfig string
	backupLastFormat    string
	backupLastTimeout   time.Duration

	backupVerifyFormat  string
	backupVerifyTimeout time.Duration

	backupGcFormat    string
	backupGcTimeout   time.Duration
	backupGcKeepFull  int
	backupGcKeepDays  int
	backupGcOrphanAge time.Duration
	backupGcDryRun    bool

	backupPlanMode    string
	backupPlanCfg     string
	backupPlanFormat  string
	backupPlanTimeout time.Duration

	backupUploadArchives    string
	backupUploadFragments   string
	backupUploadPlan        string
	backupUploadBackupID    string
	backupUploadClusterName string
	backupUploadEnvironment string
	backupUploadKeepLocal   bool
	backupUploadTimeout     time.Duration
)

const (
	formatTable = "table"
	formatJSON  = "json"
)

const (
	// backupVerifyProblemsExitCode is returned by `tt backup verify` when the
	// storage is reachable but unhealthy, so that a cron job tells problems found
	// from a run that could not check anything at all (exit code 1). It matches
	// the fail-loud code of `tt backup start`.
	backupVerifyProblemsExitCode = 2

	// defaultVerifyTimeout covers a full pass over the storage: verify reads every
	// archive end to end to recompute its checksum, so it needs far more time than
	// the metadata-only commands.
	defaultVerifyTimeout = 30 * time.Minute

	// defaultGcTimeout covers reading every manifest and deleting the objects the
	// retention rules select; unlike verify, gc never reads archive contents.
	defaultGcTimeout = 10 * time.Minute
)

const backupPlanTargetHelp = `backup mode (required):
  incremental - build on top of the latest manifest (requires a valid chain)
  full        - start a new chain, no chain checks

For --target=incremental the command loads the latest manifest, builds the
backup chain, and verifies that the latest manifest is the chain's latest
entry with no problems. If any check fails, the command exits with a
detailed error (no auto-promotion to full).`

// backupStorageURIHelp documents the --backup-storage flag value for every
// manager-side backup command.
const backupStorageURIHelp = `The --backup-storage flag accepts a URI describing the
storage backend, or @<path> naming a YAML config file:

    file://<abs_path>?Prefix=<subdir>
      Local filesystem storage. The path must be absolute.
      Query parameters:
        Prefix           - subdirectory within the path (optional).

    s3://<bucket>/<prefix>
      S3 storage taken from the standard AWS_* environment variables, so that
      no credential is written in the command line:
        AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY  - credentials (required).
        AWS_SESSION_TOKEN                         - for temporary credentials.
        AWS_REGION, AWS_DEFAULT_REGION            - region.
        AWS_ENDPOINT_URL, AWS_ENDPOINT_URL_S3     - endpoint of an
                                                    S3-compatible storage.
        AWS_CA_BUNDLE                             - CA of an endpoint the
                                                    system roots do not cover.

    s3+https://endpoint:port/bucket/prefix?Region=...&AccessKeyID=...&SecretAccessKey=...
    s3+http://endpoint:port/bucket/prefix?Region=...&AccessKeyID=...&SecretAccessKey=...
      S3-compatible storage with the endpoint and the credentials in the URI.
      Use s3+https for TLS, s3+http for plain TCP. The first path segment
      after the host is the bucket name, the rest is the optional key prefix.
      Query parameters:
        Region           - AWS region (optional).
        AccessKeyID      - access key ID (required).
        SecretAccessKey  - secret access key (required).

    @<path>.yaml
      What a URI cannot express: a custom endpoint, a region, explicit
      credentials, a private CA. The backend is chosen by 'type', an unknown
      field is an error, and ${VAR} in a value is read from the environment
      ('$$' is a literal '$'), so a secret stays out of the file:

        type: s3
        endpoint: https://storage.yandexcloud.net    # https unless http:// is asked for
        region: ru-central1
        bucket: payments-backups
        prefix: tarantool                            # storage root in the bucket, optional
        access_key_id: ${AWS_ACCESS_KEY_ID}          # optional, AWS_* by default
        secret_access_key: ${AWS_SECRET_ACCESS_KEY}
        ca_cert: /etc/ssl/private-ca.pem             # or skip_verify: true

        type: fs
        root: /mnt/backups/payments        # storage root, must be absolute
        prefix: mycluster                  # subdirectory within the root, optional`

// storageContext builds the context of a command's storage phase. A zero or
// negative --timeout means no limit rather than "already expired": a storage
// large enough to outlast any sensible deadline still has to be usable, and the
// flag has to mean the same thing on every backup subcommand.
func storageContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(context.Background())
	}

	return context.WithTimeout(context.Background(), timeout)
}

// NewBackupCmd creates the parent `tt backup` command.
func NewBackupCmd() *cobra.Command {
	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage Tarantool backups (PITR)",
	}

	backupCmd.AddCommand(
		newBackupStartCmd(),
		newBackupFinalizeCmd(),
		newBackupLastCmd(),
		newBackupVerifyCmd(),
		newBackupGcCmd(),
		newBackupPlanCmd(),
		newBackupUploadCmd(),
	)

	// A failed run has already said what it managed to do; burying that under
	// the flag list is the last thing an operator needs in a cron log.
	for _, subCmd := range backupCmd.Commands() {
		subCmd.SilenceUsage = true
	}

	return backupCmd
}

// newBackupStartCmd creates `tt backup start`.
func newBackupStartCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "start (<APP:INSTANCE>|<URI>) [flags]",
		Short: "Open a backup on the instance and build a local archive",
		Long: `Open box.backup on the instance, pack WAL files and a per-shard manifest
fragment into a .tar.zst archive under /tmp/tt-backup/<backup-id>/, and leave
box.backup open. The archive path is printed to stdout. Closing box.backup is
done by 'tt backup finalize' after the manifest has been uploaded.`,
		Args: cobra.ExactArgs(1),
		RunE: runBackupStart,
	}
	cmd.Flags().StringVarP(&backupStartCfg, "config", "c", "",
		"path to the cluster configuration file (for <APP:INSTANCE>)")
	cmd.Flags().StringVar(&backupStartID, "backup-id", "",
		"backup identifier (required)")
	cmd.Flags().StringVar(&backupStartFromVclock, "from-vclock", "",
		"vclock of the last manifest (JSON object, e.g. '{\"1\":1500}'); "+
			"incremental only")
	cmd.Flags().DurationVar(&backupStartTTL, "ttl", time.Hour,
		"force the backup to complete after this duration")

	cmd.MarkFlagRequired("backup-id")

	return cmd
}

// newBackupFinalizeCmd creates `tt backup finalize`.
func newBackupFinalizeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "finalize (<APP:INSTANCE>|<URI>) [flags]",
		Short: "Close the backup on the instance and remove the local archive",
		Long: `Run box.backup.stop() on the instance and remove the local .tar.zst
archive. Idempotent: if the backup is already closed, it does not fail.`,
		Args: cobra.ExactArgs(1),
		RunE: runBackupFinalize,
	}

	cmd.Flags().StringVarP(&backupFinalizeCfg, "config", "c", "",
		"path to the cluster configuration file (for <APP:INSTANCE>)")
	cmd.Flags().StringVar(&backupFinalizeID, "backup-id", "",
		"backup identifier (required); local artifacts of the target replicaset are removed")
	cmd.MarkFlagRequired("backup-id")

	return cmd
}

func newBackupLastCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "last --backup-storage=<uri> [--format <table|json>]",
		Short: "Show the last backup manifest from the storage",
		Long:  `Find the latest manifest in the storage and print it to stdout.`,
		Example: `$ tt backup last --backup-storage=file:///var/backups
  $ tt backup last --backup-storage=file:///var/backups?Prefix=mycluster
  $ tt backup last --backup-storage=s3://payments-backups/tarantool
  $ tt backup last --backup-storage=@s3-prod.yaml
  $ tt backup last --backup-storage=s3+https://s3.example.com:9000/... \
    ?Region=us-east-1&AccessKeyID=minio&SecretAccessKey=minio123
  $ tt backup last --backup-storage=file:///var/backups --format json`,
		Args: cobra.NoArgs,
		RunE: runBackupLast,
	}

	cmd.Flags().StringVar(&backupStorageConfig, "backup-storage", "",
		backupStorageURIHelp)
	cmd.Flags().StringVar(&backupLastFormat, "format", formatTable,
		"output format: `table` or `json`")
	cmd.Flags().DurationVar(&backupLastTimeout, "timeout", time.Minute,
		"timeout for connecting to and reading from the storage; 0 means no limit")

	cmd.MarkFlagRequired("backup-storage")

	return cmd
}

func newBackupPlanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "plan --target=(incremental|full) --backup-storage=<uri> [flags]",
		Short: "Plan the next backup: mode, master source, from_vclock",
		Long: `Compute a backup plan from the current cluster topology and the latest
		manifest in the storage.`,
		Example: `$ tt backup plan --target=incremental --backup-storage=file:///var/backups
  $ tt backup plan --target=full --backup-storage=file:///var/backups --format json
  $ tt backup plan --target=incremental --backup-storage=s3+https://... -c cluster.yaml`,
		Args: cobra.NoArgs,
		RunE: runBackupPlan,
	}

	addTarantoolConnectFlags(cmd)
	cmd.Flags().StringVar(&backupPlanMode, "target", "",
		backupPlanTargetHelp)
	cmd.Flags().StringVar(&backupStorageConfig, "backup-storage", "",
		backupStorageURIHelp)
	cmd.Flags().StringVarP(&backupPlanCfg, "config", "c", "",
		clusterUriHelp)
	cmd.Flags().StringVar(&backupPlanFormat, "format", formatJSON,
		"output format: table or json")
	cmd.Flags().DurationVar(&backupPlanTimeout, "timeout", time.Minute,
		"timeout for storage operations; 0 means no limit")

	cmd.MarkFlagRequired("target")
	cmd.MarkFlagRequired("backup-storage")
	cmd.MarkFlagRequired("config")

	return cmd
}

func newBackupUploadCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "upload [flags]",
		Short: "Build cluster manifest from per-shard fragments and upload to storage",
		Long: `Run on the manager host after the orchestrator collected .tar.zst archives
and instance_backup.json fragments from all cluster nodes.`,
		Example: `$ tt backup upload \
    --archives /tmp/bkp/20260326T120000Z-A.tar.zst,/tmp/bkp/20260326T120000Z-B.tar.zst \
    --fragments /tmp/bkp/A.json,/tmp/bkp/B.json \
    --plan /tmp/bkp/plan.json \
    --backup-storage file:///var/backups \
    --backup-id 20260326T120000Z \
    --cluster-name payments-cluster \
    --environment production`,
		Args: cobra.NoArgs,
		RunE: runBackupUpload,
	}

	cmd.Flags().StringVar(&backupUploadArchives, "archives", "",
		"comma-separated paths to .tar.zst archives (required)")
	cmd.Flags().StringVar(&backupUploadFragments, "fragments", "",
		"comma-separated paths to per-shard instance_backup.json fragments")
	cmd.Flags().StringVar(&backupUploadPlan, "plan", "",
		"path to tt backup plan JSON (required); provides type, "+
			"previous_backup_id and expected replicasets")
	cmd.Flags().StringVar(&backupStorageConfig, "backup-storage", "",
		backupStorageURIHelp)
	cmd.Flags().StringVar(&backupUploadBackupID, "backup-id", "",
		"backup identifier, e.g. 20260326T120000Z (required)")
	cmd.Flags().StringVar(&backupUploadClusterName, "cluster-name", "",
		"cluster name; used as a storage path component")
	cmd.Flags().StringVar(&backupUploadEnvironment, "environment", "",
		"environment tag (production, staging, ...); used as a storage path component")
	cmd.Flags().BoolVar(&backupUploadKeepLocal, "keep-local", false,
		"keep local .tar.zst copies on the manager host after successful upload")
	cmd.Flags().DurationVar(&backupUploadTimeout, "timeout", 30*time.Minute,
		"timeout for storage operations; 0 means no limit")

	cmd.MarkFlagRequired("archives")
	cmd.MarkFlagRequired("fragments")
	cmd.MarkFlagRequired("plan")
	cmd.MarkFlagRequired("backup-storage")
	cmd.MarkFlagRequired("backup-id")

	return cmd
}

func runBackupUpload(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	// The id is the manifest key and the prefix of every archive key: reject it
	// before anything is stored, so a malformed run cannot claim another
	// object's name.
	if err := backup.ValidateBackupID(backupUploadBackupID); err != nil {
		return err //nolint:wrapcheck
	}

	keyPrefix := backup.StoragePrefix(backupUploadClusterName, backupUploadEnvironment)
	backupID := backup.BackupID(backupUploadBackupID)
	fragmentPaths := backup.SplitPaths(backupUploadFragments)
	archivePaths := backup.SplitPaths(backupUploadArchives)

	if len(fragmentPaths) != len(archivePaths) {
		return fmt.Errorf("fragment and archive paths must have the same length")
	}

	// Read the plan, fragments, and validate coverage.
	plan, err := backup.ReadPlan(backupUploadPlan)
	if err != nil {
		return fmt.Errorf("failed to read plan: %w", err)
	}

	fragments, err := backup.ReadFragments(fragmentPaths)
	if err != nil {
		return fmt.Errorf("failed to read fragments: %w", err)
	}

	if err := backup.ValidateFragmentsAgainstPlan(fragments, plan); err != nil {
		return fmt.Errorf("failed to validate fragments: %w", err)
	}

	// Prepare archives: stat, extract UUIDs, compute storage keys..
	archives, locationsByReplicaset, err := backup.PrepareArchives(
		archivePaths, keyPrefix, backupID)
	if err != nil {
		return fmt.Errorf("failed to prepare archives: %w", err)
	}

	// The manifest is built before the storage is touched, so a fragment that
	// does not line up with an archive costs nothing: no object is written and
	// no local archive is removed.
	manifest, manifestData, err := buildUploadManifest(
		backupID, plan, fragments, locationsByReplicaset)
	if err != nil {
		return fmt.Errorf("failed to build manifest: %w", err)
	}

	// Open storage.
	storeCfg, err := backup.ParseStorageURI(backupStorageConfig)
	if err != nil {
		return fmt.Errorf("failed to parse storage URI: %w", err)
	}

	store, err := backup.OpenStorage(storeCfg)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}

	ctx, cancel := storageContext(backupUploadTimeout)
	defer cancel()

	// Upload archives, then the manifest. On manifest failure, uploaded
	// archives are rolled back (deleted from storage).
	if err := backup.Upload(ctx, store, keyPrefix, backupID, manifestData, archives); err != nil {
		return fmt.Errorf("failed to upload: %w", err)
	}

	// The manifest is what was stored: its shards are keyed by replicaset, the
	// fragment list is only what the run was asked to aggregate.
	log.Infof("backup %q uploaded successfully (%d shards)", backupID, len(manifest.Shards))

	// Remove local archives unless --keep-local was requested.
	if !backupUploadKeepLocal {
		removeLocalArchives(archivePaths)
	}

	return nil
}

// buildUploadManifest aggregates the cluster manifest from the plan and
// fragments and returns it together with its JSON encoding.
func buildUploadManifest(
	backupID backup.BackupID,
	plan *backup.BackupPlan,
	fragments []*backup.Fragment,
	locationsByReplicaset map[string]*backup.ArtifactLocation,
) (*backup.ClusterManifest, []byte, error) {
	baseFullBackupID := plan.BaseFullBackupID
	if plan.Type == backup.BackupTypeFull {
		baseFullBackupID = backupID
	}

	shards := make([]*backup.ShardInput, 0, len(fragments))
	for _, fragment := range fragments {
		// Archives are keyed by the UUID in their file name, fragments by the
		// UUID they carry. A fragment with no archive would be aggregated with
		// an empty artifact path -- a manifest that validates, restores nothing
		// and outlives the local archive removed right after the upload.
		location, ok := locationsByReplicaset[fragment.ReplicasetUUID]
		if !ok {
			return nil, nil, fmt.Errorf(
				"no archive given for replicaset %q: expected an archive named %q",
				fragment.ReplicasetUUID,
				fmt.Sprintf("%s-%s.tar.zst", backupID, fragment.ReplicasetUUID))
		}

		shards = append(shards, &backup.ShardInput{
			ReplicasetUUID: fragment.ReplicasetUUID,
			Fragment:       fragment,
			Location:       location,
		})
	}

	manifest, err := backup.Aggregate(backup.AggregateInput{
		BackupID:         backupID,
		PreviousBackupID: plan.PreviousBackupID,
		BaseFullBackupID: baseFullBackupID,
		CreationTime:     time.Now(),
		Topology:         backup.BuildTopologyFromFragments(fragments),
		Shards:           shards,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to aggregate manifest: %w", err)
	}

	manifestData, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal manifest: %w", err)
	}

	return manifest, manifestData, nil
}

// removeLocalArchives best-effort removes local archive files, logging a
// warning for any failure other than the file already being gone.
func removeLocalArchives(paths []string) {
	for _, path := range paths {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			log.Warnf("failed to remove local archive %q: %s", path, err)
		}
	}
}

func runBackupLast(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	if backupLastFormat != formatTable && backupLastFormat != formatJSON {
		return fmt.Errorf("unsupported format %q: expected %q or %q",
			backupLastFormat, formatTable, formatJSON)
	}

	cfg, err := backup.ParseStorageURI(backupStorageConfig)
	if err != nil {
		return fmt.Errorf("failed to parse storage URI: %w", err)
	}

	store, err := backup.OpenStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}

	ctx, cancel := storageContext(backupLastTimeout)
	defer cancel()

	manifest, err := backup.LatestManifest(ctx, store)
	if err != nil {
		return fmt.Errorf("failed to load latest backup manifest: %w", err)
	}
	if manifest == nil {
		return fmt.Errorf("no backups found in storage")
	}

	switch backupLastFormat {
	case formatJSON:
		data, err := json.MarshalIndent(manifest, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal manifest: %w", err)
		}

		fmt.Println(string(data))
	case formatTable:
		printManifestTable(manifest)
	}

	return nil
}

func printManifestTable(manifest *backup.ClusterManifest) {
	log.Info("Latest backup manifest")
	log.Infof("  Backup ID:        %s", manifest.BackupID)
	log.Infof("  Status:           %s", manifest.Status)
	log.Infof("  Schema version:   %d", manifest.SchemaVersion)
	if manifest.PreviousBackupID != "" {
		log.Infof("  Previous backup:  %s", manifest.PreviousBackupID)
	}
	if manifest.BaseFullBackupID != "" {
		log.Infof("  Base full backup: %s", manifest.BaseFullBackupID)
	}
	log.Infof("  Created:          %s", manifest.CreationTime.Format(time.RFC3339))
	log.Infof("  Shards:           %d", len(manifest.Shards))
	if len(manifest.Warnings) > 0 {
		log.Infof("  Warnings:         %d", len(manifest.Warnings))
	}
}

func newBackupVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Check backup manifests and archives in the storage",
		Long: `Health-check the backup storage and report what is wrong with it.

	The check reads the whole storage and reports:
	  - manifests referring to archives that are not stored;
	  - archives whose bytes do not match checksum_sha256 of the manifest;
	  - breaks in the previous_backup_id chain (orphans and forks);
	  - increments whose vclock_begin does not continue the previous backup;
	  - archives no manifest refers to.

	An archive of a backup newer than every stored manifest is reported too, but
	as an upload in progress rather than a problem: an upload writes its archives
	before the manifest that references them, so this is what a backup being taken
	right now looks like from the outside.

	Nothing is ever deleted or repaired: removing backups is 'tt backup gc'.
	Exit codes: 0 - the storage is healthy, 2 - problems were found, 1 - the
	storage could not be checked.`,
		Example: `$ tt backup verify --backup-storage=file:///var/backups
  $ tt backup verify --backup-storage=file:///var/backups --format json`,
		Args: cobra.NoArgs,
		RunE: runBackupVerify,
	}

	cmd.Flags().StringVar(&backupStorageConfig, "backup-storage", "",
		backupStorageURIHelp)
	cmd.Flags().StringVar(&backupVerifyFormat, "format", formatTable,
		"output format: table or json")
	cmd.Flags().DurationVar(&backupVerifyTimeout, "timeout", defaultVerifyTimeout,
		"timeout for connecting to and reading from the storage; 0 means no limit")

	cmd.MarkFlagRequired("backup-storage")

	return cmd
}

func runBackupVerify(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	healthy, err := runBackupVerifyInner()
	if err != nil {
		return fmt.Errorf("backup verify: %w", err)
	}

	if !healthy {
		// The report has already been printed; a second error line would only
		// repeat it. The exit code carries the verdict for a cron job.
		os.Exit(backupVerifyProblemsExitCode)
	}

	return nil
}

// runBackupVerifyInner checks the storage, prints the report and reports whether
// the storage turned out to be healthy.
func runBackupVerifyInner() (bool, error) {
	if backupVerifyFormat != formatTable && backupVerifyFormat != formatJSON {
		return false, fmt.Errorf("unsupported format %q: expected %q or %q",
			backupVerifyFormat, formatTable, formatJSON)
	}

	cfg, err := backup.ParseStorageURI(backupStorageConfig)
	if err != nil {
		return false, fmt.Errorf("failed to parse storage URI: %w", err)
	}

	store, err := backup.OpenStorage(cfg)
	if err != nil {
		return false, fmt.Errorf("failed to open storage: %w", err)
	}

	ctx, cancel := storageContext(backupVerifyTimeout)
	defer cancel()

	report, err := verify.Verify(ctx, store)
	if err != nil {
		return false, fmt.Errorf("failed to verify backup storage: %w", err)
	}

	switch backupVerifyFormat {
	case formatJSON:
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			return false, fmt.Errorf("failed to marshal verification report: %w", err)
		}

		fmt.Println(string(data))
	case formatTable:
		printVerifyReportTable(report)
	}

	return report.OK(), nil
}

func printVerifyReportTable(report *verify.Report) {
	log.Info("Backup storage verification")
	log.Infof("  Manifests checked: %d", report.Manifests)
	log.Infof("  Archives checked:  %d", report.Archives)

	if problems := report.Problems(); problems > 0 {
		log.Infof("  Problems:          %d", problems)
	} else {
		log.Info("  Problems:          none")
	}

	for _, issue := range report.Issues {
		inherited := ""
		if issue.Inherited {
			inherited = " (inherited)"
		}

		line := fmt.Sprintf("  [%s]%s %s: %s",
			issue.Kind, inherited, verifyIssueTarget(issue), issue.Detail)

		// An informational finding is worth showing but is not what makes the
		// storage unhealthy, so it must not look like the rest.
		if issue.Informational {
			log.Info(line)
			continue
		}

		log.Warn(line)
	}
}

// verifyIssueTarget names what an issue is about: a manifest, one of its shards,
// or a stored archive.
func verifyIssueTarget(issue verify.Issue) string {
	parts := make([]string, 0, 3)
	if issue.BackupID != "" {
		parts = append(parts, "backup "+issue.BackupID)
	}
	if issue.ReplicasetUUID != "" {
		parts = append(parts, "replicaset "+issue.ReplicasetUUID)
	}
	if issue.Archive != "" {
		parts = append(parts, issue.Archive)
	}

	return strings.Join(parts, " ")
}

func newBackupGcCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Delete old backup chains and dangling archives from the storage",
		Long: `Delete backups the retention rules no longer cover, and clean up archives
	no manifest refers to.

	Retention rules combine: a backup is deleted only when no rule keeps it.
	Deletion cascades: a full backup goes only together with every increment
	based on it, and a chain is cut from its newest end, so a kept increment
	always keeps the backups it is recovered from.

	Three things are never deleted, whatever the rules say: the chain holding
	the newest manifest, because an upload may be appending to it; the newest
	chain that can still be recovered from; and archives newer than the newest
	stored manifest. This means gc cannot empty a storage; remove the storage
	root with your storage tooling if that is what you want.

	Chains with problems (a fork, a tail whose full backup is gone) are left
	for 'tt backup verify' to report.

	A dangling archive is re-checked with a direct read of its manifest just
	before it is deleted, so a run may keep an archive its plan listed.`,
		Example: `$ tt backup gc --backup-storage=file:///var/backups --keep-full 3 --dry-run
  $ tt backup gc --backup-storage=file:///var/backups --keep-days 30
  $ tt backup gc --backup-storage=file:///var/backups --keep-full 2 --keep-days 7`,
		Args: cobra.NoArgs,
		RunE: runBackupGc,
	}

	cmd.Flags().StringVar(&backupStorageConfig, "backup-storage", "",
		backupStorageURIHelp)
	cmd.Flags().IntVar(&backupGcKeepFull, "keep-full", 0,
		"keep the last N healthy backup chains; "+
			"without --keep-full or --keep-days nothing is deleted at all")
	cmd.Flags().IntVar(&backupGcKeepDays, "keep-days", 0,
		"keep backups created within the last D days")
	cmd.Flags().DurationVar(&backupGcOrphanAge, "orphan-age", gc.DefaultOrphanAge,
		"minimum age before a dangling archive or a problem chain tail "+
			"is deleted; 0 means the default")
	cmd.Flags().BoolVar(&backupGcDryRun, "dry-run", false,
		"report what would be deleted without deleting anything; "+
			"a real run reports what it kept vs what the plan listed")
	cmd.Flags().StringVar(&backupGcFormat, "format", formatTable,
		"output format: table or json")
	cmd.Flags().DurationVar(&backupGcTimeout, "timeout", defaultGcTimeout,
		"timeout for connecting to and working with the storage; 0 means no limit")

	cmd.MarkFlagRequired("backup-storage")

	return cmd
}

func runBackupGc(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	if backupGcFormat != formatTable && backupGcFormat != formatJSON {
		return fmt.Errorf("unsupported format %q: expected %q or %q",
			backupGcFormat, formatTable, formatJSON)
	}

	if backupGcKeepFull < 0 || backupGcKeepDays < 0 {
		return fmt.Errorf("--keep-full and --keep-days must not be negative")
	}

	if backupGcOrphanAge < 0 {
		return fmt.Errorf("--orphan-age must not be negative")
	}

	cfg, err := backup.ParseStorageURI(backupStorageConfig)
	if err != nil {
		return fmt.Errorf("failed to parse storage URI: %w", err)
	}

	store, err := backup.OpenStorage(cfg)
	if err != nil {
		return fmt.Errorf("failed to open storage: %w", err)
	}

	ctx, cancel := storageContext(backupGcTimeout)
	defer cancel()

	plan, err := gc.BuildPlan(ctx, store, gc.Options{
		KeepFull:  backupGcKeepFull,
		KeepDays:  backupGcKeepDays,
		OrphanAge: backupGcOrphanAge,
	})
	if err != nil {
		return fmt.Errorf("failed to plan backup storage cleanup: %w", err)
	}

	if backupGcDryRun {
		if err := reportBackupGc(plan, nil); err != nil {
			return fmt.Errorf("failed to report the cleanup plan: %w", err)
		}

		return nil
	}

	// Execute reports what it managed to delete even when it fails midway, so the
	// result is printed before the error is returned.
	result, err := gc.Execute(ctx, store, plan)
	if reportErr := reportBackupGc(plan, result); reportErr != nil {
		return fmt.Errorf("failed to report the cleanup: %w", reportErr)
	}

	if err != nil {
		return fmt.Errorf("failed to clean up backup storage: %w", err)
	}

	return nil
}

// backupGcOutput is the machine-readable report of one gc run.
type backupGcOutput struct {
	DryRun bool       `json:"dry_run"`
	Plan   *gc.Plan   `json:"plan"`
	Result *gc.Result `json:"result,omitempty"`
}

// reportBackupGc prints the plan and, for a real run, what was deleted.
func reportBackupGc(plan *gc.Plan, result *gc.Result) error {
	if backupGcFormat == formatJSON {
		data, err := json.MarshalIndent(backupGcOutput{
			DryRun: result == nil,
			Plan:   plan,
			Result: result,
		}, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal cleanup report: %w", err)
		}

		fmt.Println(string(data))

		return nil
	}

	printBackupGcTable(plan, result)

	return nil
}

func printBackupGcTable(plan *gc.Plan, result *gc.Result) {
	if result == nil {
		log.Info("Backup storage cleanup (dry run)")
	} else {
		log.Info("Backup storage cleanup")
	}

	log.Infof("  Backups:           %d", len(plan.Backups))
	log.Infof("  Archives:          %d", plan.Archives())
	log.Infof("  Dangling archives: %d", len(plan.Orphans))

	for _, deleted := range plan.Backups {
		log.Infof("  backup %s (%d archive(s))", deleted.BackupID, len(deleted.ArchiveKeys))
	}

	for _, orphan := range plan.Orphans {
		log.Infof("  dangling %s (modified %s)",
			orphan.Key, orphan.LastModified.UTC().Format(time.RFC3339))
	}

	if plan.Empty() {
		log.Info("  Nothing to delete.")
	}

	for _, note := range plan.Notes {
		log.Warnf("  %s", note)
	}

	if result != nil {
		log.Infof("Deleted %d backup(s), %d archive(s), %d dangling archive(s)",
			result.Backups, result.Archives, result.Orphans)

		for _, kept := range result.Kept {
			log.Warnf("  kept %s: its manifest showed up on a direct read", kept)
		}
	}
}

// applyBackupConfig reloads cliOpts/cmdCtx.Cli.ConfigPath from a per-command
// --config flag. Other tt subcommands rely on the root -c/--cfg flag, but
// 'tt backup' is invoked by the orchestrator with its own --config, so the
// config is reloaded here when the flag is set.
func applyBackupConfig(localCfg string) error {
	if localCfg == "" {
		return nil
	}

	opts, configPath, err := configure.GetCliOpts(localCfg, cmdCtx.Integrity.Repository)
	if err != nil {
		return fmt.Errorf("failed to load config %q: %w", localCfg, err)
	}

	cmdCtx.Cli.ConfigPath = configPath
	cliOpts = opts

	return nil
}

// dialBackupTarget resolves <APP:INSTANCE> or <URI> and dials the binary port
// (box.backup.* is a binary-protocol eval surface).
func dialBackupTarget(cfg, target string) (connector.Connector, error) {
	if err := applyBackupConfig(cfg); err != nil {
		return nil, fmt.Errorf("failed to apply backup config: %w", err)
	}

	connCtx := connect.ConnectCtx{Binary: true}
	connOpts, err := resolveConnectOpts(&cmdCtx, cliOpts, &connCtx, target)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve connection options for %q: %w", target, err)
	}

	conn, err := connector.Connect(connOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to %q: %w", target, err)
	}

	return conn, nil
}

// instanceNameFromTarget extracts the instance name from <APP:INSTANCE>, used
// as a fallback when the instance does not report box.info.name.
func instanceNameFromTarget(target string) string {
	if _, inst, ok := strings.Cut(target, string(running.InstanceDelimiter)); ok {
		return inst
	}

	return ""
}

func runBackupStart(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	// MarkFlagRequired only checks the flag was set, so the id is validated
	// here as well as in backup.Start: the rejection then costs no dial.
	if err := backup.ValidateBackupID(backupStartID); err != nil {
		return err //nolint:wrapcheck
	}

	archivePath, err := runBackupStartInner(args)
	if err != nil {
		if errors.Is(err, backup.ErrAlreadyInProgress) {
			// Fail-loud: exit code 2 so the orchestrator can tell a stuck backup
			// from a regular error and route to the --force branch.
			log.Error(err.Error())
			os.Exit(2)
		}
		return fmt.Errorf("backup start: %w", err)
	}

	fmt.Println(archivePath)
	return nil
}

// runBackupStartInner dials the instance and runs backup.Start.
func runBackupStartInner(args []string) (string, error) {
	fromVclock, err := parseFromVclock(backupStartFromVclock)
	if err != nil {
		// Deliberately not a util.ArgError: Execute answers one by repeating the
		// error and printing the root command's usage, the list of every tt
		// subcommand, which is what the backup commands silence the usage block
		// to keep out of a cron log.
		return "", fmt.Errorf("invalid flag: %w", err)
	}

	conn, err := dialBackupTarget(backupStartCfg, args[0])
	if err != nil {
		return "", fmt.Errorf("failed to dial backup target %q: %w", args[0], err)
	}
	defer conn.Close()

	archivePath, err := backup.Start(conn, backup.BackupStartOpts{
		BackupID:   backupStartID,
		FromVclock: fromVclock,
		TTL:        backupStartTTL,
		InstName:   instanceNameFromTarget(args[0]),
	})
	if err != nil {
		return "", fmt.Errorf("failed to start backup: %w", err)
	}

	return archivePath, nil
}

func runBackupFinalize(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	if err := backup.ValidateBackupID(backupFinalizeID); err != nil {
		return err //nolint:wrapcheck
	}

	conn, err := dialBackupTarget(backupFinalizeCfg, args[0])
	if err != nil {
		return fmt.Errorf("failed to dial backup target %q: %w", args[0], err)
	}
	defer conn.Close()

	if err := backup.Stop(conn, backupFinalizeID); err != nil {
		return fmt.Errorf("failed to finalize backup: %w", err)
	}

	return nil
}

// parseFromVclock parses the --from-vclock flag value (a JSON object such as
// {"1":1500,"2":230}) into a Vclock. An empty string means a full backup.
func parseFromVclock(s string) (backup.Vclock, error) {
	if s == "" {
		return nil, nil
	}

	var vc backup.Vclock

	if err := json.Unmarshal([]byte(s), &vc); err != nil {
		return nil, fmt.Errorf(
			"invalid --from-vclock (expected JSON object like {\"1\":1500}): %w", err)
	}

	// Both decode without an error and both silently change what the backup is:
	// null turns the run into a full backup, {} into an increment with no base
	// that only 'tt backup upload' would reject, a backup window later.
	switch {
	case vc == nil:
		return nil, errors.New(
			"invalid --from-vclock: null is not a vclock; " +
				"omit the flag to take a full backup")
	case len(vc) == 0:
		return nil, errors.New(
			"invalid --from-vclock: {} is not a vclock; pass the vclock of the " +
				"previous backup, or omit the flag to take a full backup")
	}

	return vc, nil
}

func runBackupPlan(cmd *cobra.Command, args []string) error {
	cmdCtx.CommandName = cmd.Name()

	target := backup.BackupType(backupPlanMode)
	switch target {
	case backup.BackupTypeFull, backup.BackupTypeIncremental:
	default:
		return fmt.Errorf("unsupported target %q: expected %q or %q",
			backupPlanMode, backup.BackupTypeFull, backup.BackupTypeIncremental)
	}

	switch backupPlanFormat {
	case formatTable, formatJSON:
	default:
		return fmt.Errorf("unsupported format %q: expected %q or %q",
			backupPlanFormat, formatTable, formatJSON)
	}

	connectCtx := connect.ConnectCtx{
		Username:    replicasetUser,
		Password:    replicasetPassword,
		SslKeyFile:  replicasetSslKeyFile,
		SslCertFile: replicasetSslCertFile,
		SslCaFile:   replicasetSslCaFile,
		SslCiphers:  replicasetSslCiphers,
	}

	merged, hostnames, reachable, err := discoverClusterTopology(&cmdCtx, backupPlanCfg, connectCtx)
	if err != nil {
		return fmt.Errorf("failed to discover cluster topology: %w", err)
	}

	live := topologyToLive(replicasetsToTopology(merged, hostnames, reachable))

	ctx, cancel := storageContext(backupPlanTimeout)
	defer cancel()

	// For incremental: load the chain, check problems, pass the latest manifest.
	var latest *backup.ClusterManifest
	if target == backup.BackupTypeIncremental {
		latest, err = getLastFromChain(ctx, backupStorageConfig)
		if err != nil {
			return fmt.Errorf("failed to get latest backup: %w", err)
		}
	}

	plan, err := backup.Plan(target, latest, &live)
	if err != nil {
		return fmt.Errorf("failed to plan backup: %w", err)
	}

	err = printPlan(plan)
	if err != nil {
		return fmt.Errorf("failed to print plan: %w", err)
	}

	return nil
}

func getLastFromChain(ctx context.Context, storageUri string) (*backup.ClusterManifest, error) {
	storageCfg, err := backup.ParseStorageURI(storageUri)
	if err != nil {
		return nil, fmt.Errorf("failed to parse storage URI: %w", err)
	}

	store, err := backup.OpenStorage(storageCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open storage: %w", err)
	}

	ch, err := chain.Load(ctx, store)
	if err != nil {
		return nil, fmt.Errorf("failed to build backup chain: %w", err)
	}

	chainLatest := ch.Latest()
	if chainLatest == nil {
		return nil, fmt.Errorf("failed to get latest backup: %w", backup.ErrNoBackups)
	}

	if problems := chainLatest.Problems; len(problems) > 0 {
		return nil, fmt.Errorf(
			"backup plan: latest backup %q has chain problems:"+
				"\n%s\nuse --target=full to start a new chain",
			chainLatest.Manifest.BackupID,
			formatChainProblems(problems),
		)
	}

	return chainLatest.Manifest, nil
}

func printPlan(plan *backup.BackupPlan) error {
	switch backupPlanFormat {
	case formatJSON:
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return fmt.Errorf("failed to marshal plan: %w", err)
		}

		fmt.Println(string(data))
	case formatTable:
		printPlanTable(plan)
	}

	return nil
}

// topologyToLive converts topology output into backup.LiveTopology.
func topologyToLive(topo topologyOutput) backup.LiveTopology {
	live := backup.LiveTopology{
		Replicasets: make(map[string][]backup.LiveInstance, len(topo.Replicasets)),
	}
	for rsUUID, instances := range topo.Replicasets {
		liveInstances := make([]backup.LiveInstance, 0, len(instances))
		for _, inst := range instances {
			liveInstances = append(liveInstances, backup.LiveInstance{
				InstanceUUID: inst.InstanceUUID,
				InstanceName: inst.InstanceName,
				Hostname:     inst.Hostname,
				Mode:         inst.Mode,
				Status:       inst.Status,
			})
		}
		live.Replicasets[rsUUID] = liveInstances
	}
	return live
}

// formatChainProblems renders chain problems as an indented list.
func formatChainProblems(problems []*chain.Problem) string {
	lines := make([]string, 0, len(problems))
	for _, p := range problems {
		prefix := "  "
		if p.Inherited {
			prefix += "[inherited] "
		}
		lines = append(lines, prefix+p.Detail)
	}
	return strings.Join(lines, "\n")
}

// printPlanTable prints the backup plan as a human-readable table.
func printPlanTable(plan *backup.BackupPlan) {
	log.Info("Backup plan")
	log.Infof("  Mode:              %s", plan.Type)
	if plan.PreviousBackupID != "" {
		log.Infof("  Previous backup:   %s", plan.PreviousBackupID)
	}
	if len(plan.Replicasets) > 0 {
		log.Infof("  Replicasets:       %d", len(plan.Replicasets))
		for uuid, rs := range plan.Replicasets {
			log.Infof("    %s", uuid)
			log.Infof("      Master UUID:   %s", rs.MasterInstanceUUID)
			log.Infof("      Master name:   %s", rs.MasterInstanceName)
			if rs.FromVclock != nil {
				log.Infof("      From vclock:   %s", formatVclock(rs.FromVclock))
			}
		}
	}
}

// formatVclock renders a Vclock as {1:1500,2:230} for table output.
func formatVclock(vc backup.Vclock) string {
	parts := make([]string, 0, len(vc))
	for id, lsn := range vc {
		parts = append(parts, fmt.Sprintf("%d:%d", id, lsn))
	}
	return "{" + strings.Join(parts, ",") + "}"
}
