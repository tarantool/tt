package pack

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func hex(b *bytes.Buffer) string {
	return fmt.Sprintf("%x", b.Bytes())
}

// Please, read the doc in the tagset.go file
// to understand how it should work
// All `right` values was obtained by running the same functions
// of the old Lua implementation

func TestPackTag(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	var err error
	var packed *packedTagType

	// NULL
	nullTag := rpmTagType{
		Type:  rpmTypeNull,
		Value: nil,
	}

	packed, err = packTag(nullTag)
	assert.Nil(err)
	assert.Equal(1, packed.Count)
	assert.Equal("", hex(packed.Data))

	// CHAR
	charTag := rpmTagType{
		Type:  rpmTypeChar,
		Value: []byte{0xde, 0xad, 0xbe, 0xaf},
	}

	packed, err = packTag(charTag)
	assert.Nil(err)
	assert.Equal(4, packed.Count)
	assert.Equal("deadbeaf", hex(packed.Data))

	// BIN
	binTag := rpmTagType{
		Type:  rpmTypeBin,
		Value: []byte("binTagValue"),
	}

	packed, err = packTag(binTag)
	assert.Nil(err)
	assert.Equal(11, packed.Count)
	assert.Equal("62696e54616756616c7565", hex(packed.Data))

	// STRING_ARRAY
	stringArrayTag := rpmTagType{
		Type:  rpmTypeStringArray,
		Value: []string{"str-1", "str-2", "str-3"},
	}

	packed, err = packTag(stringArrayTag)
	assert.Nil(err)
	assert.Equal(3, packed.Count)
	assert.Equal("7374722d31007374722d32007374722d3300", hex(packed.Data))

	// STRING
	stringTag := rpmTagType{
		Type:  rpmTypeString,
		Value: "stringTagValue",
	}

	packed, err = packTag(stringTag)
	assert.Nil(err)
	assert.Equal(1, packed.Count)
	assert.Equal("737472696e6754616756616c756500", hex(packed.Data))

	// INT8
	int8Tag := rpmTagType{
		Type:  rpmTypeInt8,
		Value: []int8{1, 2, 100, -66},
	}

	packed, err = packTag(int8Tag)
	assert.Nil(err)
	assert.Equal(4, packed.Count)
	assert.Equal("010264be", hex(packed.Data))

	// INT16
	int16Tag := rpmTagType{
		Type:  rpmTypeInt16,
		Value: []int16{1, 2, 100, -66},
	}

	packed, err = packTag(int16Tag)
	assert.Nil(err)
	assert.Equal(4, packed.Count)
	assert.Equal("000100020064ffbe", hex(packed.Data))

	// INT32
	int32Tag := rpmTagType{
		Type:  rpmTypeInt32,
		Value: []int32{1, 2, 100, -66},
	}

	packed, err = packTag(int32Tag)
	assert.Nil(err)
	assert.Equal(4, packed.Count)
	assert.Equal("000000010000000200000064ffffffbe", hex(packed.Data))

	// INT64
	int64Tag := rpmTagType{
		Type:  rpmTypeInt64,
		Value: []int64{1, 2, 100, -66},
	}

	packed, err = packTag(int64Tag)
	assert.Nil(err)
	assert.Equal(4, packed.Count)
	assert.Equal(
		"000000000000000100000000000000020000000000000064ffffffffffffffbe",
		hex(packed.Data),
	)

	// INT64
	err64Tag := rpmTagType{
		Type:  rpmTypeInt64,
		Value: []int16{1},
	}

	_, err = packTag(err64Tag)
	assert.EqualError(err, "INT64 value should be []int64")

	// INT32
	err32Tag := rpmTagType{
		Type:  rpmTypeInt32,
		Value: []int16{1},
	}

	_, err = packTag(err32Tag)
	assert.EqualError(err, "INT32 value should be []int32")

	// NULL
	errNullTag := rpmTagType{
		Type:  rpmTypeNull,
		Value: []int16{1},
	}

	_, err = packTag(errNullTag)
	assert.EqualError(err, "NULL value should be nil")

	// BIN
	errBinTag := rpmTagType{
		Type:  rpmTypeBin,
		Value: []int16{1},
	}

	_, err = packTag(errBinTag)
	assert.EqualError(err, "BIN and CHAR values should be []byte")

	// STRING
	errStringTag := rpmTagType{
		Type:  rpmTypeString,
		Value: []int16{1},
	}

	_, err = packTag(errStringTag)
	assert.EqualError(err, "STRING value should be string")

	// ARRAY STRING
	errArrStringTag := rpmTagType{
		Type:  rpmTypeStringArray,
		Value: []int16{1},
	}

	_, err = packTag(errArrStringTag)
	assert.EqualError(err, "STRING_ARRAY value should be []string")
}

func TestPackedTagIndex(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	index := getPackedTagIndex(-100, 1, -16, 12)
	assert.Equal("ffffff9c00000001fffffff00000000c", hex(index))
}

func TestTagSetHeader(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	header := getTagSetHeader(11, 100500)
	assert.Equal("8eade801000000000000000b00018894", hex(header))
}

func TestPackTagSet(t *testing.T) {
	t.Parallel()

	assert := assert.New(t)

	var err error
	var data *bytes.Buffer

	regionTagID := 63

	// tags set with different types
	data, err = packTagSet(rpmTagSetType{
		{ID: tagName, Type: rpmTypeString, Value: "name"},
		{ID: tagDirNames, Type: rpmTypeStringArray, Value: []string{"name-1", "name-2"}},
		{ID: tagDirIndexes, Type: rpmTypeInt32, Value: []int32{1, 2}},
		{ID: tagFileModes, Type: rpmTypeInt16, Value: []int16{10, 20}},
	}, regionTagID)

	assert.Nil(err)
	assert.Equal(
		"8eade8010000000000000005000000300000003f000000070000002"+
			"000000010000003e80000000600000000000000010000045e00"+
			"00000800000005000000020000045c000000040000001400000"+
			"00200000406000000030000001c000000026e616d65006e616d"+
			"652d31006e616d652d3200000000000100000002000a0014000"+
			"0003f00000007ffffffb000000010",
		hex(data),
	)

	// tags set with padding
	data, err = packTagSet(rpmTagSetType{
		// 5 bytes string (4 symbols + NULL)
		{ID: tagName, Type: rpmTypeString, Value: "abcd"},
		// int32 value should be aligned on 4-byte boundaries
		{ID: tagDirIndexes, Type: rpmTypeInt32, Value: []int32{1, 2, 3}},
	}, regionTagID)

	assert.Nil(err)
	assert.Equal(
		"8eade8010000000000000003000000240000003f000000070000001"+
			"400000010000003e80000000600000000000000010000045c00"+
			"000004000000080000000361626364000000000000000100000"+
			"002000000030000003f00000007ffffffd000000010",
		hex(data),
	)
}
