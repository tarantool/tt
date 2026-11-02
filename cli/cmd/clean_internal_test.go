package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/running"
)

func TestCleanVerboseDirectoryBoundaries(t *testing.T) {
	for _, trailingSeparator := range []bool{false, true} {
		t.Run(fmt.Sprintf("trailing separator %t", trailingSeparator), func(t *testing.T) {
			root := t.TempDir()
			dataDir := filepath.Join(root, "data")
			otherDir := filepath.Join(root, "data-extra")
			first := filepath.Join(dataDir, "first")
			second := filepath.Join(otherDir, "second")

			for _, file := range []string{first, second} {
				err := os.MkdirAll(filepath.Dir(file), 0o755)
				require.NoError(t, err)

				err = os.WriteFile(file, []byte("data"), 0o600)
				require.NoError(t, err)
			}

			inputPath := filepath.Join(root, "stdin")
			err := os.WriteFile(inputPath, []byte("n\n"), 0o600)
			require.NoError(t, err)

			input, err := os.Open(inputPath)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, input.Close()) })

			outputPath := filepath.Join(root, "stdout")
			output, err := os.Create(outputPath)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, output.Close()) })

			oldInput, oldOutput := os.Stdin, os.Stdout
			oldVerbose := verboseList

			t.Cleanup(func() {
				os.Stdin, os.Stdout = oldInput, oldOutput
				verboseList = oldVerbose
			})

			os.Stdin, os.Stdout = input, output
			verboseList = true

			if trailingSeparator {
				dataDir += string(filepath.Separator)
			}

			total, err := previewClean([]running.InstanceCtx{{
				LogDir: dataDir, WalDir: otherDir, VinylDir: otherDir, MemtxDir: otherDir,
			}})
			require.NoError(t, err)
			assert.Equal(t, 2, total)

			data, err := os.ReadFile(outputPath)
			require.NoError(t, err)

			text := string(data)
			assert.Contains(t, text, "Total files to remove: 2")

			var listed []string

			for line := range strings.SplitSeq(text, "\n") {
				if strings.HasPrefix(line, "to remove \"") {
					listed = append(listed, line)
				}
			}

			expected := make([]string, 0, 2)

			for dir, name := range map[string]string{dataDir: "first", otherDir: "second"} {
				line := fmt.Sprintf("to remove %q", "."+string(filepath.Separator)+name)

				expected = append(expected, line)
				assert.Contains(t, text, fmt.Sprintf("to remove 1 file(s) in %q\n%s", dir, line))
			}

			assert.ElementsMatch(t, expected, listed)
			assert.FileExists(t, first)
			assert.FileExists(t, second)
		})
	}
}

// TestCleanStale pins that tt instance clean takes an instance whose pid
// file nobody holds for stopped, the file a watchdog that died without
// cleaning up left: it previews and cleans it, and removes the file, which
// names an unrelated live process that gets no signal. An instance whose
// watchdog holds its pid file is left alone.
//
//nolint:paralleltest // Sets the environment, the working directory and the flags.
func TestCleanStale(t *testing.T) {
	configlessProject(t, "groups:\n  g:\n    replicasets:\n      r:\n        instances:\n"+
		"          storage-1: {}\n          storage-2: {}\n")

	cmdCtx := &cmdcontext.CmdCtx{}

	insts, err := resolveInstances(cmdCtx, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"storage-1", "storage-2"}, instanceNames(insts))

	logs := make([]string, len(insts))

	for i := range insts {
		logs[i] = filepath.Join(insts[i].LogDir, "tt.log")
		require.NoError(t, os.MkdirAll(insts[i].LogDir, 0o755))
		require.NoError(t, os.WriteFile(logs[i], []byte("log"), 0o644))
	}

	other := exec.CommandContext(t.Context(), "sleep", "60")
	require.NoError(t, other.Start())
	t.Cleanup(func() {
		_ = other.Process.Kill()
		_ = other.Wait()
	})

	require.NoError(t, os.MkdirAll(filepath.Dir(insts[0].PIDFile), 0o755))
	require.NoError(t, os.WriteFile(insts[0].PIDFile,
		[]byte(strconv.Itoa(other.Process.Pid)), 0o644))
	fakeWatchdog(t, &insts[1])

	out, err := captureStdout(t, func() error {
		total, err := previewClean(insts)
		assert.Equal(t, 1, total)

		return err
	})
	require.NoError(t, err)
	assert.Contains(t, out, "myapp:storage-1:\n")
	assert.NotContains(t, out, "myapp:storage-2")

	// The constructor has bound --yes to it, and reset it.
	instanceYes = true

	t.Cleanup(func() { instanceYes = false })

	err = internalCleanModule(cmdCtx, []string{"myapp:*"})
	require.ErrorIs(t, err, errInstancesFailed)
	require.ErrorContains(t, err, "1 of 2 instances: myapp:storage-2")

	assert.NoFileExists(t, logs[0])
	assert.NoFileExists(t, insts[0].PIDFile)
	assert.FileExists(t, logs[1])
	assert.FileExists(t, insts[1].PIDFile)

	var status syscall.WaitStatus

	reaped, err := syscall.Wait4(other.Process.Pid, &status, syscall.WNOHANG, nil)
	require.NoError(t, err)
	assert.Zero(t, reaped, "the unrelated process got a signal")
}
