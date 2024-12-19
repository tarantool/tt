package console_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/console"
)

func TestNewHistory(t *testing.T) {
	type args struct {
		file        string
		maxCommands int
	}
	tests := []struct {
		name    string
		args    args
		want    []string
		wantErr bool
	}{
		{
			name:    "Empty file",
			args:    args{"testdata/history0.info", 10000},
			want:    []string{},
			wantErr: false,
		},
		{
			name: "Not empty file",
			args: args{"testdata/history1.info", 10000},
			want: []string{
				"box.cfg{}",
				"box.schema.space.create(\"test\")",
			},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hist, err := console.NewHistory(tt.args.file, tt.args.maxCommands)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewHistory() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			cmds := hist.Commands()
			if !reflect.DeepEqual(cmds, tt.want) {
				fmt.Print(cmds)
				t.Errorf("NewHistory() = %v, want %v", cmds, tt.want)
			}
		})
	}
}

func TestHistory_AppendCommand(t *testing.T) {
	tests := []struct {
		name     string
		max      int
		commands []string
	}{
		{
			"test 10",
			3,
			[]string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "0"},
		},
		{
			"test 3",
			3,
			[]string{"1", "2", "3"},
		},
		{
			"test 1",
			3,
			[]string{"1"},
		},
	}
	tmp, _ := os.MkdirTemp(os.TempDir(), "history_test*")

	// Write and ensure last command in buffer.
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(tmp, fmt.Sprintf("history_%d.info", i))
			h, err := console.NewHistory(name, tt.max)
			require.NoError(t, err)
			for _, c := range tt.commands {
				h.AppendCommand(c)
			}
			from := max(len(tt.commands)-tt.max, 0)
			reflect.DeepEqual(tt.commands[from:], h.Commands())
		})
	}

	// Read previously created history data.
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name := filepath.Join(tmp, fmt.Sprintf("history%d.info", i))
			h, err := console.NewHistory(name, tt.max)
			require.NoError(t, err)
			from := max(len(tt.commands)-tt.max, 0)
			reflect.DeepEqual(tt.commands[from:], h.Commands())
		})
	}
	os.RemoveAll(tmp)
}
