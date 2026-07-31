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
	"github.com/tarantool/tt/cli/backup/verify"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

// tt backup start / finalize / last / verify flags. They are package-level because
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
)

// backupStorageURIHelp documents the --backup-storage flag value for every
// manager-side backup command.
const backupStorageURIHelp = `The --backup-storage flag accepts a URI describing the
storage backend:

  file://<abs_path>?Prefix=<subdir>
      Local filesystem storage. The path must be absolute.
      Query parameters:
        Prefix  - subdirectory within the path (optional).

  s3+https://endpoint:port/bucket/prefix?Region=...&AccessKeyID=...&SecretAccessKey=...
  s3+http://endpoint:port/bucket/prefix?Region=...&AccessKeyID=...&SecretAccessKey=...
      S3-compatible storage. Use s3+https for TLS, s3+http for plain TCP.
      The first path segment after the host is the bucket name, the rest is
      the optional key prefix.
      Query parameters:
        Region           - AWS region (optional).
        AccessKeyID      - access key ID (required).
        SecretAccessKey  - secret access key (required).`

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
	)

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
		"backup identifier; local artifacts of the target replicaset are removed")
	cmd.MarkFlagRequired("backup-id")

	return cmd
}

func newBackupLastCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "last",
		Short: "Show the last backup manifest from the storage",
		Long: `Find the latest manifest in the storage and print it to stdout.

Usage:
  tt backup last --backup-storage=<uri> [--format <table|json>]

` + backupStorageURIHelp + `

Examples:
  tt backup last --backup-storage=file:///var/backups
  tt backup last --backup-storage=file:///var/backups?Prefix=mycluster
  tt backup last --backup-storage=s3+https://s3.example.com:9000/... \
    ?Region=us-east-1&AccessKeyID=minio&SecretAccessKey=minio123
  tt backup last --backup-storage=file:///var/backups --format json`,
		Args: cobra.NoArgs,
		RunE: runBackupLast,
	}

	cmd.Flags().StringVar(&backupStorageConfig, "backup-storage", "",
		"storage URI (file://<path> or s3+http(s)://host:port/bucket/prefix?...")
	cmd.Flags().StringVar(&backupLastFormat, "format", formatTable,
		"output format: table or json")
	cmd.Flags().DurationVar(&backupLastTimeout, "timeout", time.Minute,
		"timeout for connecting to and reading from the storage")

	cmd.MarkFlagRequired("backup-storage")

	return cmd
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

	ctx, cancel := context.WithTimeout(context.Background(), backupLastTimeout)
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
	log.Infof("  Duration:         %s", manifest.CreationDuration)
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

Usage:
  tt backup verify --backup-storage=<uri> [--format <table|json>]

The check reads the whole storage and reports:

  - manifests referring to archives that are not stored;
  - archives whose bytes do not match checksum_sha256 of the manifest;
  - breaks in the previous_backup_id chain (orphans and forks);
  - increments whose vclock_begin does not continue the previous backup;
  - archives no manifest refers to.

Nothing is ever deleted or repaired: removing backups is 'tt backup gc'.
Exit codes: 0 - the storage is healthy, 2 - problems were found, 1 - the
storage could not be checked.

` + backupStorageURIHelp + `

Examples:
  tt backup verify --backup-storage=file:///var/backups
  tt backup verify --backup-storage=file:///var/backups --format json`,
		Args: cobra.NoArgs,
		RunE: runBackupVerify,
	}

	cmd.Flags().StringVar(&backupStorageConfig, "backup-storage", "",
		"storage URI (file://<path> or s3+http(s)://host:port/bucket/prefix?...")
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

	// A zero timeout means no limit, not "already expired": a storage large
	// enough to outlast any sensible deadline still has to be checkable.
	ctx := context.Background()
	if backupVerifyTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, backupVerifyTimeout)
		defer cancel()
	}

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

	if report.OK() {
		log.Info("  Problems:          none")
		return
	}

	log.Infof("  Problems:          %d", len(report.Issues))

	for _, issue := range report.Issues {
		inherited := ""
		if issue.Inherited {
			inherited = " (inherited)"
		}

		log.Warnf("  [%s]%s %s: %s",
			issue.Kind, inherited, verifyIssueTarget(issue), issue.Detail)
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
		return "", fmt.Errorf("invalid flag: %w", util.NewArgError(err.Error()))
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

	return vc, nil
}
