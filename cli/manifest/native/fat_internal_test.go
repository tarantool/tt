package native

import (
	"bytes"
	"debug/macho"
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/require"
)

// universalMacho returns a universal Mach-O file holding the slices given, one per
// cpu in the same order, each placed at its aligned offset.
func universalMacho(t *testing.T, cpus []macho.Cpu, slices [][]byte) []byte {
	t.Helper()

	require.Len(t, slices, len(cpus))

	var (
		table, body bytes.Buffer
	)

	for index, cpu := range cpus {
		require.LessOrEqual(t, len(slices[index]), fatSliceAlign,
			"a slice of the test file must fit one alignment block")

		header := macho.FatArchHeader{Cpu: cpu}

		offset := (index + 1) * fatSliceAlign
		size := len(slices[index])

		header.Offset = uint32(offset)
		header.Size = uint32(size) //nolint:gosec // A test file is small.

		require.NoError(t, binary.Write(&table, binary.BigEndian, &header))
	}

	for _, slice := range slices {
		body.Write(slice)
		body.Write(make([]byte, fatSliceAlign-len(slice)))
	}

	var file bytes.Buffer

	require.NoError(t, binary.Write(&file, binary.BigEndian, macho.MagicFat))
	// Several slices, as a Mach-O file has.
	require.NoError(t, binary.Write(&file, binary.BigEndian, uint32(len(cpus)))) //nolint:gosec
	file.Write(table.Bytes())
	file.Write(make([]byte, fatSliceAlign-file.Len()))
	file.Write(body.Bytes())

	parsed, err := macho.NewFatFile(bytes.NewReader(file.Bytes()))
	require.NoError(t, err)
	require.Len(t, parsed.Arches, len(cpus))

	for n, cpu := range cpus {
		require.Equal(t, cpu, parsed.Arches[n].Cpu)
	}

	return file.Bytes()
}
