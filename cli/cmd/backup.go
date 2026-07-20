package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/apex/log"
	"github.com/spf13/cobra"
	"github.com/tarantool/tt/cli/backup"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/connect"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/running"
	"github.com/tarantool/tt/cli/util"
)

// tt backup start / finalize flags. They are package-level because cobra flag
// bindings need stable addresses; only one backup subcommand runs per process.
var (
	backupStartCfg        string
	backupStartID         string
	backupStartFromVclock string
	backupStartTTL        time.Duration

	backupFinalizeCfg string
	backupFinalizeID  string
)

// NewBackupCmd creates the parent `tt backup` command.
func NewBackupCmd() *cobra.Command {
	backupCmd := &cobra.Command{
		Use:   "backup",
		Short: "Manage Tarantool backups (PITR)",
	}
	backupCmd.AddCommand(
		newBackupStartCmd(),
		newBackupFinalizeCmd(),
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
archive. Idempotent: if the backup is already closed, it does not fail — so
'ATE backup --force' can wipe a stuck state before a new start.`,
		Args: cobra.ExactArgs(1),
		RunE: runBackupFinalize,
	}

	cmd.Flags().StringVarP(&backupFinalizeCfg, "config", "c", "",
		"path to the cluster configuration file (for <APP:INSTANCE>)")
	cmd.Flags().StringVar(&backupFinalizeID, "backup-id", "",
		"backup identifier; the whole /tmp/tt-backup/<backup-id>/ directory is removed")
	cmd.MarkFlagRequired("backup-id")

	return cmd
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
