package util

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
