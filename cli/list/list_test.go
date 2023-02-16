package list

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSortValid(t *testing.T) {
	type argsData struct {
		input, expected []string
	}

	testArgsData := []argsData{
		{[]string{"0.3.0", "1.3.0-nightly", "0.1.0"},
			[]string{"1.3.0-nightly", "0.3.0", "0.1.0"}},
		{[]string{"1.3.0", "1.4.0", "1.2.0"},
			[]string{"1.4.0", "1.3.0", "1.2.0"}},
		{[]string{"1.3.0", "2.3.0", "3.3.0"},
			[]string{"3.3.0", "2.3.0", "1.3.0"}},
		{[]string{"3.3.1", "3.3.3", "3.3.0"},
			[]string{"3.3.3", "3.3.1", "3.3.0"}},
		{[]string{"master", "3.3.3", "3.3.0"},
			[]string{"master", "3.3.3", "3.3.0"}},
	}

	for _, testData := range testArgsData {
		returned, err := sortBinaryVersions(testData.input)
		require.Nil(t, err)
		require.Equal(t, returned, testData.expected)
	}
}

func TestSortNonValid(t *testing.T) {
	type argsData struct {
		input, expected []string
	}

	testArgsData := []argsData{
		{[]string{"123123", "1.3.0-nightly", "0.1.0"},
			[]string{"1.3.0-nightly", "0.1.0"}},
		{[]string{"1.3.0.2.2.2.2", "1.4.0", "1.2.0"},
			[]string{"1.4.0", "1.2.0"}},
		{[]string{"3.3.1", "foo", "3.3.0"},
			[]string{"3.3.1", "3.3.0"}},
	}

	for _, testData := range testArgsData {
		returned, err := sortBinaryVersions(testData.input)
		require.Nil(t, err)
		require.Equal(t, returned, testData.expected)
	}
}
