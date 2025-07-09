package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_IsValidCommitHash(t *testing.T) {
	tests := []struct {
		name string
		hash string
		want bool
	}{
		{
			"common 7-digit",
			"aaaaaaa",
			true,
		},
		{
			"common git hash",
			"168cf81ce2430ce3ad12f17c81eea3cd7e6bf54b", // spell-checker:disable-line
			true,
		},
		{
			"common git hash",
			"954e256e6df0b402040091ee1bbc08624dfb72f8", // spell-checker:disable-line
			true,
		},
		{
			"wrong hash",
			"95965085ebed88eabd28cc3e83bdz9157391ac81", // spell-checker:disable-line
			false,
		},
		{
			"wrong hash",
			"zzzzzzz",
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := IsValidCommitHash(tt.hash)
			if err != nil {
				assert.Error(t, err)
			}
			assert.Equal(t, tt.want, got)
		})
	}
}

func Test_IsPullRequest(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantBool bool
		wantID   string
	}{
		{
			"common pr",
			"pr/530",
			true,
			"530",
		},
		{
			"common pr upper case",
			"PR/130",
			true,
			"130",
		},
		{
			"wrong pr",
			"r/10",
			false,
			"",
		},
		{
			"wrong rp",
			"p/50",
			false,
			"",
		},
		{
			"wrong rp",
			"qr/12",
			false,
			"",
		},
		{
			"wrong rp",
			"qr/abcd",
			false,
			"",
		},
		{
			"wrong rp",
			"pr/aa",
			false,
			"aa",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotBool, gotID := IsPullRequest(tt.input)
			assert.Equal(t, tt.wantBool, gotBool)
			assert.Equal(t, tt.wantID, gotID)
		})
	}
}

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
