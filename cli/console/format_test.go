package console_test

import (
	"errors"
	"strconv"
	"testing"

	"github.com/tarantool/tt/cli/console"
)

type formatterImpl struct {
	data string
}

func (f formatterImpl) Format(_ console.Format) (string, error) {
	if len(f.data) > 0 {
		return f.data, nil
	}
	return "", errors.New("formatter error message")
}

type stringerImpl int

func (i stringerImpl) String() string {
	return strconv.FormatInt(int64(i), 16)
}

func TestFormat_Print(t *testing.T) {
	f := console.Format{}

	tests := []struct {
		name    string
		data    any
		want    string
		wantErr bool
	}{
		{
			"formatterImpl OK",
			formatterImpl{"test data string"},
			"test data string",
			false,
		},
		{
			"formatterImpl Err",
			formatterImpl{},
			"",
			true,
		},
		{
			"Stringer",
			stringerImpl(789997),
			"c0ded",
			false,
		},
		{
			"error",
			errors.New("just error message"),
			"Error: just error message",
			false,
		},
		{
			"string",
			"just string message",
			"just string message",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := f.Sprint(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("Format.Sprint() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Format.Sprint() = %v, want %v", got, tt.want)
			}
		})
	}
}
