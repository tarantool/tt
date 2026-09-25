package console

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeLines(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "lines")
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}

func TestGetLastNLines(t *testing.T) {
	// Enough lines for the file to span several chunks, so that the line
	// boundaries fall on both sides of a chunk's edge.
	many := make([]string, 0, 5000)
	for i := range 5000 {
		many = append(many, "line "+strconv.Itoa(i))
	}

	tests := []struct {
		name    string
		content string
		n       int
		want    []string
	}{
		{"empty file", "", 10, []string{}},
		{"zero lines asked", "a\nb\n", 0, []string{"a", "b"}},
		{"fewer lines than asked", "a\nb\n", 10, []string{"a", "b"}},
		{"last lines", "a\nb\nc\n", 2, []string{"b", "c"}},
		{"no trailing newline", "a\nb\nc", 2, []string{"b", "c"}},
		{"negative count", "a\nb\nc\n", -1, []string{"c"}},
		{"across chunks", strings.Join(many, "\n") + "\n", 3000, many[2000:]},
		{"whole file of chunks", strings.Join(many, "\n") + "\n", 6000, many},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := getLastNLines(writeLines(t, tt.content), tt.n)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestGetLastNLines_NoFile(t *testing.T) {
	_, err := getLastNLines(filepath.Join(t.TempDir(), "missing"), 1)
	require.Error(t, err)
}

func TestIsRegularFile(t *testing.T) {
	dir := t.TempDir()

	assert.True(t, isRegularFile(writeLines(t, "a\n")))
	assert.False(t, isRegularFile(dir))
	assert.False(t, isRegularFile(filepath.Join(dir, "missing")))
}
