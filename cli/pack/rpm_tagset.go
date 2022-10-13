package pack

import (
	"bytes"
	"fmt"
	"io"

	"github.com/tarantool/tt/cli/util"
)

type rpmValueType int32

type rpmTagType struct {
	ID    int
	Type  rpmValueType
	Value interface{}
}

type rpmTagSetType []rpmTagType

type packedTagType struct {
	Count int
	Data  *bytes.Buffer
}

func (tagSet *rpmTagSetType) addTags(tags ...rpmTagType) {
	*tagSet = append(*tagSet, tags...)
}

/**
 *
 *  Bless @knazarov for writing this code once,
 *  I just rewrote it in Go.

 *  See http: * ftp.rpm.org/max-rpm/s1-rpm-file-format-rpm-file-format.html
 *  Section `The Header Structure in Depth` contains detailed description of
 *  header structure

 *  In fact, both RPM header and Signature is a tags set (`rpmTagSetType`),
 *  that is packed by some rules.

 *  Tag (`rpmTagType`):
 *  - ID: a numeric value that identifies type of value
 *  - Type: a numeric value that describes the format of the value
 *  - Value : a value itself

 *  For example,
 *  {ID: tagName, Type: rpmTypeString, Value: "myapp"}
 *  is a tag that stores Name - a string value "myapp"

 *  So,
 *  packed tags set structure is quite simple:
 *
 *   +-----------------------+
 *   |         Header        |
 *   +-----------------------+
 *   |         Index         |
 *   +-----------------------+
 *   |         Data          |
 *   +-----------------------+
 *
 *  Any tag set has a special regionTag, that is described below.
 *
 *   -- Header -----------------------------------------------------------------
 *
 *  - 3-byte magic number: 8e ad e8                     `headerMagic`
 *  - 1-byte version number                             `versionByte`
 *  - 4 bytes that are reserved for future expansion    `reservedBytes`
 *  - 4-byte number that indicates how many tags are packed (include regionTag)
 *  - 4-byte number indicating how many bytes contains Data
 *
 *   -- Index ------------------------------------------------------------------
 *
 *  Each index entry contains information about all tags in tagset
 *  (include regionTag).
 *
 *  - 4-byte tag.ID
 *  - 4-byte tag.Type
 *  - 4-byte offset that contains the position of the data
 *  - 4-byte count that contains the number of value items
 *
 *   -- Data -------------------------------------------------------------------
 *
 *  Data is a concatenated tag values packed to bytes
 *  and a special regionTag data that is described below.
 *
 *  Depending on the tag Type, there are some details that should be kept in mind:
 *  - For STRING data, each string is terminated with a null byte.
 *  - For INT data, each integer is stored at the natural boundary for its type.
 *    A 64-bit INT is stored on an 8-byte boundary,
 *    a 16-bit INT is stored on a 2-byte boundary, and so on.
 *  All data is in network byte order.
 *
 *   -- Example -----------------------------------------------------------------
 *
 *  For example, we have set of two tags:
 *    {ID: tagName, Type: rpmTypeString, Value: "abcd"} <- 5 bytes string
 *                                                        (4 symbols + NULL)
 *    {ID: tagDirIndexes, Type: rpmTypeInt32, Value: []int32{1, 2, 3}} <- int32
 *
 *  * `Data` is `tagsData` + `regionTagData`
 *
 *    first, let's compute the Data for each tag
 *
 *    `tagNameData` =       `6162636400`               (5 bytes)
 *    `tagDirIndexesData` = `000000010000000200000003` (should be stored an 4-byte boundary )
 *
 *    We need to add 3 NULL bytes to align second value offset
 *
 *    `tagsData` = `6162636400000000000000010000000200000003`
 *      `6162636400`                 `tagNameData`
 *      `000000`                     3 padding bytes
 *      `000000010000000200000003`   `tagDirIndexesData`
 *
 *  * `Index` is `regionTagIndex` + `tagsIndex`
 *
 *   first, let's compute the Index for each tag
 *
 *   `tagNameIndex` = `000003e8000000060000000000000001`
 *     `000003e8`  ID: tagName (1000)
 *     `00000006`  Type: rpmTypeString (6)
 *     `00000000`  offset: 0
 *     `00000001`  count: 1
 *
 *   `tagDirIndexesIndex` = `0000045c000000040000000800000003`
 *     `0000045c`  ID: tagDirIndexes (1116)
 *     `00000004`  Type: rpmTypeInt32 (4)
 *     `00000008`  offset: 8 (5 bytes of tagNameData + 3 padding bytes)
 *     `00000003`  count: 3
 *
 *   `tagsIndex` = `000003e80000000600000000000000010000045c000000040000000800000003`
 *
 *  * `regionTag` data and index
 *
 *    First, `regionTagIndex`
 *
 *    It's computed as an Index entry for a tag (it has ID and a type `rpmTypeBin`)
 *      it's `offset` is equal to len of `tagsData`, and `count` is always 16
 *
 *    `regionTagIndex` = `0000003f000000070000001400000010`
 *       `0000003f`  ID: regionTagID (63)
 *  		`00000007`  Type: rpmTypeBin (7)
 *       `00000014`  offset: 20 (8 + 3*4)
 *       `00000003`  count: 16
 *
 *    Now, `regionTagData`
 *
 *    It's computed as an Index entry for a tag (it has ID and a type `rpmTypeBin`)
 *      but it's `offset` is equal to `tagsNum`*16, and `count` is always 16
 *      (`tagsNum` includes `regionTag`)
 *
 *    Let the `regionTagID` to be 63.
 *
 *    `regionTagData` = `0000003f00000007ffffffd000000010`
 *       `0000003f`  ID: regionTagID (63)
 *  		`00000007`  Type: rpmTypeBin (7)
 *       `ffffffd0`  offset: -48 (-3*16) 3 is `tagName`, `tagDirIndexes` and `regionTag`
 *       `00000010`  count: 16
 *
 *  * Result
 *
 *  `resData` = `tagsData` + `regionTagData` =
 *    `61626364000000000000000100000002000000030000003f00000007ffffffd000000010`
 *
 *  `resIndex` = `regionTagIndex` + `tagsIndex`
 *
 *  Let's compute `tagSetHeader`:
 *
 *  `tagSetHeader` = `8eade801000000000000000300000024`
 *    `8eade8`    `headerMagic`
 *    `01`        `versionByte`
 *    `00000000`  `reservedBytes`
 *    `00000003`  `tagsNum`: 3 (`tagName`, `tagDirIndexes` and `regionTag`)
 *    `00000024`  `dataLen`: 36 (20 + 16)
 *
 *   The result is
 *   `tagSetHeader` + `resIndex` + `resData`
 *
 */

// packTagSet packs all passed tags into a buffer and returns it.
func packTagSet(tagSet rpmTagSetType, regionTagID int) (*bytes.Buffer, error) {
	var tagsData = bytes.NewBuffer(nil)
	var tagsIndex = bytes.NewBuffer(nil)

	// tagsData and tagsIndex
	for _, tag := range tagSet {
		packed, err := packTag(tag)

		if err != nil {
			return nil, err
		}
		if boundaries, ok := boundariesByType[tag.Type]; !ok {
			return nil, fmt.Errorf("Boundaries for type %d is not set", tag.Type)
		} else if boundaries > 1 {
			alignData(tagsData, boundaries)
		}

		tagIndex := getPackedTagIndex(tag.ID, tag.Type, tagsData.Len(), packed.Count)

		if err := util.ConcatBuffers(tagsData, packed.Data); err != nil {
			return nil, err
		}

		if err := util.ConcatBuffers(tagsIndex, tagIndex); err != nil {
			return nil, err
		}
	}

	// regionTag index
	regionTagIndex := getPackedTagIndex(regionTagID, rpmTypeBin, tagsData.Len(), 16)

	// resIndex is regionTagIndex + tagsIndex
	var resIndex = bytes.NewBuffer(nil)
	if err := util.ConcatBuffers(resIndex, regionTagIndex, tagsIndex); err != nil {
		return nil, err
	}

	// regionTag data
	tagsNum := len(tagSet) + 1
	regionTagData := getPackedTagIndex(regionTagID, rpmTypeBin, -tagsNum*16, 16)

	// resData is tagsData + regionTagData
	var resData = bytes.NewBuffer(nil)
	if err := util.ConcatBuffers(resData, tagsData, regionTagData); err != nil {
		return nil, err
	}

	// tagSetHeader
	tagSetHeader := getTagSetHeader(tagsNum, resData.Len())

	// res is tagSetHeader + resIndex + resData
	var res = bytes.NewBuffer(nil)
	if err := util.ConcatBuffers(res, tagSetHeader, resIndex, resData); err != nil {
		return nil, err
	}

	return res, nil
}

// getPackedTagIndex packs a passed tag index into a buffer and returns it.
func getPackedTagIndex(tagID int, tagType rpmValueType, offset int, count int) *bytes.Buffer {
	tagIndex := packValues(
		int32(tagID),
		int32(tagType),
		int32(offset),
		int32(count),
	)

	return tagIndex
}

// getPackedTagIndex packs a tags set header into a buffer and returns it.
func getTagSetHeader(tagsNum int, dataLen int) *bytes.Buffer {
	tagSetHeader := packValues(
		headerMagic,
		byte(versionByte),
		int32(reservedBytes),
		int32(tagsNum),
		int32(dataLen),
	)

	return tagSetHeader
}

// packTag packs the passed tag into the tag structure and returns it.
func packTag(tag rpmTagType) (*packedTagType, error) {
	var packed packedTagType
	packed.Data = bytes.NewBuffer(nil)

	switch tag.Type {
	case rpmTypeNull: // NULL
		if tag.Value != nil {
			return nil, fmt.Errorf("NULL value should be nil")
		}

		packed.Count = 1

	case rpmTypeChar: // CHAR
		fallthrough

	case rpmTypeBin: // BIN
		byteArray, ok := tag.Value.([]byte)
		if !ok {
			return nil, fmt.Errorf("BIN and CHAR values should be []byte")
		}

		packed.Count = len(byteArray)
		for _, byteValue := range byteArray {
			if _, err := io.Copy(packed.Data, packValues(byteValue)); err != nil {
				return nil, err
			}
		}

	case rpmTypeStringArray: // STRING_ARRAY
		// Value should be strings array.
		stringsArray, ok := tag.Value.([]string)
		if !ok {
			return nil, fmt.Errorf("STRING_ARRAY value should be []string")
		}

		packed.Count = len(stringsArray)

		for _, v := range stringsArray {
			bytedString := []byte(v)
			bytedString = append(bytedString, 0)
			if _, err := io.Copy(packed.Data, packValues(bytedString)); err != nil {
				return nil, err
			}
		}

	case rpmTypeString: // STRING
		// Value should be string.
		stringValue, ok := tag.Value.(string)
		if !ok {
			return nil, fmt.Errorf("STRING value should be string")
		}

		packed.Count = 1

		bytedString := []byte(stringValue)
		bytedString = append(bytedString, 0)
		if _, err := io.Copy(packed.Data, packValues(bytedString)); err != nil {
			return nil, err
		}

	case rpmTypeInt8: // INT8
		// Value should be []int8.
		int8Values, ok := tag.Value.([]int8)
		if !ok {
			return nil, fmt.Errorf("INT8 value should be []int8")
		}

		packed.Count = len(int8Values)

		if _, err := io.Copy(packed.Data, packValues(int8Values)); err != nil {
			return nil, err
		}

	case rpmTypeInt16: // INT16
		// Value should be []int16.
		int16Values, ok := tag.Value.([]int16)
		if !ok {
			return nil, fmt.Errorf("INT16 value should be []int16")
		}

		packed.Count = len(int16Values)

		for _, value := range int16Values {
			if _, err := io.Copy(packed.Data, packValues(value)); err != nil {
				return nil, err
			}
		}

	case rpmTypeInt32: // INT32
		// Value should be []int32.
		int32Values, ok := tag.Value.([]int32)
		if !ok {
			return nil, fmt.Errorf("INT32 value should be []int32")
		}

		packed.Count = len(int32Values)

		for _, value := range int32Values {
			if _, err := io.Copy(packed.Data, packValues(value)); err != nil {
				return nil, err
			}
		}

	case rpmTypeInt64: // INT64
		// Value should be []int64.
		int64Values, ok := tag.Value.([]int64)
		if !ok {
			return nil, fmt.Errorf("INT64 value should be []int64")
		}

		packed.Count = len(int64Values)

		for _, value := range int64Values {
			if _, err := io.Copy(packed.Data, packValues(value)); err != nil {
				return nil, err
			}
		}

	default:
		return nil, fmt.Errorf("Unknown tag type: %d", tag.Type)
	}

	return &packed, nil
}
