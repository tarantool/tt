package coredump

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
)

const (
	// LogTailBytes is how much of the end of a log of an instance pack puts
	// into the archive: what the instance logged before it crashed, and not
	// the log of its whole life.
	LogTailBytes = 1 << 20
	// logsDir is the directory of the archive the tails of the logs go
	// into.
	logsDir = "logs"
	// logFileMode is the mode of the tail of a log pack writes: a log may
	// tell what the instance worked with.
	logFileMode = 0o600
)

// PackLog is a log pack puts the tail of into the archive, under logs/.
type PackLog struct {
	// Name is the name of the tail in logs/ of the archive, tt.log or
	// tarantool.log.
	Name string
	// Path is the log.
	Path string
}

// readTail returns the last limit bytes of the file at path, from the start
// of a line, and whether that is less than the whole file: the line the
// limit cuts is left out, unless it is the only one.
func readTail(path string, limit int64) ([]byte, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, false, fmt.Errorf("cannot read the log: %w", err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return nil, false, fmt.Errorf("cannot read the log: %w", err)
	}

	offset := max(info.Size()-limit, 0)

	data, err := io.ReadAll(io.NewSectionReader(file, offset, info.Size()-offset))
	if err != nil {
		return nil, false, fmt.Errorf("cannot read the log %s: %w", path, err)
	}

	if offset == 0 {
		return data, false, nil
	}

	// The first line read is cut: it starts before the tail, unless the
	// byte before the tail ends a line.
	var before [1]byte

	_, err = file.ReadAt(before[:], offset-1)
	if err == nil && before[0] == '\n' {
		return data, true, nil
	}

	_, rest, found := bytes.Cut(data, []byte("\n"))
	if found && len(rest) > 0 {
		return rest, true, nil
	}

	return data, true, nil
}

// noteLogs says, before gdb starts, where the logs of the instance of the
// core are, when the artefacts in dir have them, unpacked from the archive
// at path into unpacked, relative to the working directory as tt coredump
// unpack puts them, or the directory path itself, unpacked empty: inspect
// unpacks an archive into a directory it removes.
func noteLogs(log *slog.Logger, dir, path, unpacked string) {
	entries, err := os.ReadDir(filepath.Join(dir, logsDir))
	if err != nil || len(entries) == 0 {
		return
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}

	if unpacked == "" {
		log.Info(fmt.Sprintf("The logs of the instance of the core are in %s: %s.",
			filepath.Join(path, logsDir), joinAnd(names)))

		return
	}

	log.Info(fmt.Sprintf("The archive has the logs of the instance of the core in %s/, %s: "+
		"tt coredump unpack %s puts them into %s.", logsDir, joinAnd(names), path,
		filepath.Join(unpacked, logsDir)))
}

// stageLogs writes the tails of logs into logs/ of dir and returns the -f
// arguments of the pack script that put them into logs/ of the archive. It
// logs to log what of each it packs, and warns of a log it cannot read,
// which is left out: the core is packed all the same.
func stageLogs(log *slog.Logger, dir string, logs []PackLog) ([]string, error) {
	if len(logs) == 0 {
		return nil, nil
	}

	staged := filepath.Join(dir, logsDir)

	err := os.MkdirAll(staged, dirMode)
	if err != nil {
		return nil, fmt.Errorf("cannot stage the logs: %w", err)
	}

	var args []string

	for _, packed := range logs {
		data, cut, err := readTail(packed.Path, LogTailBytes)
		if errors.Is(err, fs.ErrNotExist) {
			log.Info(fmt.Sprintf("There is no %s, %s: it is not packed.", packed.Name, packed.Path))

			continue
		}

		if err != nil {
			log.Warn(fmt.Sprintf("%v: it is not packed.", err))

			continue
		}

		tail := filepath.Join(staged, packed.Name)

		err = os.WriteFile(tail, data, logFileMode)
		if err != nil {
			return nil, fmt.Errorf("cannot stage the log %s: %w", packed.Path, err)
		}

		member := path.Join(logsDir, packed.Name)

		args = append(args, "-f", member+"="+tail)

		// The tail is the lines of the last LogTailBytes, the line the limit
		// cuts left out: its size is of the log, as the limit, 1.0 MiB.
		what := "the log " + packed.Path
		if cut {
			what = fmt.Sprintf("the lines of the last %s of the log %s",
				FormatSize(LogTailBytes), packed.Path)
		}

		log.Info(fmt.Sprintf("Packing %s as %s of the archive.", what, member))
	}

	return args, nil
}
