package tail

import (
	"bytes"
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTailReader(t *testing.T) {
	type args struct {
		text  []byte
		count int
	}

	fiveLines := []byte(`one
two
three
four
five
`)

	last1000 := bytes.Repeat([]byte("one two three four five\n"), 1000)

	tests := []struct {
		name    string
		args    args
		want    []byte
		wantErr bool
	}{
		{
			name: "Last 3 lines",
			args: args{
				text:  fiveLines,
				count: 3,
			},
			want:    fiveLines[8:],
			wantErr: false,
		},
		{
			name: "Last 3 lines, last empty line",
			args: args{
				text:  fiveLines,
				count: 3,
			},
			want:    fiveLines[8:],
			wantErr: false,
		},
		{
			name: "More than we have",
			args: args{
				text:  fiveLines,
				count: 10,
			},
			want:    fiveLines,
			wantErr: false,
		},
		{
			name: "Empty",
			args: args{
				text:  []byte{},
				count: 10,
			},
			want:    []byte{},
			wantErr: true, // EOF
		},
		{
			name: "No new-line, want 0",
			args: args{
				text:  []byte("line"),
				count: 0,
			},
			want:    []byte("line"),
			wantErr: true, // EOF.
		},
		{
			name: "No new-line, want 1",
			args: args{
				text:  []byte("line"),
				count: 1,
			},
			want:    []byte("line"),
			wantErr: false, // EOF.
		},
		{
			name: "Only new-line, want 1",
			args: args{
				text:  []byte("\n"),
				count: 1,
			},
			want:    []byte("\n"),
			wantErr: false,
		},
		{
			name: "Multiple new-lines",
			args: args{
				text:  []byte("\n\n\n\n"),
				count: 2,
			},
			want:    []byte("\n\n"),
			wantErr: false,
		},
		{
			name: "Large buffer",
			args: args{
				text:  bytes.Repeat(last1000, 3),
				count: 1000,
			},
			want:    last1000,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := bytes.NewReader(tt.args.text)
			tailReader, _, err := newTailReader(context.Background(), reader, tt.args.count)
			require.NoError(t, err)

			buf := make([]byte, 1024*1024)
			n, err := tailReader.Read(buf)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, string(tt.want), string(buf[:n]))
		})
	}
}

func linesChecker(t *testing.T, expected []string) func(str string) {
	i := 0
	return func(str string) {
		require.Less(t, i, len(expected))
		assert.Equal(t, expected[i], string(str))
		i++
	}
}

func TestTailN(t *testing.T) {
	type args struct {
		n int
	}

	tmpDir := t.TempDir()

	fiveLines := `one
two
three
four
five
`

	tests := []struct {
		name    string
		text    string
		args    args
		check   func(str string)
		wantErr bool
	}{
		{
			name: "Last 3 lines",
			text: fiveLines,
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{"three", "four", "five"}),
			wantErr: false,
		},
		{
			name: "No last new-line, want 3",
			text: fiveLines[:len(fiveLines)-1],
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{"three", "four", "five"}),
			wantErr: false,
		},
		{
			name: "Empty, want 3",
			text: "",
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{}),
			wantErr: false,
		},
		{
			name: "Only new-lines, want 3",
			text: "\n\n\n\n\n\n\n\n\n",
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{"", "", ""}),
			wantErr: false,
		},
		{
			name: "Only new-lines, want 0",
			text: "\n\n\n\n\n\n\n\n\n",
			args: args{
				n: 0,
			},
			check:   linesChecker(t, []string{}),
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outFile, err := os.CreateTemp(tmpDir, "*.txt")
			require.NoError(t, err)
			defer outFile.Close()
			outFile.WriteString(tt.text)
			outFile.Close()

			in, err := TailN(context.Background(), func(str string) string {
				return str
			}, outFile.Name(), tt.args.n)
			assert.NoError(t, err)
			for line := range in {
				tt.check(line)
			}
		})
	}
}

func TestTailReader_interface(t *testing.T) {
	type args struct {
		n int
	}

	tmpDir := t.TempDir()

	fiveLines := `one
two
three
four
five
`

	tests := []struct {
		name    string
		text    string
		args    args
		check   func(str string)
		wantErr bool
	}{
		{
			name: "Last 3 lines",
			text: fiveLines,
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{"three", "four", "five"}),
			wantErr: false,
		},
		{
			name: "No last new-line, want 3",
			text: fiveLines[:len(fiveLines)-1],
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{"three", "four", "five"}),
			wantErr: false,
		},
		{
			name: "Empty, want 3",
			text: "",
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{}),
			wantErr: false,
		},
		{
			name: "Only new-lines, want 3",
			text: "\n\n\n\n\n\n\n\n\n",
			args: args{
				n: 3,
			},
			check:   linesChecker(t, []string{"", "", ""}),
			wantErr: false,
		},
		{
			name: "Only new-lines, want 0",
			text: "\n\n\n\n\n\n\n\n\n",
			args: args{
				n: 0,
			},
			check:   linesChecker(t, []string{}),
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			outFile, err := os.CreateTemp(tmpDir, "*.txt")
			require.NoError(t, err)

			defer outFile.Close()
			outFile.WriteString(tt.text)
			outFile.Close()

			r := NewTailReader(outFile.Name())
			in, err := r.Read(context.Background(), tt.args.n)
			assert.NoError(t, err)

			for line := range in {
				tt.check(line)
			}
		})
	}
}

func TestPrintLastNLinesFileDoesNotExist(t *testing.T) {
	in, err := TailN(context.Background(), func(str string) string {
		return str
	}, "some_file_name", 10)
	assert.Error(t, err)
	assert.Nil(t, in)
}

func TestFollow(t *testing.T) {
	tests := []struct {
		name                  string
		initialText           string
		linesToAppend         []string
		expectedLastLines     []string
		expectedAppendedLines []string
		nLines                int
	}{
		{
			name:                  "Follow, no last lines",
			initialText:           "line 1\n",
			linesToAppend:         []string{"line 2", "line 3"},
			expectedAppendedLines: []string{"line 2", "line 3"},
		},
		{
			name:                  "Follow, want 1 last line",
			initialText:           "line 1\n",
			linesToAppend:         []string{"line 2", "line 3"},
			expectedLastLines:     []string{"line 1"},
			expectedAppendedLines: []string{"line 2", "line 3"},
			nLines:                1,
		},
		{
			name:                  "Follow, empty file, want 1 last",
			initialText:           "",
			linesToAppend:         []string{"line 1", "line 2"},
			expectedAppendedLines: []string{"line 1", "line 2"},
			nLines:                1,
		},
		{
			name:                  "Follow, more lines, want 10",
			initialText:           "line 1\nline 2\nline 3\nline 4\n",
			linesToAppend:         []string{"line 5", "line 6", "line 7"},
			expectedLastLines:     []string{"line 1", "line 2", "line 3", "line 4"},
			expectedAppendedLines: []string{"line 5", "line 6", "line 7"},
			nLines:                10,
		},
		{
			name:                  "Follow, more lines, want 2",
			initialText:           "line 1\nline 2\nline 3\nline 4\n",
			linesToAppend:         []string{"line 5", "line 6", "line 7"},
			expectedLastLines:     []string{"line 3", "line 4"},
			expectedAppendedLines: []string{"line 5", "line 6", "line 7"},
			nLines:                2,
		},
	}

	tmpDir := t.TempDir()
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			outFile, err := os.CreateTemp(tmpDir, "*.txt")
			require.NoError(t, err)
			defer outFile.Close()

			outFile.WriteString(tt.initialText)
			outFile.Sync()

			ctx, stop := context.WithTimeout(context.Background(), time.Second*2)
			defer stop()
			in := make(chan string)
			err = Follow(ctx, in,
				func(str string) string { return str }, outFile.Name(), tt.nLines,
				&sync.WaitGroup{})
			require.NoError(t, err)

			if tt.nLines > 0 && len(tt.expectedLastLines) > 0 {
				i := 0
				for i != len(tt.expectedLastLines) {
					select {
					case <-ctx.Done():
						require.Fail(t, "timed out, no initial lines received")
						return
					case line := <-in:
						assert.Equal(t, tt.expectedLastLines[i], line)
						i++
					}
				}
			}

			// Need some time to start watching for changes after reading last lines.
			time.Sleep(time.Millisecond * 500)
			for _, line := range tt.linesToAppend {
				outFile.WriteString(line + "\n")
			}
			assert.NoError(t, outFile.Sync())

			i := 0
			for i != len(tt.expectedAppendedLines) {
				select {
				case <-ctx.Done():
					assert.Fail(t, "timed out, no lines received")
					return
				case line := <-in:
					assert.Equal(t, tt.expectedAppendedLines[i], line)
					i++
				}
			}
		})
	}
}
