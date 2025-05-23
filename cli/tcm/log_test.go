package tcm_test

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/fatih/color"
	"github.com/pmezard/go-difflib/difflib"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/tcm"
)

var updateTestdata = flag.Bool("update-testdata", false,
	`Update "golden" data files for specified test(s)`)

var originNoColor = color.NoColor

const (
	testDataDir      = "testdata"
	expectedSubDir   = "expected"
	logLineFormat    = "%03d: line"
	logNewLineFormat = "%03d: new line added"
)

// mockTailer aimed to test logic for [tcm.TailLogs] method.
// Not considered to dependency on [tail] library.
// So it reads `N` lines from the beginning, cause it does not matter for tests.
type mockTailer struct {
	t     *testing.T
	name  string
	error error
}

// mockFollower aimed to test logic for [tcm.FollowLogs] method.
// Not considered to dependency on [tail] library.
type mockFollower struct {
	t     *testing.T
	name  string
	error error
	done  chan struct{}
	ctx   context.Context
}

// Read implements the Reader interface for mockTailer.
func (mt *mockTailer) Read(ctx context.Context, lines int) (<-chan string, error) {
	mt.t.Helper()

	if mt.error != nil {
		return nil, mt.error
	}

	return fileReaderByLine(mt.t, ctx, lines, filepath.Join(testDataDir, mt.name), nil)
}

func (mf *mockFollower) Follow(ctx context.Context, lines int) (<-chan string, error) {
	mf.t.Helper()

	if mf.error != nil {
		return nil, mf.error
	}

	mf.done = make(chan struct{})
	mf.ctx = ctx

	return fileReaderByLine(mf.t, ctx, lines, filepath.Join(testDataDir, mf.name), mf.done)
}

func (mf *mockFollower) Wait() {
	mf.t.Helper()
	require.NotNil(mf.t, mf.ctx, "Wait called before Follow - no context")
	require.NotNil(mf.t, mf.done, "Wait called before Follow - no done channel")

	select {
	case <-mf.done:
		// Got notification about finished reading file lines.
		syscall.Kill(os.Getpid(), syscall.SIGINT)

	case <-mf.ctx.Done():
		return
	}

	// Wait to context handle interrupt signal.
	<-mf.ctx.Done()
}

func fileReaderByLine(
	t *testing.T,
	ctx context.Context,
	lines int,
	fName string,
	doneSync chan struct{},
) (<-chan string, error) {
	t.Helper()

	out := make(chan string)

	go func() {
		defer func() {
			close(out)

			if doneSync != nil {
				close(doneSync)
			}
		}()

		log, err := os.Open(fName)
		if err != nil {
			return
		}
		defer log.Close()

		scanner := bufio.NewScanner(log)

		for scanner.Scan() {
			if ctx.Err() != nil || lines <= 0 {
				return
			}

			line := scanner.Text()

			select {
			case out <- line:
				lines--

			case <-ctx.Done():
				return
			}
		}
	}()

	return out, nil
}

func makeDataFileName(t *testing.T, tc *testCase) string {
	t.Helper()

	fName := filepath.Base(tc.log)
	fName = strings.TrimSuffix(fName, filepath.Ext(fName))
	fName += fmt.Sprintf("_%d_", tc.lines)

	if !tc.isFormat {
		fName += "no-"
	}

	fName += "format_"

	if !tc.isColor {
		fName += "no-"
	}

	fName += "color.log"

	return filepath.Join(testDataDir, expectedSubDir, fName)
}

func compareResults(t *testing.T, got, dataFile string) {
	t.Helper()

	want, err := os.ReadFile(dataFile)
	if err != nil {
		t.Errorf("failed to read expected data %q: %v", dataFile, err)
		return
	}

	diff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(want)),
		FromFile: "want",
		B:        difflib.SplitLines(got),
		ToFile:   "got",
		Context:  2,
	}

	u, err := difflib.GetUnifiedDiffString(diff)
	require.NoError(t, err)

	if u != "" {
		t.Errorf("mismatch (-want +got):\n%s", u)
	}
}

func saveResults(t *testing.T, data []byte, dataFile string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(dataFile), 0o755); err != nil {
		t.Fatalf("failed to create expected subdirectory: %v", err)
	}

	if err := os.WriteFile(dataFile, data, 0o644); err != nil {
		t.Fatalf("failed to write expected data %q: %v", dataFile, err)
	}
}

type testCase struct {
	isFormat bool
	isColor  bool
	lines    int
	log      string
	error    error
	wantErr  bool
}

var testCases = map[string]testCase{
	"plain 1 line": {
		lines: 1,
		log:   "plain.log",
	},
	"plain 5 lines": {
		lines: 5,
		log:   "plain.log",
	},
	"plain 10 lines": {
		lines: 10,
		log:   "plain.log",
	},
	"plain 0 lines": {
		lines: 0,
		log:   "plain.log",
	},
	"plain 1000 lines": {
		lines: 1000,
		log:   "plain.log",
	},
	"json 1 line": {
		isFormat: true,
		lines:    1,
		log:      "json.log",
	},
	"json 5 color line": {
		isFormat: true,
		isColor:  true,
		lines:    5,
		log:      "json.log",
	},
	"json 10 no-format line": {
		isColor: true,
		lines:   10,
		log:     "json.log",
	},
	"json 1000 line": {
		isFormat: true,
		isColor:  true,
		lines:    1000,
		log:      "json.log",
	},
	"json 1000 no-color line": {
		isFormat: true,
		lines:    1000,
		log:      "json.log",
	},
	"error no file": {
		isFormat: true,
		isColor:  true,
		lines:    10,
		log:      "not-readable-file.log",
		error:    errors.New("can't open file"),
		wantErr:  true,
	},
}

func TestFollowLogs(t *testing.T) {
	if *updateTestdata {
		t.Skip("Skipping FollowLogs in update test data stage")
	}

	for name, tt := range testCases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer

			if tt.isColor {
				color.NoColor = false
				defer func() {
					color.NoColor = originNoColor
				}()
			}

			mf := mockFollower{
				t:     t,
				name:  tt.log,
				error: tt.error,
			}

			p := tcm.NewLogPrinter(!tt.isFormat, !tt.isColor, &buf)

			err := tcm.FollowLogs(&mf, p, tt.lines)
			if tt.wantErr {
				require.Error(t, err, "expected an error but got none")
				return
			}

			require.NoError(t, err, "unexpected error while tailing logs")

			expectedDataFile := makeDataFileName(t, &tt)
			compareResults(t, buf.String(), expectedDataFile)
		})
	}
}

func TestTailLogs(t *testing.T) {
	for name, tt := range testCases {
		t.Run(name, func(t *testing.T) {
			var buf bytes.Buffer

			if tt.isColor {
				color.NoColor = false
				defer func() {
					color.NoColor = originNoColor
				}()
			}

			mt := mockTailer{
				t:     t,
				name:  tt.log,
				error: tt.error,
			}

			p := tcm.NewLogPrinter(!tt.isFormat, !tt.isColor, &buf)

			err := tcm.TailLogs(&mt, p, tt.lines)
			if tt.wantErr {
				require.Error(t, err, "expected an error but got none")
				return
			}

			require.NoError(t, err, "unexpected error while tailing logs")

			expectedDataFile := makeDataFileName(t, &tt)

			if *updateTestdata {
				saveResults(t, buf.Bytes(), expectedDataFile)
			} else {
				compareResults(t, buf.String(), expectedDataFile)
			}
		})
	}
}

func TestMain(m *testing.M) {
	flag.Parse()

	if *updateTestdata {
		fmt.Println("Updating testdata files...")
		os.RemoveAll(filepath.Join(testDataDir, expectedSubDir))
	}

	os.Exit(m.Run())
}
