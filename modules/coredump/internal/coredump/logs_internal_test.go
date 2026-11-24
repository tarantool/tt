package coredump

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// TestReadTail pins the cut of a log: the whole of a short one, and the end
// of a long one from the start of a line, the line the limit cuts left out
// but for a single one.
func TestReadTail(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

		return path
	}

	data, cut, err := readTail(write("short", "a\nb\n"), 10)
	require.NoError(t, err)
	assert.Equal(t, "a\nb\n", string(data))
	assert.False(t, cut)

	data, cut, err = readTail(write("exact", "0123456789"), 10)
	require.NoError(t, err)
	assert.Equal(t, "0123456789", string(data))
	assert.False(t, cut)

	// The limit cuts "second": the tail starts at the line after it.
	data, cut, err = readTail(write("long", "first\nsecond\nthird\nfourth\n"), 16)
	require.NoError(t, err)
	assert.Equal(t, "third\nfourth\n", string(data))
	assert.True(t, cut)

	// The limit falls at the start of a line: the line is whole.
	data, cut, err = readTail(write("boundary", "first\nsecond\nthird\n"), 13)
	require.NoError(t, err)
	assert.Equal(t, "second\nthird\n", string(data))
	assert.True(t, cut)

	// A line longer than the limit is cut, rather than lost.
	data, cut, err = readTail(write("one line", strings.Repeat("x", 30)), 10)
	require.NoError(t, err)
	assert.Equal(t, strings.Repeat("x", 10), string(data))
	assert.True(t, cut)

	_, _, err = readTail(filepath.Join(dir, "missing"), 10)
	require.ErrorIs(t, err, os.ErrNotExist)
}

// TestReadTailOfALog cuts a log of more than LogTailBytes to at most that,
// from the start of a line.
func TestReadTailOfALog(t *testing.T) {
	t.Parallel()

	var log strings.Builder

	for i := 0; log.Len() < 3*LogTailBytes; i++ {
		log.WriteString("line " + strconv.Itoa(i) + " of the log of the instance\n")
	}

	path := filepath.Join(t.TempDir(), "tt.log")
	require.NoError(t, os.WriteFile(path, []byte(log.String()), 0o600))

	data, cut, err := readTail(path, LogTailBytes)
	require.NoError(t, err)
	assert.True(t, cut)
	assert.LessOrEqual(t, len(data), LogTailBytes)
	assert.Greater(t, len(data), LogTailBytes-100)
	assert.True(t, strings.HasPrefix(string(data), "line "), string(data[:20]))
	assert.True(t, strings.HasSuffix(log.String(), string(data)))
}

// TestStageLogs stages the tails of the logs of an instance for the pack
// script, as files of their own named in logs/, and leaves out one that is
// not there, saying so.
func TestStageLogs(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ttLog := filepath.Join(dir, "tt.log")
	require.NoError(t, os.WriteFile(ttLog, []byte("crashed\n"), 0o600))

	log, recorder := logtest.New(t)
	staged := t.TempDir()

	args, err := stageLogs(log, staged, []PackLog{
		{Name: "tt.log", Path: ttLog},
		{Name: "tarantool.log", Path: filepath.Join(dir, "tarantool.log")},
	})
	require.NoError(t, err)

	tail := filepath.Join(staged, "logs", "tt.log")
	assert.Equal(t, []string{"-f", "logs/tt.log=" + tail}, args)

	content, err := os.ReadFile(tail)
	require.NoError(t, err)
	assert.Equal(t, "crashed\n", string(content))

	assert.Equal(t, []string{
		"Packing the log " + ttLog + " as logs/tt.log of the archive.",
		"There is no tarantool.log, " + filepath.Join(dir, "tarantool.log") + ": it is not packed.",
	}, recorder.Messages())

	args, err = stageLogs(log, staged, nil)
	require.NoError(t, err)
	assert.Empty(t, args)
}

// TestPackLogs packs a core of a tarantool cc built with the logs of its
// instance: the tail of a long tt.log and the whole of a short
// tarantool.log go into logs/ of the archive, which unpack keeps, and inspect
// says where they are.
func TestPackLogs(t *testing.T) {
	t.Parallel()
	requirePackScript(t)

	dir := t.TempDir()
	tarantool := compileTarantool(t, dir)
	core := binaryCore(t, tarantool, 0x560000000000)

	ttLog := filepath.Join(dir, "tt.log")
	tarantoolLog := filepath.Join(dir, "tarantool.log")
	// Lines of 100 bytes: the one the tail cuts, left out, is up to 99, and
	// what is packed is 1023.9 KiB or less, the lines of the last MiB.
	line := "an old line of the log" + strings.Repeat(".", 77) + "\n"
	long := strings.Repeat(line, LogTailBytes/64) + "the last line\n"

	require.NoError(t, os.WriteFile(ttLog, []byte(long), 0o600))
	require.NoError(t, os.WriteFile(tarantoolLog, []byte("F> crashed\n"), 0o600))

	log, recorder := logtest.New(t)
	out := t.TempDir()

	archive, _, err := Pack(t.Context(), output.Streams{In: nil, Out: io.Discard, Err: io.Discard},
		log, PackOptions{
			Core: core.write(t, filepath.Join(dir, "core")), Executable: tarantool, Dir: out,
			PID: 4242, Time: "1763869440", Logs: []PackLog{
				{Name: "tt.log", Path: ttLog}, {Name: "tarantool.log", Path: tarantoolLog},
			},
		})
	require.NoError(t, err)

	members := archiveMembers(t, archive)
	assert.Equal(t, "F> crashed\n", members["logs/tarantool.log"])
	assert.LessOrEqual(t, len(members["logs/tt.log"]), LogTailBytes)
	assert.True(t, strings.HasSuffix(members["logs/tt.log"], "the last line\n"))
	assert.True(t, strings.HasPrefix(members["logs/tt.log"], "an old line"))
	assert.NotContains(t, members, "logs", "the directory alone is no member")

	messages := recorder.Messages()
	assert.Contains(t, messages, "Packing the log "+tarantoolLog+
		" as logs/tarantool.log of the archive.")
	assert.Less(t, len(members["logs/tt.log"]), LogTailBytes-51, "a cut line of over 51 bytes")
	assert.Contains(t, messages, "Packing the lines of the last 1.0 MiB of the log "+ttLog+
		" as logs/tt.log of the archive.")

	// tt coredump unpack keeps them, and inspect says where they are.
	unpacked := t.TempDir()
	require.NoError(t, extractTarGz(archive, unpacked))

	name, err := unpackedDir(unpacked)
	require.NoError(t, err)

	artefacts := filepath.Join(unpacked, name)
	content, err := os.ReadFile(filepath.Join(artefacts, "logs", "tarantool.log"))
	require.NoError(t, err)
	assert.Equal(t, "F> crashed\n", string(content))

	log, recorder = logtest.New(t)

	noteLogs(log, artefacts, artefacts, "")
	noteLogs(log, artefacts, archive, name)
	noteLogs(log, dir, dir, "")
	assert.Equal(t, []string{
		"The logs of the instance of the core are in " + filepath.Join(artefacts, "logs") +
			": tarantool.log and tt.log.",
		"The archive has the logs of the instance of the core in logs/, tarantool.log and " +
			"tt.log: tt coredump unpack " + archive + " puts them into " +
			filepath.Join(name, "logs") + ".",
	}, recorder.Messages(), "artefacts with no logs/ are not told of")
}
