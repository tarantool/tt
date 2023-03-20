package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_isFetchJobsSupported(t *testing.T) {
	type args struct {
		gitOutput string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"Less",
			args{"2.7"},
			false,
		},
		{
			"Less",
			args{"1.8"},
			false,
		},
		{
			"Equal",
			args{"2.8"},
			true,
		},
		{
			"Equal",
			args{"2.8.0"},
			true,
		},
		{
			"Greater",
			args{"2.8.1"},
			true,
		},
		{
			"Greater",
			args{"2.9"},
			true,
		},
		{
			"Invalid git output",
			args{"no version"},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isGitFetchJobsSupported(tt.args.gitOutput)
			assert.Equal(t, tt.want, got)
		})
	}
}
