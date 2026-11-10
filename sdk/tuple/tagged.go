package tuple

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"
)

// The tags of the tagged JSON: the key of the one-key object a value that
// JSON has no type for is written as.
const (
	TagBinary    = "$binary"
	TagRawString = "$rawstring"
	TagDouble    = "$double"
	TagFloat32   = "$float32"
	TagDecimal   = "$decimal"
	TagUUID      = "$uuid"
	TagDatetime  = "$datetime"
	TagInterval  = "$interval"
	TagExt       = "$ext"
	TagMap       = "$map"
)

// knownTags are the tags: an object of one of them as its only key is a
// tagged value, and a map of one entry whose key is one is written in a
// TagMap.
var knownTags = map[string]bool{
	TagBinary: true, TagRawString: true, TagDouble: true, TagFloat32: true,
	TagDecimal: true, TagUUID: true, TagDatetime: true, TagInterval: true,
	TagExt: true, TagMap: true,
}

// The keys of the object a TagExt holds.
const (
	extKeyType = "type"
	extKeyData = "data"
)

var (
	// ErrTaggedJSON reports a text that is not tagged JSON.
	ErrTaggedJSON = errors.New("not tagged JSON")
	// ErrUnsupportedType reports a value of a Go type that tagged JSON does
	// not hold.
	ErrUnsupportedType = errors.New("no tagged JSON for the type")
)

// AppendTaggedJSON appends value as tagged JSON: JSON that keeps every
// MessagePack value Tarantool stores, so that [ParseTaggedJSON] reads back
// the same value.
//
//   - nil, a bool, an integer, a string that is valid UTF-8 and an array are
//     their JSON. A float64 is a number with a point or an exponent, so that
//     it does not read back as an integer.
//   - A map is a JSON object in the order of its entries, unless a key is not
//     a string, or it has one entry whose key is a tag, such as $decimal:
//     then it is {"$map": [[key, value], ...]}.
//   - Any other value is an object of one key, its tag, which names its type:
//     {"$binary": base64}, {"$rawstring": base64} for a string that is not
//     valid UTF-8, {"$double": "NaN"} and the infinities, {"$float32": number
//     or "NaN"...}, {"$decimal": text}, {"$uuid": text}, {"$datetime": text},
//     {"$interval": text} and {"$ext": {"type": type, "data": base64}}, each
//     text as [Text] writes it - but for a datetime its Text does not tell
//     from another, such as an hour its zone repeats, which is its date and
//     time, its offset and its zone, as in "2024-10-27T02:30:00+0100
//     Europe/Berlin".
//
// What it keeps is the value, as Tarantool compares and stores it, not the
// bytes that encode it: an integer is written as the number it is, whatever
// the width and the signedness of its encoding, a NaN as NaN, whatever its
// bits.
//
// Like the other renderers, it takes the values go-tarantool decodes as well:
// integers and floats of any width, []byte, slices, and Go maps, whose
// entries it sorts by their keys. A value of another type is an error that
// wraps [ErrUnsupportedType], and dst is returned as it is then.
func AppendTaggedJSON(dst []byte, value any) ([]byte, error) {
	out, err := appendTaggedJSON(dst, value)
	if err != nil {
		return dst, err
	}

	return out, nil
}

//nolint:cyclop,funlen // One case per type is the clearest form.
func appendTaggedJSON(dst []byte, value any) ([]byte, error) {
	if isNil(value) {
		return append(dst, "null"...), nil
	}

	switch typed := value.(type) {
	case bool:
		return strconv.AppendBool(dst, typed), nil
	case string:
		if !utf8.ValidString(typed) {
			raw := base64.StdEncoding.EncodeToString([]byte(typed))

			return appendTagged(dst, TagRawString, raw), nil
		}

		return appendJSONString(dst, typed), nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return appendTagged(dst, TagDouble, floatText(typed, float64Bits)), nil
		}

		return append(dst, floatLiteral(typed, float64Bits)...), nil
	case float32:
		dst = append(dst, `{"`+TagFloat32+`":`...)

		number := float64(typed)
		if math.IsNaN(number) || math.IsInf(number, 0) {
			dst = appendJSONString(dst, floatText(number, float32Bits))
		} else {
			dst = append(dst, floatLiteral(number, float32Bits)...)
		}

		return append(dst, '}'), nil
	case Binary:
		return appendTagged(dst, TagBinary, base64.StdEncoding.EncodeToString(typed)), nil
	case []byte:
		return appendTagged(dst, TagBinary, base64.StdEncoding.EncodeToString(typed)), nil
	case decimal.Decimal:
		return appendTagged(dst, TagDecimal, decimalText(typed)), nil
	case *decimal.Decimal:
		return appendTagged(dst, TagDecimal, decimalText(*typed)), nil
	case uuid.UUID:
		return appendTagged(dst, TagUUID, typed.String()), nil
	case datetime.Datetime:
		return appendTagged(dst, TagDatetime, datetimeTaggedText(typed)), nil
	case *datetime.Datetime:
		return appendTagged(dst, TagDatetime, datetimeTaggedText(*typed)), nil
	case datetime.Interval:
		return appendTagged(dst, TagInterval, intervalText(typed)), nil
	case *datetime.Interval:
		return appendTagged(dst, TagInterval, intervalText(*typed)), nil
	case Ext:
		dst = append(dst, `{"`+TagExt+`":{"`+extKeyType+`":`...)
		dst = strconv.AppendInt(dst, int64(typed.Type), decimalBase)
		dst = append(dst, `,"`+extKeyData+`":`...)
		dst = appendJSONString(dst, base64.StdEncoding.EncodeToString(typed.Data))

		return append(dst, "}}"...), nil
	case []any:
		return appendTaggedArray(dst, len(typed), func(i int) any { return typed[i] })
	case Map:
		return appendTaggedMap(dst, typed)
	case map[any]any:
		return appendTaggedMap(dst, sortedMap(typed))
	case map[string]any:
		items := make(Map, 0, len(typed))
		for key, item := range typed {
			items = append(items, MapItem{Key: key, Value: item})
		}

		return appendTaggedMap(dst, sortItems(items))
	}

	if i, ok := asInt(value); ok {
		return strconv.AppendInt(dst, i, decimalBase), nil
	}

	if u, ok := asUint(value); ok {
		return strconv.AppendUint(dst, u, decimalBase), nil
	}

	return appendTaggedReflect(dst, value)
}

// appendTaggedReflect appends a nil pointer as null, and a slice, an array
// or a map of another type by its items.
func appendTaggedReflect(dst []byte, value any) ([]byte, error) {
	reflected := reflect.ValueOf(value)

	switch reflected.Kind() { //nolint:exhaustive // Other kinds have no tagged JSON.
	case reflect.Pointer, reflect.Interface:
		if reflected.IsNil() {
			return append(dst, "null"...), nil
		}

		return appendTaggedJSON(dst, reflected.Elem().Interface())
	case reflect.Slice, reflect.Array:
		return appendTaggedArray(dst, reflected.Len(), func(i int) any {
			return reflected.Index(i).Interface()
		})
	case reflect.Map:
		items := make(Map, 0, reflected.Len())
		for iter := reflected.MapRange(); iter.Next(); {
			items = append(items, MapItem{
				Key:   iter.Key().Interface(),
				Value: iter.Value().Interface(),
			})
		}

		return appendTaggedMap(dst, sortItems(items))
	}

	return dst, fmt.Errorf("%w: %T", ErrUnsupportedType, value)
}

// appendTagged appends {"tag": "text"}.
func appendTagged(dst []byte, tag, text string) []byte {
	dst = append(dst, `{"`+tag+`":`...)
	dst = appendJSONString(dst, text)

	return append(dst, '}')
}

func appendTaggedArray(dst []byte, count int, item func(int) any) ([]byte, error) {
	dst = append(dst, '[')

	for index := range count {
		if index > 0 {
			dst = append(dst, ',')
		}

		var err error

		dst, err = appendTaggedJSON(dst, item(index))
		if err != nil {
			return dst, err
		}
	}

	return append(dst, ']'), nil
}

func appendTaggedMap(dst []byte, items Map) ([]byte, error) {
	if !isPlainObject(items) {
		dst = append(dst, `{"`+TagMap+`":[`...)

		for i, item := range items {
			if i > 0 {
				dst = append(dst, ',')
			}

			var err error

			dst, err = appendTaggedJSON(dst, []any{item.Key, item.Value})
			if err != nil {
				return dst, err
			}
		}

		return append(dst, "]}"...), nil
	}

	dst = append(dst, '{')

	for i, item := range items {
		if i > 0 {
			dst = append(dst, ',')
		}

		key, _ := item.Key.(string)

		dst = appendJSONString(dst, key)
		dst = append(dst, ':')

		var err error

		dst, err = appendTaggedJSON(dst, item.Value)
		if err != nil {
			return dst, err
		}
	}

	return append(dst, '}'), nil
}

// isPlainObject reports whether a map is written as a JSON object: all its
// keys are strings of valid UTF-8, and it is not one entry whose key would be
// taken for a tag.
func isPlainObject(items Map) bool {
	for _, item := range items {
		key, ok := item.Key.(string)
		if !ok || !utf8.ValidString(key) || (len(items) == 1 && knownTags[key]) {
			return false
		}
	}

	return true
}

// ParseTaggedJSON parses one value of tagged JSON, as [AppendTaggedJSON]
// writes it, into the values [Decode] returns: an integer is a uint64, or an
// int64 when it is negative, a number with a point or an exponent a float64,
// an object a [Map] in the order of its keys. An object whose only key is a
// tag is a tagged value, and its payload must be one of the tag; any other
// object, of a key such as $id or of more keys, is a map. Values nested
// deeper than Decode takes are an error, as is a decimal Tarantool does not
// hold.
func ParseTaggedJSON(text []byte) (any, error) {
	err := checkJSONText(text)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTaggedJSON, err)
	}

	dec := json.NewDecoder(bytes.NewReader(text))
	dec.UseNumber()

	value, err := taggedValue(dec, 0)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrTaggedJSON, err)
	}

	_, err = dec.Token()
	if !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%w: text after the value", ErrTaggedJSON)
	}

	return value, nil
}

var (
	// errUnexpected reports a token that does not start a value where one is
	// expected.
	errUnexpected = errors.New("unexpected token")
	// errTooDeep reports values nested deeper than maxDepth.
	errTooDeep = fmt.Errorf("nested deeper than %d", maxDepth)
	// errUnknownTag reports a tag this package does not know.
	errUnknownTag = errors.New("unknown tag")
	// errFloatWord reports a string in place of a float that is not NaN or
	// an infinity.
	errFloatWord = errors.New("not NaN or an infinity")
	// errDecimalRange reports a decimal Tarantool does not hold.
	errDecimalRange = errors.New("a decimal of more digits or a larger exponent " +
		"than Tarantool takes")
	// errNotUTF8 reports a text encoding/json would change: bytes that are not
	// UTF-8, or a \u escape of half a surrogate pair, each of which it reads
	// as U+FFFD.
	errNotUTF8 = errors.New("a string that is not UTF-8")
)

// checkJSONText checks that a JSON text has no string encoding/json would
// read as another one. A backslash is in a string only, so every escape is
// found by its backslash.
func checkJSONText(text []byte) error {
	if !utf8.Valid(text) {
		return errNotUTF8
	}

	for pos := 0; pos < len(text); pos++ {
		if text[pos] != '\\' {
			continue
		}

		// The escaped byte, which the loop steps over.
		pos++

		code, ok := escapedRune(text, pos)
		if !ok {
			continue
		}

		pos += len(`uXXXX`) - 1

		switch {
		case utf16.IsSurrogate(code) && code >= lowSurrogates:
			return errNotUTF8
		case utf16.IsSurrogate(code):
			low, ok := escapedRune(text, pos+len(`\`)+1)
			if !ok || pos+1 >= len(text) || text[pos+1] != '\\' ||
				!utf16.IsSurrogate(low) || low < lowSurrogates {
				return errNotUTF8
			}

			pos += len(`\uXXXX`)
		}
	}

	return nil
}

const (
	// lowSurrogates is the first of the second halves of a surrogate pair.
	lowSurrogates = 0xdc00
	// The base and the bits of the code of a \u escape.
	hexBase    = 16
	escapeBits = 16
)

// escapedRune reads the code of a \u escape whose u is at pos.
func escapedRune(text []byte, pos int) (rune, bool) {
	if pos+len(`uXXXX`) > len(text) || text[pos] != 'u' {
		return 0, false
	}

	code, err := strconv.ParseUint(string(text[pos+1:pos+len(`uXXXX`)]), hexBase, escapeBits)

	return rune(code), err == nil
}

// taggedValue parses a value at depth, the depth Decode would find it at: a
// tagged value is at the depth of the value it stands for, and the items of
// an array or a map one deeper.
func taggedValue(dec *json.Decoder, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errTooDeep
	}

	token, err := dec.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
	}

	return valueFrom(dec, token, depth)
}

// valueFrom parses the value token starts at depth.
func valueFrom(dec *json.Decoder, token json.Token, depth int) (any, error) {
	if depth > maxDepth {
		return nil, errTooDeep
	}

	switch typed := token.(type) {
	case json.Delim:
		switch typed {
		case '[':
			return taggedArray(dec, depth)
		case '{':
			return taggedObject(dec, depth)
		}
	case json.Number:
		return jsonNumber(typed.String())
	case nil, bool, string:
		return typed, nil
	}

	return nil, fmt.Errorf("%w %v", errUnexpected, token)
}

func taggedArray(dec *json.Decoder, depth int) (any, error) {
	items := []any{}

	for dec.More() {
		item, err := taggedValue(dec, depth+1)
		if err != nil {
			return nil, err
		}

		items = append(items, item)
	}

	_, err := dec.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
	}

	return items, nil
}

func taggedObject(dec *json.Decoder, depth int) (any, error) {
	items := Map{}

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return nil, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
		}

		key, _ := token.(string)

		if len(items) == 0 && knownTags[key] {
			value, tagged, err := firstTagged(dec, key, depth)
			if err != nil || tagged {
				return value, err
			}

			items = append(items, MapItem{Key: key, Value: value})

			continue
		}

		value, err := taggedValue(dec, depth+1)
		if err != nil {
			return nil, err
		}

		items = append(items, MapItem{Key: key, Value: value})
	}

	_, err := dec.Token()
	if err != nil {
		return nil, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
	}

	return items, nil
}

// firstTagged reads the value after tag, the first key of an object at
// depth. Whether the object is a tagged value is known once the value is
// read: it is if no key follows, and then firstTagged reads the end of the
// object too and returns the tagged value. The value is read once, as the
// payload of the tag and as a value of a map both.
func firstTagged(dec *json.Decoder, tag string, depth int) (any, bool, error) {
	read, err := readPayload(dec, tag, depth)
	if err != nil {
		return nil, false, err
	}

	if dec.More() {
		value, err := read.plain()

		return value, false, err
	}

	value, err := read.tagged()
	if err != nil {
		return nil, false, fmt.Errorf("%s: %w", tag, err)
	}

	_, err = dec.Token()

	return value, true, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
}

// payload is the first value of an object whose first key is a tag, as the
// payload of the tag and as a value of a map: which of the two it is the
// keys that follow tell.
type payload struct {
	tagged func() (any, error)
	plain  func() (any, error)
}

// readPayload reads the value after a tag, for an object at depth. Only the
// payload of a TagMap holds values, read at the depth of the items of a map;
// that of any other tag is a scalar, or for a TagExt an object of scalars, so
// that tags nested in a payload cannot recurse without going deeper.
func readPayload(dec *json.Decoder, tag string, depth int) (payload, error) {
	token, err := dec.Token()
	if err != nil {
		return payload{}, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
	}

	switch {
	case tag == TagMap && token == json.Delim('['):
		return mapPayload(dec, depth)
	case tag == TagExt && token == json.Delim('{'):
		// The fields of an extension, scalars, are read at its own depth; as
		// the value of a map the object is one deeper, which is checked if it
		// is one.
		fields, err := taggedObject(dec, depth-1)
		if err != nil {
			return payload{}, err
		}

		return payload{
			tagged: func() (any, error) { return extFrom(fields) },
			plain: func() (any, error) {
				if depth+1+valueDepth(fields) > maxDepth {
					return nil, errTooDeep
				}

				return fields, nil
			},
		}, nil
	}

	if delim, ok := token.(json.Delim); ok {
		value, err := valueFrom(dec, delim, depth+1)
		if err != nil {
			return payload{}, err
		}

		return payload{
			tagged: func() (any, error) { return nil, fmt.Errorf("%w %v", errUnexpected, delim) },
			plain:  func() (any, error) { return value, nil },
		}, nil
	}

	return payload{
		tagged: func() (any, error) { return untagToken(tag, token) },
		plain:  func() (any, error) { return valueFrom(dec, token, depth+1) },
	}, nil
}

// untagToken is the value of a tag whose payload is a scalar token.
func untagToken(tag string, token json.Token) (any, error) {
	if tag == TagFloat32 {
		return float32From(token)
	}

	text, ok := token.(string)
	if !ok || tag == TagMap || tag == TagExt {
		return nil, fmt.Errorf("%w %v", errUnexpected, token)
	}

	return untag(tag, text)
}

// mapPayload reads the array after a TagMap, once its [ is read, for an
// object at depth: as the pairs of a map, whose keys and values are at the
// depth of its items, and as an array, whose depth is checked if it is one.
func mapPayload(dec *json.Decoder, depth int) (payload, error) {
	items := Map{}
	array := []any{}
	pairs := true

	for dec.More() {
		token, err := dec.Token()
		if err != nil {
			return payload{}, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
		}

		if token != json.Delim('[') {
			pairs = false

			item, err := valueFrom(dec, token, depth+1)
			if err != nil {
				return payload{}, err
			}

			array = append(array, item)

			continue
		}

		// The key and the value of a pair are items of the map.
		pair, err := taggedArray(dec, depth)
		if err != nil {
			return payload{}, err
		}

		entry, _ := pair.([]any)
		if len(entry) == 1+1 {
			items = append(items, MapItem{Key: entry[0], Value: entry[1]})
		} else {
			pairs = false
		}

		array = append(array, entry)
	}

	_, err := dec.Token()
	if err != nil {
		return payload{}, err //nolint:wrapcheck // ParseTaggedJSON wraps it.
	}

	return payload{
		tagged: func() (any, error) {
			if !pairs {
				return nil, fmt.Errorf("%w: pairs of a key and a value are expected", errUnexpected)
			}

			return items, nil
		},
		plain: func() (any, error) {
			// The items of the array are two levels deeper than those of
			// the map they were read as.
			if depth+1+valueDepth(array) > maxDepth {
				return nil, errTooDeep
			}

			return array, nil
		},
	}, nil
}

// valueDepth is the depth of the deepest item of an array or a map, as Decode
// counts it, under the value.
func valueDepth(value any) int {
	deepest := -1

	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			deepest = max(deepest, valueDepth(item))
		}
	case Map:
		for _, item := range typed {
			deepest = max(deepest, valueDepth(item.Key), valueDepth(item.Value))
		}
	default:
		return 0
	}

	return deepest + 1
}

// extFrom is the extension of the fields of a TagExt: its type, an integer
// that fits an int8, and its data, the base64 of its payload.
func extFrom(fields any) (any, error) {
	items, _ := fields.(Map)

	var (
		ext              Ext
		hasType, hasData bool
	)

	for _, item := range items {
		var err error

		switch {
		case item.Key == extKeyType && !hasType:
			ext.Type, hasType = asInt8(item.Value)
			if !hasType {
				err = fmt.Errorf("%w %v: a type is expected", errUnexpected, item.Value)
			}
		case item.Key == extKeyData && !hasData:
			text, _ := item.Value.(string)

			ext.Data, err = base64.StdEncoding.DecodeString(text)
			hasData = err == nil && text == item.Value
		default:
			err = fmt.Errorf("%w %v: {%q, %q} is expected", errUnexpected, item.Key,
				extKeyType, extKeyData)
		}

		if err != nil {
			return nil, err
		}
	}

	if !hasType || !hasData {
		return nil, fmt.Errorf("%w: {%q, %q} is expected", errUnexpected, extKeyType, extKeyData)
	}

	return decodeExt(ext.Type, ext.Data), nil
}

// asInt8 returns an integer that fits an int8.
func asInt8(value any) (int8, bool) {
	if unsigned, ok := value.(uint64); ok && unsigned <= math.MaxInt8 {
		return int8(unsigned), true
	}

	if signed, ok := value.(int64); ok && signed >= math.MinInt8 && signed <= math.MaxInt8 {
		return int8(signed), true
	}

	return 0, false
}

// float32From parses the number of a TagFloat32 as a float32 rather than a
// float64, which may round it otherwise.
func float32From(token json.Token) (any, error) {
	var text string

	switch typed := token.(type) {
	case json.Number:
		text = typed.String()
	case string:
		text = typed
		if !isFloatWord(text) {
			return nil, fmt.Errorf("%q: %w", text, errFloatWord)
		}
	default:
		return nil, fmt.Errorf("%w %v", errUnexpected, token)
	}

	number, err := strconv.ParseFloat(text, float32Bits)
	if err == nil {
		err = checkUnderflow(number, text)
	}

	if err != nil {
		return nil, err
	}

	return float32(number), nil
}

// isFloatWord reports whether text is how a float that is NaN or infinite is
// written.
func isFloatWord(text string) bool {
	return text == textNaN || text == textInf || text == textNegInf
}

// untag is the value of a tag whose payload is a text.
func untag(tag, text string) (any, error) {
	switch tag {
	case TagBinary, TagRawString:
		data, err := base64.StdEncoding.DecodeString(text)
		if err != nil {
			return nil, err //nolint:wrapcheck // taggedTail wraps it.
		}

		if tag == TagRawString {
			return string(data), nil
		}

		return Binary(data), nil
	case TagDouble:
		if !isFloatWord(text) {
			return nil, fmt.Errorf("%q: %w", text, errFloatWord)
		}

		return strconv.ParseFloat(text, float64Bits) //nolint:wrapcheck // taggedTail wraps it.
	case TagDecimal:
		return parseDecimal(text)
	case TagUUID:
		return uuid.Parse(text) //nolint:wrapcheck // taggedTail wraps it.
	case TagDatetime:
		return ParseDatetime(text)
	case TagInterval:
		return ParseInterval(text)
	}

	return nil, errUnknownTag
}

// parseDecimal parses the text of a decimal Tarantool holds.
func parseDecimal(text string) (decimal.Decimal, error) {
	value, err := decimal.NewDecimalFromString(text)
	if err != nil {
		return decimal.Decimal{}, err //nolint:wrapcheck // The callers wrap it.
	}

	if !validDecimal(value) {
		return decimal.Decimal{}, fmt.Errorf("%q: %w", text, errDecimalRange)
	}

	return value, nil
}

// jsonNumber is a number of JSON: an integer is a uint64, or an int64 when
// it is negative, and one too large for them, like a number with a point or
// an exponent, a float64.
func jsonNumber(text string) (any, error) {
	if !strings.ContainsAny(text, ".eE") {
		unsigned, err := strconv.ParseUint(text, decimalBase, 64)
		if err == nil {
			return unsigned, nil
		}

		signed, err := strconv.ParseInt(text, decimalBase, 64)
		if err == nil && signed < 0 {
			return signed, nil
		}

		if err == nil {
			return uint64(0), nil // -0, which ParseUint does not take.
		}
	}

	number, err := strconv.ParseFloat(text, float64Bits)
	if err == nil {
		err = checkUnderflow(number, text)
	}

	if err != nil {
		return nil, err
	}

	return number, nil
}
