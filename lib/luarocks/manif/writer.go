// Package manif reads and writes LuaRocks tree- and rock-manifest files.
//
// The writer is a byte-for-byte reimplementation of upstream
// `luarocks/src/luarocks/persist.lua`'s `save_from_table_to_string`
// pipeline. Goldens under testdata/persist/out are generated from upstream
// (see gen_goldens.sh) and are the canonical regression evidence.
//
// The parser is a small hand-rolled recursive-descent reader for the
// restricted Lua-table-literal subset that persist.lua emits. It deliberately
// does NOT depend on a Lua VM and fails loud on unsupported syntax.
package manif

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Write serializes v to w using upstream luarocks' "assignments mode"
// (`persist.save_from_table_to_string`). The accepted shape of v matches what
// upstream's runtime would have produced from a Lua table:
//
//   - map[string]any → table with string keys
//   - map[any]any    → table with mixed keys (string and int64)
//   - []any          → 1-indexed numeric array
//   - string         → Lua string
//   - bool           → Lua boolean
//   - int / int8..int64 / uint / uint8..uint64 → Lua number (integer form)
//   - float32 / float64 → Lua number (float form)
//
// The top-level value MUST be a table (map or []any). All top-level keys
// must be strings matching `[a-zA-Z_][a-zA-Z0-9_]*` and not a Lua keyword.
// Anything else mirrors upstream's hard error
// ("cannot store '<k>' as a plain key").
//
// Write returns ErrInvalidTopLevelKey for that case, and a wrapped error
// for write failures.
func Write(w io.Writer, v any) error {
	tbl, err := normalizeTable(v)
	if err != nil {
		return fmt.Errorf("manif.Write: top-level value: %w", err)
	}

	bw := &bufWriter{w: w}
	if err := writeTableAsAssignments(bw, tbl); err != nil {
		return err
	}

	return bw.err
}

// ErrInvalidTopLevelKey is returned when Write is called with a table whose
// top-level keys cannot be serialized as bare Lua identifiers.
var ErrInvalidTopLevelKey = errors.New("manif: top-level key is not a valid plain Lua identifier")

// bufWriter is a tiny error-trapping io.Writer wrapper.
type bufWriter struct {
	w   io.Writer
	err error
}

func (b *bufWriter) write(s string) {
	if b.err != nil {
		return
	}

	_, b.err = io.WriteString(b.w, s)
}

// table is the internal canonical form used by the writer/parser.
//
// strKeys is sorted by Go string-comparison (matches Lua default_sort for
// string keys). numKeys is sorted ascending. Entries with non-string,
// non-integer keys are an error — upstream persist supports a richer key
// space but every concrete manifest input only uses these two.
type table struct {
	strKeys []string
	str     map[string]any
	numKeys []int64
	num     map[int64]any
}

func newTable() *table {
	return &table{str: map[string]any{}, num: map[int64]any{}}
}

// normalizeTable converts the various Go shapes Write accepts into the
// internal canonical form.
func normalizeTable(v any) (*table, error) {
	t := newTable()

	switch x := v.(type) {
	case *table:
		return x, nil
	case map[string]any:
		maps.Copy(t.str, x)
	case map[any]any:
		for k, vv := range x {
			switch kk := k.(type) {
			case string:
				t.str[kk] = vv
			case int:
				t.num[int64(kk)] = vv
			case int64:
				t.num[kk] = vv
			default:
				return nil, fmt.Errorf("unsupported key type %T", k)
			}
		}
	case []any:
		for i, vv := range x {
			t.num[int64(i+1)] = vv
		}
	default:
		return nil, fmt.Errorf("expected table-shaped value, got %T", v)
	}

	for k := range t.str {
		t.strKeys = append(t.strKeys, k)
	}

	sort.Strings(t.strKeys)

	for k := range t.num {
		t.numKeys = append(t.numKeys, k)
	}

	slices.Sort(t.numKeys)

	return t, nil
}

// writeTableAsAssignments mirrors persist.lua:154-164.
// At the top level every key MUST be a valid plain Lua identifier.
func writeTableAsAssignments(out *bufWriter, t *table) error {
	if len(t.numKeys) != 0 {
		return fmt.Errorf("%w: numeric key %d present at top level", ErrInvalidTopLevelKey, t.numKeys[0])
	}

	for _, k := range t.strKeys {
		if !isValidPlainKey(k) {
			return fmt.Errorf("%w: %q", ErrInvalidTopLevelKey, k)
		}

		out.write(k)
		out.write(" = ")
		writeValue(out, t.str[k], 0)
		out.write("\n")
	}

	return nil
}

// writeValue mirrors persist.lua:41-62 (`write_value`).
func writeValue(out *bufWriter, v any, level int) {
	switch x := v.(type) {
	case string:
		writeString(out, x)
	case bool:
		if x {
			out.write("true")
		} else {
			out.write("false")
		}
	case nil:
		out.write("nil")
	case float32:
		out.write(formatNumber(float64(x)))
	case float64:
		out.write(formatNumber(x))
	case int:
		out.write(strconv.FormatInt(int64(x), 10))
	case int8:
		out.write(strconv.FormatInt(int64(x), 10))
	case int16:
		out.write(strconv.FormatInt(int64(x), 10))
	case int32:
		out.write(strconv.FormatInt(int64(x), 10))
	case int64:
		out.write(strconv.FormatInt(x, 10))
	case uint:
		out.write(strconv.FormatUint(uint64(x), 10))
	case uint8:
		out.write(strconv.FormatUint(uint64(x), 10))
	case uint16:
		out.write(strconv.FormatUint(uint64(x), 10))
	case uint32:
		out.write(strconv.FormatUint(uint64(x), 10))
	case uint64:
		out.write(strconv.FormatUint(x, 10))
	default:
		nt, err := normalizeTable(v)
		if err != nil {
			if out.err == nil {
				out.err = fmt.Errorf("manif.Write: %w", err)
			}

			return
		}

		writeTable(out, nt, level+1)
	}
}

// writeTable mirrors persist.lua:115-147.
// Key ordering: numeric keys first (ascending), then string keys
// (ascending lexicographic) — matching util.default_sort.
func writeTable(out *bufWriter, t *table, level int) {
	out.write("{")

	sep := "\n"
	indent := true

	emit := func(k any, v any) {
		out.write(sep)

		if indent {
			for range level {
				out.write("   ")
			}
		}

		if _, isNum := k.(int64); !isNum {
			// String key (numeric keys produce no key assignment).
			writeTableKeyAssignment(out, k, level)
		}

		writeValue(out, v, level)

		if _, isNum := v.(float32); isNum {
			sep = ", "
			indent = false

			return
		}

		if _, isNum := v.(float64); isNum {
			sep = ", "
			indent = false

			return
		}

		if isGoInt(v) {
			sep = ", "
			indent = false

			return
		}

		sep = ",\n"
		indent = true
	}

	for _, k := range t.numKeys {
		emit(k, t.num[k])
	}

	for _, k := range t.strKeys {
		emit(k, t.str[k])
	}

	if sep != "\n" {
		out.write("\n")

		for range level - 1 {
			out.write("   ")
		}
	}

	out.write("}")
}

// writeTableKeyAssignment mirrors persist.lua:96-106.
func writeTableKeyAssignment(out *bufWriter, k any, level int) {
	if s, ok := k.(string); ok && isValidPlainKey(s) {
		out.write(s)
	} else {
		out.write("[")
		writeValue(out, k, level)
		out.write("]")
	}

	out.write(" = ")
}

// writeString mirrors persist.lua:45-58.
// Strings containing \r or \n are written as long-bracket strings;
// everything else goes through %q.
func writeString(out *bufWriter, s string) {
	if strings.ContainsAny(s, "\r\n") {
		writeLongBracket(out, s)

		return
	}

	out.write(luaQ(s))
}

// writeLongBracket finds the smallest equals-count k such that "]" + k*"=" + "]"
// does not appear in s + "]". Matches persist.lua:46-55. Note the trailing
// "]" probe is what catches values that legitimately end in "]".
func writeLongBracket(out *bufWriter, s string) {
	equals := 0
	probe := s + "]"

	for {
		closeBracket := "]" + strings.Repeat("=", equals) + "]"
		if !strings.Contains(probe, closeBracket) {
			open := "[" + strings.Repeat("=", equals) + "["
			out.write(open)
			out.write("\n")
			out.write(s)
			out.write(closeBracket)

			return
		}

		equals++
	}
}

// luaQ replicates Lua 5.1 / LuaJIT's string.format("%q", s).
//
// Confirmed against /opt/homebrew/bin/tarantool (LuaJIT 2.1):
//
//   - '"' → \"
//   - '\\' → \\
//   - '\n' → '\' + literal LF
//   - bytes 0x00-0x09, 0x0B-0x0C, 0x0E-0x1F, 0x7F → "\DDD" decimal
//   - bytes 0x20-0x7E (except '"' and '\\') → literal
//   - bytes 0x80-0xFF → literal (LuaJIT does not escape high bytes)
//
// Digit count: minimal needed unless the next byte is an ASCII decimal digit,
// in which case 3 digits are used to prevent re-parsing into a different
// codepoint.
//
// Note: writeString already routes strings containing CR or LF through the
// long-bracket form, so luaQ never sees \n itself in practice — but the rule
// is preserved here for parity with upstream.
// quoteCharsLen accounts for the opening and closing '"' added by luaQ.
const quoteCharsLen = 2

func luaQ(s string) string {
	var b strings.Builder

	b.Grow(len(s) + quoteCharsLen)
	b.WriteByte('"')

	for i := range len(s) {
		c := s[i]

		switch {
		case c == '"' || c == '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		case c == '\n':
			b.WriteByte('\\')
			b.WriteByte('\n')
		case c < 0x20 || c == 0x7F:
			nextIsDigit := i+1 < len(s) && s[i+1] >= '0' && s[i+1] <= '9'

			b.WriteByte('\\')

			if nextIsDigit {
				fmt.Fprintf(&b, "%03d", c)
			} else {
				b.WriteString(strconv.Itoa(int(c)))
			}
		default:
			b.WriteByte(c)
		}
	}

	b.WriteByte('"')

	return b.String()
}

// formatNumber mirrors Lua 5.1 / LuaJIT's tostring on a number.
//
// LuaJIT tostring uses "%.14g" by default. For integer-valued doubles in
// safe-integer range it omits the decimal point (e.g. tostring(3) == "3",
// not "3.0").
func formatNumber(f float64) string {
	if math.IsNaN(f) {
		return "nan"
	}

	if math.IsInf(f, 1) {
		return "inf"
	}

	if math.IsInf(f, -1) {
		return "-inf"
	}

	if f == math.Trunc(f) && math.Abs(f) < 1e16 {
		return strconv.FormatInt(int64(f), 10)
	}

	return strconv.FormatFloat(f, 'g', 14, 64)
}

// isValidPlainKey mirrors persist.lua:64-94.
func isValidPlainKey(s string) bool {
	if s == "" {
		return false
	}

	c := s[0]
	if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && c != '_' {
		return false
	}

	for i := 1; i < len(s); i++ {
		c := s[i]
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}

	return !luaKeywords[s]
}

var luaKeywords = map[string]bool{
	"and": true, "break": true, "do": true, "else": true, "elseif": true,
	"end": true, "false": true, "for": true, "function": true, "goto": true,
	"if": true, "in": true, "local": true, "nil": true, "not": true,
	"or": true, "repeat": true, "return": true, "then": true, "true": true,
	"until": true, "while": true,
}

// isGoInt reports whether v is one of the Go integer types that writeValue
// renders without a decimal point. This drives the "inline-pack" separator
// rule alongside float32/float64.
func isGoInt(v any) bool {
	switch v.(type) {
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return true
	}

	return false
}
