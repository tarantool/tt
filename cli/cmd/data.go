package cmd

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/go-tarantool/v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/dial"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/data"
	"github.com/tarantool/tt/v3/cli/printing"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errDataNoFiles  = errors.New("specify at least one .snap, .xlog or .vylog file or directory")
	errDataNoTarget = errors.New(
		"specify a target and at least one .snap or .xlog file or directory")
	errDataAllRefused = errors.New("the target refused every transaction")
	errDataUnknown    = errors.New("unknown space")
)

// dataFilterHelp is the part of the help of tt data cat and tt data play on
// what of the files they read.
const dataFilterHelp = `The files are read the way Tarantool recovers: the newest snapshot, then the
write-ahead logs in the order of their vclocks, without the transactions of
its instance the snapshot already contains. A snapshot holds no data of vinyl
spaces: it is in .run files, which are not read. A directory gives its .snap
and .xlog files (-r: of its subdirectories too); a .vylog file is read when it
is named. A write-ahead log an instance is still writing is read as far as it
goes.

Bounds select whole transactions: a transaction is taken when any of its rows
is within them, so a bound inside a transaction takes all of it. They apply to
write-ahead logs; a snapshot is taken whole - the newest one within the upper
bounds - and none is taken with a lower bound, which asks for changes, not for
a state.
  --from N, --to N         the LSNs of one replica, both ends included: the
                           replica --replica names, or the only one the files
                           have rows of. A transaction with no row of it is
                           not taken.
  --from-vclock V          the transactions past V: with a row whose LSN is
                           above that of its replica in V.
  --to-vclock V            the transactions within V: with a row whose LSN is
                           at most that of its replica in V. A replica V does
                           not name, 0 (local spaces) included, has LSN 0.
  --timestamp T            the transactions written before T: Unix seconds or
                           RFC 3339, such as 2024-11-14T14:02:36.818+00:00.
V is {1: 100, 2: 50}, as box.info.vclock prints it, or {"1": 100, "2": 50}.

Of the transactions taken, only the changes of user spaces are kept: the rows
of system spaces and those that change no space - RAFT, synchronous
replication, NOP - only with --show-system. --space keeps the rows of the
spaces it names, system ones included: by id, or by name when the snapshot
read defines it. --replica keeps the rows of write-ahead logs the replicas it
names wrote.`

// dataFilterFlags are the flags of what tt data cat and play read.
type dataFilterFlags struct {
	spaces      []string
	replicas    []uint
	from, to    uint64
	fromVClock  string
	toVClock    string
	timestamp   string
	showSystem  bool
	recursive   bool
	skipCorrupt bool
}

// NewDataCmd creates tt data, the commands of Tarantool journals.
func NewDataCmd() *cobra.Command {
	dataCmd := &cobra.Command{
		Use:   "data",
		Short: "Read and replay snapshots and write-ahead logs",
		Long: `Read and replay Tarantool journals - snapshots (.snap), write-ahead logs
(.xlog) and vinyl logs (.vylog) - without Tarantool.

  cat   prints the rows of the files as YAML, JSON Lines or Lua.
  play  applies the data changes of the files to an instance.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}

	dataCmd.AddCommand(newDataCatCmd(), newDataPlayCmd())

	return dataCmd
}

// addDataFilterFlags declares the flags of what to read on cmd.
func addDataFilterFlags(cmd *cobra.Command, flags *dataFilterFlags) {
	cmd.Flags().StringSliceVar(&flags.spaces, "space", nil,
		"keep the rows of a space, by id or by name; may be repeated")
	cmd.Flags().UintSliceVar(&flags.replicas, "replica", nil,
		"keep the rows a replica wrote, by id; may be repeated")
	cmd.Flags().Uint64Var(&flags.from, "from", 0,
		"take the transactions from this LSN of the replica on, inclusive")
	cmd.Flags().Uint64Var(&flags.to, "to", 0,
		"take the transactions up to this LSN of the replica, inclusive")
	cmd.Flags().StringVar(&flags.fromVClock, "from-vclock", "",
		"take the transactions past this vclock")
	cmd.Flags().StringVar(&flags.toVClock, "to-vclock", "",
		"take the transactions within this vclock")
	cmd.Flags().StringVar(&flags.timestamp, "timestamp", "",
		"take the transactions written before this time")
	cmd.Flags().BoolVar(&flags.showSystem, "show-system", false,
		"keep the rows of system spaces and those that change no space")
	cmd.Flags().BoolVarP(&flags.recursive, "recursive", "r", false,
		"read the files of the subdirectories of a directory too")
	cmd.Flags().BoolVar(&flags.skipCorrupt, "skip-corrupt", false,
		"skip a block of transactions whose checksum does not match instead of failing")
}

// dataFilter returns the filter the flags of cmd ask for, its spaces still
// given by name, and the files of paths it reads.
func dataFilter(cmd *cobra.Command, flags *dataFilterFlags, paths []string) (
	*data.Filter, *data.Plan, error,
) {
	filter := &data.Filter{
		From: int64(flags.from), To: int64(flags.to), //nolint:gosec // An LSN fits int64.
		HasFrom:    cmd.Flags().Changed("from"),
		HasTo:      cmd.Flags().Changed("to"),
		ShowSystem: flags.showSystem,
	}

	for _, replica := range flags.replicas {
		filter.Replicas = append(filter.Replicas, uint32(replica)) //nolint:gosec // An id.
	}

	var err error

	if flags.fromVClock != "" {
		filter.FromVClock, err = data.ParseVClock(flags.fromVClock)
		if err != nil {
			return nil, nil, util.NewArgError("--from-vclock: " + err.Error())
		}
	}

	if flags.toVClock != "" {
		filter.ToVClock, err = data.ParseVClock(flags.toVClock)
		if err != nil {
			return nil, nil, util.NewArgError("--to-vclock: " + err.Error())
		}
	}

	if flags.timestamp != "" {
		timestamp, err := util.StringToTimestamp(flags.timestamp)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse a timestamp: %w", err)
		}

		filter.Before, err = strconv.ParseFloat(timestamp, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse a timestamp: %w", err)
		}
	}

	err = filter.Check()
	if err != nil {
		return nil, nil, util.NewArgError(err.Error())
	}

	files, err := data.Collect(paths, flags.recursive)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // It names the file.
	}

	plan, err := data.Select(files, filter)
	if err != nil {
		return nil, nil, err //nolint:wrapcheck // It says what is wrong.
	}

	return filter, plan, nil
}

// resolveDataSpaces sets the spaces of filter from --space: an id as it is, a
// name by the snapshot of plan, or by lookup when the snapshot does not
// define it.
func resolveDataSpaces(filter *data.Filter, plan *data.Plan, spaces []string,
	lookup func(string) (uint32, bool),
) error {
	if len(spaces) == 0 {
		return nil
	}

	var defined map[string]uint32

	for _, space := range spaces {
		id, err := strconv.ParseUint(space, 10, 32)
		if err == nil {
			filter.Spaces = append(filter.Spaces, uint32(id))

			continue
		}

		if defined == nil {
			defined, err = data.SpaceIDs(plan)
			if err != nil {
				return err //nolint:wrapcheck // It names the file.
			}
		}

		if id, ok := defined[space]; ok {
			filter.Spaces = append(filter.Spaces, id)

			continue
		}

		if lookup != nil {
			if id, ok := lookup(space); ok {
				filter.Spaces = append(filter.Spaces, id)

				continue
			}
		}

		where := "the files read have no snapshot that defines it"
		if plan.Base != nil {
			where = "the snapshot " + plan.Base.Path + " does not define it"
		}

		if lookup != nil {
			where += ", and neither does the target"
		}

		return util.NewArgError(fmt.Sprintf("--space %s: %s: %s; give the id of the space",
			space, errDataUnknown, where))
	}

	return nil
}

func newDataCatCmd() *cobra.Command {
	var (
		flags  dataFilterFlags
		format *output.FormatFlag
	)

	cmd := &cobra.Command{
		Use:   "cat <FILE|DIR>... [flags]",
		Short: "Print the rows of snapshots and write-ahead logs",
		Long: `Print the rows of snapshots, write-ahead logs and vinyl logs to stdout. No
Tarantool is needed.

` + dataFilterHelp + `

-o yaml, the default, prints a YAML document per row; -o json a JSON object per
line (JSON Lines). Both have the fields of the xlog module of Tarantool:
  HEADER  type (INSERT, REPLACE, UPDATE, DELETE, UPSERT, RAFT, RAFT_PROMOTE,
          RAFT_CONFIRM, NOP, ...), lsn (a row number in a snapshot) and
          replica_id; group_id and timestamp when they are set; tsn, the LSN
          of the first row, on the rows of a transaction of several, and
          commit: true on its last row.
  BODY    the body of the row by key: space_id, index_id, key, tuple,
          operations, ... for a data change; replica_id, lsn, term, vclock for
          synchronous replication; term, vote, state, vclock, leader_id for
          RAFT; type and the keys of the record for a vinyl log. None for NOP.
In JSON a value with no JSON type is a string: a decimal, a UUID, a datetime in
RFC 3339, an interval, a binary value in base64. -o lua prints each data change
as a statement of the box API, such as box.space[512]:replace({1, 'a'}), that
makes it in Tarantool.`,
		Example: `# Print a snapshot and the write-ahead logs after it.
  $ tt data cat var/lib/myapp/instance-001

  # Print the changes of space 512 from LSN 100 to 200 as JSON Lines.
  $ tt data cat 00000000000000000000.xlog --space 512 --from 100 --to 200 -o json

  # Print the changes of two replicas up to a vclock as Lua.
  $ tt data cat ./wal --to-vclock '{1: 1500, 2: 300}' -o lua

  # Print the changes made before a time, the rows of system spaces too.
  $ tt data cat -r ./backups --timestamp 2024-11-14T14:02:36Z --show-system`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return errDataNoFiles
			}

			return nil
		},
	}

	format = output.BindFormat(cmd.Flags(), printing.FormatYAML,
		printing.FormatYAML, output.FormatJSON, data.FormatLua)
	addDataFilterFlags(cmd, &flags)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmdCtx.CommandName = cmd.Name()

		return commandError(cmd, runDataCat(dataCatRun{cmd: cmd, flags: &flags, format: format},
			args))
	}

	return cmd
}

// dataCatRun is a run of tt data cat.
type dataCatRun struct {
	cmd    *cobra.Command
	flags  *dataFilterFlags
	format *output.FormatFlag
}

func runDataCat(run dataCatRun, args []string) error {
	filter, plan, err := dataFilter(run.cmd, run.flags, args)
	if err != nil {
		return err
	}

	err = resolveDataSpaces(filter, plan, run.flags.spaces, nil)
	if err != nil {
		return err
	}

	options := append(printing.Options(),
		output.WithEncoder(data.FormatLua, data.EncodeLua),
		output.WithStreamEncoder(data.FormatLua, data.NewLuaStream))

	printer, err := output.NewPrinter(output.StdStreams(), run.format.Format(), options...)
	if err != nil {
		return fmt.Errorf("output: %w", err)
	}

	stream, err := printer.Stream()
	if err != nil {
		return fmt.Errorf("output: %w", err)
	}

	printed, err := data.Cat(plan, filter, data.CatOptions{
		ScanOptions: data.ScanOptions{SkipCorrupt: run.flags.skipCorrupt},
	}, stream)

	closeErr := stream.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return err //nolint:wrapcheck // It names the file and the row.
	}

	if printed == 0 {
		log.Infof("No rows match")
	}

	return nil
}

// dataPlayFlags are the flags of tt data play beside those of what to read.
type dataPlayFlags struct {
	username, password string
	connectCtx         connect.ConnectCtx
	txnMode            string
	onError            string
	batch              int
	dryRun             bool
}

func newDataPlayCmd() *cobra.Command {
	var (
		flags dataFilterFlags
		play  dataPlayFlags
	)

	cmd := &cobra.Command{
		Use:   "play (<APP>:<INSTANCE> | <URI>) <FILE|DIR>... [flags]",
		Short: "Apply the data changes of snapshots and write-ahead logs to an instance",
		Long: `Apply the data changes of snapshots and write-ahead logs to an instance, over
its binary port, transaction by transaction, in their order. No local
Tarantool is needed. The target is an instance of an application, reached at
its binary port, or a URI: [tcp://][username:password@]host:port or
[unix://][username:password@]socketpath, with the TLS settings in the flags or
in the query: ?ssl_key_file=...&ssl_cert_file=...&ssl_ca_file=...

` + dataFilterHelp + `

Only data changes are applied: INSERT, REPLACE, UPDATE, DELETE and UPSERT, of
system spaces too with --show-system. The requests go in one IPROTO stream,
which the instance processes in the order they were sent, --batch of them in
flight at a time. --txn-mode says how a transaction of several rows is applied:
  auto    in a transaction of the stream, and row by row from the first
          transaction the target cannot take so: a transaction that spans
          requests cannot change a memtx space unless memtx_use_mvcc_engine
          is enabled. The default.
  stream  in a transaction of the stream; a target that cannot take it is an
          error.
  row     row by row. A reader of the target may see a transaction half
          applied, and a refused row leaves the others of its transaction
          applied.
A transaction of one row is a request of its own in every mode.

--on-error says what to do when the target refuses a row, with the server's
error, the LSN and the replica of the row:
  stop    stop, the default. The requests already in flight - up to --batch -
          are still applied.
  skip    report the transaction and go on.
--dry-run reads and filters the files, checks that the target has the spaces
the rows change and applies nothing. A summary of what was applied ends the
run. Exit codes: 0 - every transaction was applied; 3 - some were, and the
target refused others; 1 - the target refused the first one it was given, or
the request is invalid; 2 - the connection failed.

` + "The command supports the TT_CLI_USERNAME and TT_CLI_PASSWORD environment\n" +
			"variables for a URI without credentials.",
		Example: `# Replay a snapshot and the write-ahead logs after it into an instance.
  $ tt data play myapp:instance-001 ./backup

  # Replay the changes of a space past a vclock, skipping the refused ones.
  $ tt data play admin:secret@localhost:3301 ./wal --from-vclock '{1: 1500}' \
      --space 512 --on-error skip

  # Check what a replay up to a time would change, applying nothing.
  $ tt data play localhost:3301 ./wal --timestamp 2024-11-14T14:02:36Z --dry-run`,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) < 2 { //nolint:mnd // A target and a file.
				return errDataNoTarget
			}

			return nil
		},
	}

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		cmdCtx.CommandName = cmd.Name()

		return commandError(cmd, runDataPlay(cmd, &flags, &play, args))
	}

	addInstanceFlags(cmd)
	addDataFilterFlags(cmd, &flags)

	cmd.Flags().StringVarP(&play.username, "username", "u", "", "username")
	cmd.Flags().StringVarP(&play.password, "password", "p", "", "password")
	cmd.Flags().StringVar(&play.connectCtx.SslKeyFile, "sslkeyfile", "",
		"path to a private SSL key file")
	cmd.Flags().StringVar(&play.connectCtx.SslCertFile, "sslcertfile", "",
		"path to an SSL certificate file")
	cmd.Flags().StringVar(&play.connectCtx.SslCaFile, "sslcafile", "",
		"path to a trusted certificate authorities (CA) file")
	cmd.Flags().StringVar(&play.connectCtx.SslCiphers, "sslciphers", "",
		"colon-separated (:) list of SSL cipher suites the connection can use")
	cmd.Flags().StringVar(&play.connectCtx.SslPassword, "sslpassword", "",
		"password for decrypting the private SSL key file")
	cmd.Flags().StringVar(&play.connectCtx.SslPasswordFile, "sslpasswordfile", "",
		"path to a file containing the password for decrypting the private SSL key file")
	cmd.Flags().DurationVar(&play.connectCtx.ConnectTimeout, "connect-timeout",
		connector.DefaultConnectTimeout, "timeout for establishing the connection")
	cmd.Flags().StringVar(&play.txnMode, "txn-mode", string(data.TxnAuto),
		"how a transaction of several rows is applied: "+joinModes(data.TxnModes))
	cmd.Flags().StringVar(&play.onError, "on-error", string(data.OnErrorStop),
		"what to do when the target refuses a row: "+joinModes(data.OnErrors))
	cmd.Flags().IntVar(&play.batch, "batch", data.DefaultBatch,
		"how many requests are in flight at most; 1 waits for each")
	cmd.Flags().BoolVar(&play.dryRun, "dry-run", false,
		"check the files and the spaces of the target, and apply nothing")

	return cmd
}

// joinModes lists the values of a flag for its help.
func joinModes[T ~string](values []T) string {
	names := make([]string, 0, len(values))
	for _, value := range values {
		names = append(names, string(value))
	}

	return strings.Join(names, ", ")
}

func runDataPlay(cmd *cobra.Command, flags *dataFilterFlags, play *dataPlayFlags,
	args []string,
) error {
	txnMode := data.TxnMode(play.txnMode)
	if !slices.Contains(data.TxnModes, txnMode) {
		return util.NewArgError(fmt.Sprintf("--txn-mode %q: want %s", play.txnMode,
			joinModes(data.TxnModes)))
	}

	onError := data.OnError(play.onError)
	if !slices.Contains(data.OnErrors, onError) {
		return util.NewArgError(fmt.Sprintf("--on-error %q: want %s", play.onError,
			joinModes(data.OnErrors)))
	}

	if play.batch < 1 {
		return util.NewArgError("--batch must be at least 1")
	}

	filter, plan, err := dataFilter(cmd, flags, args[1:])
	if err != nil {
		return err
	}

	conn, err := dataPlayConnect(cmd.Context(), play, args[0])
	if err != nil {
		return err
	}

	defer func() { _ = conn.Close() }()

	target, err := data.NewTarget(conn)
	if err != nil {
		return sdk.WithCode(sdk.ExitSystem, err)
	}

	var spaces map[uint32]string

	if play.dryRun || len(flags.spaces) > 0 {
		spaces, err = target.Spaces()
		if err != nil {
			return sdk.WithCode(sdk.ExitSystem, err)
		}
	}

	err = resolveDataSpaces(filter, plan, flags.spaces, func(name string) (uint32, bool) {
		for id, space := range spaces {
			if space == name {
				return id, true
			}
		}

		return 0, false
	})
	if err != nil {
		return err
	}

	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}

	summary, err := data.Play(ctx, target, plan, filter, data.PlayOptions{
		ScanOptions: data.ScanOptions{SkipCorrupt: flags.skipCorrupt},
		TxnMode:     txnMode, OnError: onError, Batch: play.batch,
		DryRun: play.dryRun, Spaces: spaces,
	})

	if summary != nil {
		reportDataPlay(summary, play.dryRun)
	}

	return dataPlayError(summary, err)
}

// dataPlayConnect connects to the target of tt data play: the binary port of
// an instance of an application, or a URI.
func dataPlayConnect(ctx context.Context, play *dataPlayFlags, target string) (
	*tarantool.Connection, error,
) {
	connectCtx := play.connectCtx
	connectCtx.Username = play.username
	connectCtx.Password = play.password
	connectCtx.Binary = true
	connectCtx.Transport = dial.TransportDefault

	// An instance of an application is reached at its binary port, which
	// it opens only when it is started with one.
	instances, resolveErr := resolveInstances(&cmdCtx, []string{target})
	if resolveErr == nil && len(instances) == 1 {
		err := running.RequireTarantool(&instances[0])
		if err != nil {
			return nil, err //nolint:wrapcheck // It names the instance.
		}

		_, err = os.Stat(instances[0].BinaryPort)
		if err != nil {
			return nil, fmt.Errorf("the binary port of %s does not exist: %w", target, err)
		}
	}

	opts, err := resolveConnectOpts(&cmdCtx, &connectCtx, target)
	if err != nil {
		return nil, err
	}

	if ctx == nil {
		ctx = context.Background()
	}

	conn, err := connector.ConnectIproto(ctx, opts, data.TargetOpts())
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", log.RedactTarget(target), err)
	}

	return conn, nil
}

// reportDataPlay logs the summary of a play.
func reportDataPlay(summary *data.Summary, dryRun bool) {
	if dryRun {
		log.Infof("Dry run: %d transactions with %d data changes would be applied",
			summary.Txns, summary.Rows)

		for _, id := range slices.Sorted(maps.Keys(summary.Missing)) {
			log.Errorf("The target has no space %d, which %d rows change", id,
				summary.Missing[id])
		}

		return
	}

	log.Infof("Applied %d of %d transactions, %d of %d data changes", summary.TxnsApplied,
		summary.Txns, summary.RowsApplied, summary.Rows)

	if summary.TxnsFailed > 0 {
		log.Warnf("The target refused %d transactions: %d rows refused, %d rolled back "+
			"with them", summary.TxnsFailed, summary.RowsFailed, summary.RowsSkipped)
	}
}

// dataPlayError returns the error tt data play ends with, with the exit code
// of what it did.
func dataPlayError(summary *data.Summary, err error) error {
	applied := summary != nil && summary.TxnsApplied > 0

	switch {
	case errors.Is(err, data.ErrRefused) && applied:
		return sdk.WithCode(sdk.ExitPartial, err)
	case errors.Is(err, data.ErrRefused), errors.Is(err, data.ErrMissingSpaces):
		return err //nolint:wrapcheck // It says what was refused.
	case err != nil:
		var serverErr tarantool.ServerError
		if errors.As(err, &serverErr) {
			return err //nolint:wrapcheck // It names the row.
		}

		if errors.Is(err, data.ErrAmbiguousReplica) || errors.Is(err, data.ErrNoTransactions) {
			return err //nolint:wrapcheck // The request is to fix.
		}

		return sdk.WithCode(sdk.ExitSystem, err)
	case summary != nil && summary.TxnsFailed > 0 && applied:
		return sdk.Errorf(sdk.ExitPartial, "the target refused %d of %d transactions",
			summary.TxnsFailed, summary.Txns)
	case summary != nil && summary.TxnsFailed > 0:
		return errDataAllRefused
	}

	return nil
}
