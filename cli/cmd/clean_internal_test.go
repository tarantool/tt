package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

			err = previewClean(&running.InstanceCtx{
				LogDir: dataDir, WalDir: otherDir, VinylDir: otherDir, MemtxDir: otherDir,
			})
			require.NoError(t, err)

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
