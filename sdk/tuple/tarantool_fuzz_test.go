package tuple_test

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/go-tarantool/v3/datetime"
	"github.com/tarantool/go-tarantool/v3/decimal"

	"github.com/tarantool/tt/sdk/tuple"
)

// luaEvalLoop is the script of the tarantool child: it evaluates each line
// of its input as a Lua expression and writes a line for it, OK with the hex
// of its tostring and of its MessagePack, or ERR with the hex of the error,
// each hex after an x.
const luaEvalLoop = `local msgpack = require('msgpack')
local function hex(s)
    return (s:gsub('.', function(c) return string.format('%02x', c:byte()) end))
end
local function eval(line)
    local fn, err = loadstring('return ' .. line)
    if fn == nil then return 'ERR x' .. hex(tostring(err)) end
    local ok, value = pcall(fn)
    if not ok then return 'ERR x' .. hex(tostring(value)) end
    local encoded, raw = pcall(msgpack.encode, value)
    if not encoded then return 'ERR x' .. hex(tostring(raw)) end
    return 'OK x' .. hex(tostring(value)) .. ' x' .. hex(raw)
end
for line in io.lines() do
    io.write(eval(line), '\n')
    io.flush()
end
os.exit(0)
`

// tarantoolChild is a tarantool that evaluates Lua expressions, one a line,
// for the life of a fuzz target: one process, not one for each input.
type tarantoolChild struct {
	mu  sync.Mutex
	in  io.WriteCloser
	out *bufio.Reader
}

// luaResult is what the child returns for an expression.
type luaResult struct {
	text string
	raw  []byte
	err  string
}

// startTarantool starts the child, or skips the target if there is no
// tarantool in PATH.
func startTarantool(f *testing.F) *tarantoolChild {
	f.Helper()

	path, err := exec.LookPath("tarantool")
	if err != nil {
		f.Skip("tarantool is not found in PATH")
	}

	cmd := exec.CommandContext(f.Context(), path, "-e", luaEvalLoop)

	cmd.Dir = f.TempDir()

	stdin, err := cmd.StdinPipe()
	require.NoError(f, err)

	stdout, err := cmd.StdoutPipe()
	require.NoError(f, err)
	require.NoError(f, cmd.Start())

	f.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Wait()
	})

	const bufferSize = 1 << 20

	return &tarantoolChild{
		mu: sync.Mutex{}, in: stdin, out: bufio.NewReaderSize(stdout, bufferSize),
	}
}

// eval evaluates an expression, which must have no line break.
func (c *tarantoolChild) eval(t *testing.T, expr string) luaResult {
	t.Helper()

	require.NotContains(t, expr, "\n")

	c.mu.Lock()
	defer c.mu.Unlock()

	_, err := io.WriteString(c.in, expr+"\n")
	require.NoError(t, err)

	line, err := c.out.ReadString('\n')
	require.NoError(t, err, "tarantool exited on %q", expr)

	fields := strings.Fields(line)
	require.NotEmpty(t, fields, line)

	decoded := make([][]byte, len(fields)-1)

	// Each hex field starts with an x, so that an empty one is not lost.
	for index, field := range fields[1:] {
		decoded[index], err = hex.DecodeString(strings.TrimPrefix(field, "x"))
		require.NoError(t, err, line)
	}

	if fields[0] == "ERR" {
		require.Len(t, decoded, 1, line)

		return luaResult{text: "", raw: nil, err: string(decoded[0])}
	}

	require.Equal(t, "OK", fields[0], line)
	require.Len(t, decoded, 2, line)

	return luaResult{text: string(decoded[0]), raw: decoded[1], err: ""}
}

// The deepest value FuzzAppendLuaTarantool sends: LuaJIT parses some 200
// levels, as AppendLua documents.
const luaDepth = 100

// The bound of the integers a Lua number holds exactly.
const luaExactInt = 1 << 53

// luaKey is a map key as a Lua table tells it from another one: a number by
// its value, a string, a bool, box.NULL; a cdata or a table, which a key is
// by its identity, is unique.
func luaKey(key any) (string, bool) {
	switch typed := key.(type) {
	case nil:
		return "null", true
	case bool:
		return strconv.FormatBool(typed), true
	case string:
		return "s" + typed, true
	case uint64:
		if typed <= luaExactInt {
			return "n" + strconv.FormatFloat(float64(typed), 'g', -1, 64), true
		}
	case int64:
		if typed >= -luaExactInt {
			return "n" + strconv.FormatFloat(float64(typed), 'g', -1, 64), true
		}
	case float64:
		if typed == 0 {
			typed = 0 // -0 is the key 0.
		}

		return "n" + strconv.FormatFloat(typed, 'g', -1, 64), true
	}

	return "", false
}

// luaExpressible reports whether AppendLua documents that Lua builds value:
// not a map with a NaN key, not a value nested deeper than LuaJIT parses.
func luaExpressible(value any) bool {
	if valueDepth(value) > luaDepth {
		return false
	}

	switch typed := value.(type) {
	case []any:
		return !slices.ContainsFunc(typed, func(item any) bool { return !luaExpressible(item) })
	case tuple.Map:
		for _, item := range typed {
			if number, ok := item.Key.(float64); ok && math.IsNaN(number) {
				return false
			}

			if !luaExpressible(item.Key) || !luaExpressible(item.Value) {
				return false
			}
		}
	}

	return true
}

// luaShape is a value as it compares after a trip through Lua: an integral
// float is the integer Tarantool encodes the Lua number in, a datetime its
// Text, which is all its Lua keeps, and a map is in the order of its tagged
// JSON, since a Lua table has none.
//
//nolint:cyclop // One case per type is the clearest form.
func luaShape(value any) any {
	switch typed := value.(type) {
	case float64:
		const (
			lowest  = -(1 << 63)
			highest = 1 << 64
		)

		if typed == math.Trunc(typed) && typed >= lowest && typed < highest {
			if typed < 0 {
				return int64(typed)
			}

			return uint64(typed)
		}
	case datetime.Datetime:
		text, _ := tuple.Text(typed)

		return "datetime " + text
	case []any:
		items := make([]any, len(typed))
		for index, item := range typed {
			items[index] = luaShape(item)
		}

		return items
	case tuple.Map:
		items := make(tuple.Map, len(typed))
		for index, item := range typed {
			items[index] = tuple.MapItem{Key: luaShape(item.Key), Value: luaShape(item.Value)}
		}

		slices.SortStableFunc(items, func(left, right tuple.MapItem) int {
			return strings.Compare(entryText(left), entryText(right))
		})

		return items
	}

	return value
}

// luaCollapses reports whether value has a map with two keys a Lua table
// takes for one, which AppendLua writes as one entry, the last.
func luaCollapses(value any) bool {
	switch typed := value.(type) {
	case []any:
		return slices.ContainsFunc(typed, luaCollapses)
	case tuple.Map:
		seen := make(map[string]bool, len(typed))

		for _, item := range typed {
			if luaCollapses(item.Key) || luaCollapses(item.Value) {
				return true
			}

			key, ok := luaKey(item.Key)
			if ok && seen[key] {
				return true
			}

			seen[key] = true
		}
	}

	return false
}

// hasEdgeDatetime reports whether value has a datetime at an edge of the
// range: its wall clock may be out of the range datetime.parse takes, or
// what parse builds out of the range go-tarantool decodes.
func hasEdgeDatetime(value any) bool {
	const edge = 2 * 24 * 3600

	switch typed := value.(type) {
	case datetime.Datetime:
		seconds := typed.ToTime().Unix()

		return seconds < minDatetimeSeconds+edge || seconds > maxDatetimeSeconds-edge
	case []any:
		return slices.ContainsFunc(typed, hasEdgeDatetime)
	case tuple.Map:
		return slices.ContainsFunc(typed, func(item tuple.MapItem) bool {
			return hasEdgeDatetime(item.Key) || hasEdgeDatetime(item.Value)
		})
	}

	return false
}

// luaRejected reports whether value has a value Tarantool rejects when it is
// built: an extension of a type Tarantool checks, with a payload that does
// not decode, which Decode keeps as an Ext.
func luaRejected(value any) bool {
	switch typed := value.(type) {
	case tuple.Ext:
		return typed.Type >= tuple.ExtDecimal && typed.Type <= tuple.ExtTuple
	case []any:
		return slices.ContainsFunc(typed, luaRejected)
	case tuple.Map:
		return slices.ContainsFunc(typed, func(item tuple.MapItem) bool {
			return luaRejected(item.Key) || luaRejected(item.Value)
		})
	}

	return false
}

// luaKnownBugs is the skip list of FuzzAppendLuaTarantool: each entry is a
// value of a kind AppendLua documents it does not build exactly.
var luaKnownBugs = []struct {
	name string
	hits func(value any) bool
}{
	{"AppendLua writes a datetime of an ambiguous abbreviation by its offset", hasAmbiguousZone},
}

// ambiguousZones are the zone abbreviations of a datetime of Tarantool its
// datetime.parse takes for ambiguous, and which go-tarantool decodes.
//
//nolint:misspell // ECT is the abbreviation of a zone.
var ambiguousZones = strings.Fields(`ACT ADST ADT AET AMST AMT AST AT AZOST BDT BST BT CDST
	CDT CLST CST CT ECT EDST EDT EST ET FKST GST IDT IRST IST IT MST MT PST PT PYST PYT SST
	WAT WST`)

// hasAmbiguousZone reports whether value has a datetime of a zone
// datetime.parse takes for ambiguous.
func hasAmbiguousZone(value any) bool {
	switch typed := value.(type) {
	case datetime.Datetime:
		return slices.Contains(ambiguousZones, typed.ToTime().Location().String())
	case []any:
		return slices.ContainsFunc(typed, hasAmbiguousZone)
	case tuple.Map:
		return slices.ContainsFunc(typed, func(item tuple.MapItem) bool {
			return hasAmbiguousZone(item.Key) || hasAmbiguousZone(item.Value)
		})
	}

	return false
}

// FuzzAppendLuaTarantool checks that the Lua of a value, evaluated in
// Tarantool, builds the value: as a Lua number keeps it, a Lua table without
// its order, a datetime its Text.
func FuzzAppendLuaTarantool(f *testing.F) {
	child := startTarantool(f)

	for _, seed := range generatorSeeds() {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		value := genValue(t, data)
		if !luaExpressible(value) || luaCollapses(value) {
			return
		}

		if hasEdgeDatetime(value) {
			return
		}

		for _, bug := range luaKnownBugs {
			if bug.hits(value) {
				t.Skip(bug.name)
			}
		}

		expr := string(tuple.AppendLua(nil, value))

		result := child.eval(t, expr)
		if result.err != "" {
			// interval.new takes units in a range, which an interval of
			// MessagePack need not keep to.
			outOfRange := strings.Contains(result.err, "out of allowed range") &&
				strings.Contains(expr, "interval.new")
			require.True(t, luaRejected(value) || outOfRange, "%s: %s", expr, result.err)

			return
		}

		back, err := tuple.Decode(result.raw)
		require.NoError(t, err, "%x", result.raw)

		want, got := luaShape(value), luaShape(back)
		require.True(t, sameValue(want, got), "%s\nwant %s\n got %s", expr, describe(want),
			describe(got))
	})
}

// FuzzTextTarantool checks that the Text of a datetime, an interval and a
// decimal Tarantool builds is the text Tarantool prints, and that
// ParseDatetime reads a text as datetime.parse does where both read it.
func FuzzTextTarantool(f *testing.F) {
	child := startTarantool(f)

	for _, text := range datetimeTexts {
		f.Add(uint8(3), int64(0), int64(0), uint8(0), int16(0), text)
	}

	for index, seconds := range datetimeSeconds {
		small := uint8(index)

		f.Add(uint8(0), seconds, int64(27_000_000), small, int16(-90), "")
		f.Add(uint8(1), seconds, int64(-2), small, int16(3), "")
		f.Add(uint8(2), seconds, int64(index), small, int16(small), "12.340")
		f.Add(uint8(2), seconds, int64(-index*7), small, int16(small), "1E+999")
	}

	f.Fuzz(func(t *testing.T, kind uint8, first, second int64, zone uint8, offset int16,
		text string,
	) {
		switch kind % 4 {
		case 0:
			checkTarantoolDatetime(t, child, first, second, zone, offset)
		case 1:
			checkTarantoolInterval(t, child, first, second, zone, offset)
		case 2:
			checkTarantoolDecimal(t, child, text, first, second)
		default:
			checkTarantoolParse(t, child, text)
		}
	})
}

// checkTarantoolText checks that Tarantool built a value whose Text is what
// Tarantool prints, with suffix added, and returns the value.
func checkTarantoolText(t *testing.T, child *tarantoolChild, expr, suffix string) (any, bool) {
	t.Helper()

	result := child.eval(t, expr)
	if result.err != "" {
		return nil, false
	}

	value, err := tuple.Decode(result.raw)
	require.NoError(t, err, "%x", result.raw)

	text, ok := tuple.Text(value)
	require.True(t, ok, describe(value))

	// go-tarantool has no decimal -0: it is 0.
	want := result.text + suffix
	if number, isDecimal := value.(decimal.Decimal); isDecimal && number.Sign() == 0 {
		want = strings.TrimPrefix(want, "-")
	}

	require.Equal(t, want, text, "%s: %s", expr, describe(value))

	return value, true
}

func checkTarantoolDatetime(t *testing.T, child *tarantoolChild, seconds, nanos int64,
	zone uint8, offset int16,
) {
	t.Helper()

	zoneArg := fmt.Sprintf("tzoffset = %d", int(offset)%(14*60))

	if choice := int(zone) % (len(datetimeZones) + len(zoneAbbreviations) + 1); choice > 0 {
		names := append(slices.Clone(datetimeZones), "")
		for _, abbrev := range zoneAbbreviations {
			names = append(names, abbrev.name)
		}

		zoneArg = "tz = " + strconv.Quote(names[choice-1])
	}

	expr := fmt.Sprintf("require('datetime').new({timestamp = %d, nsec = %d, %s})",
		seconds%maxDatetimeSeconds, (nanos%1_000_000_000+1_000_000_000)%1_000_000_000, zoneArg)

	value, ok := checkTarantoolText(t, child, expr, "")
	if !ok {
		return
	}

	// The texts of the datetime read back as checkDatetimeTexts expects.
	typed, ok := value.(datetime.Datetime)
	if ok && knownBug(typed) == "" {
		checkDatetimeTexts(t, typed)
	}
}

func checkTarantoolInterval(t *testing.T, child *tarantoolChild, first, second int64,
	zone uint8, offset int16,
) {
	t.Helper()

	names := []string{"none", "excess", "last"}
	adjust := names[int(zone)%len(names)]

	expr := fmt.Sprintf("require('datetime').interval.new({year = %d, month = %d, week = %d, "+
		"day = %d, hour = %d, min = %d, sec = %d, nsec = %d, adjust = '%s'})",
		first%11_759_222, second%140_000_000, int64(offset), first%5_000_000, second%10_000_000,
		int64(zone)-128, first%1_000_000_000, second%1_000_000_000, adjust)

	suffix := ""
	if adjust != "none" {
		suffix = ", adjust " + adjust
	}

	_, _ = checkTarantoolText(t, child, expr, suffix)
}

func checkTarantoolDecimal(t *testing.T, child *tarantoolChild, text string, digits,
	exponent int64,
) {
	t.Helper()

	_, err := decimal.NewDecimalFromString(text)
	if err != nil {
		text = strconv.FormatInt(digits, 10) + "E" + strconv.FormatInt(exponent%2_000_000, 10)
	}

	_, _ = checkTarantoolText(t, child, "require('decimal').new("+
		string(tuple.AppendLuaString(nil, text))+")", "")
}

// offsetAndName finds an offset and a zone name after a time.
var offsetAndName = regexp.MustCompile(`\d\d:\d\d(:\d\d([.,]\d*)?)?[+-]\d\d(:?\d\d)? `)

// checkTarantoolParse checks that a text both ParseDatetime and
// datetime.parse read is the same datetime to them, as its Text tells. A
// text of an offset and a zone name, which datetime.parse takes for the
// offset alone, is left out.
func checkTarantoolParse(t *testing.T, child *tarantoolChild, text string) {
	t.Helper()

	if strings.ContainsAny(text, "\n\r") {
		return
	}

	value, err := tuple.ParseDatetime(text)
	if err != nil {
		return
	}

	if offsetAndName.MatchString(text) {
		return
	}

	result := child.eval(t, "require('datetime').parse("+
		string(tuple.AppendLuaString(nil, text))+")")
	if result.err != "" {
		return
	}

	back, err := tuple.Decode(result.raw)
	require.NoError(t, err)

	want, _ := tuple.Text(back)
	got, _ := tuple.Text(value)

	// A wall time in a gap of summer time is normalized as time.Date does.
	if wall, _, ok := textWall(text); want != got && ok && !wall.Equal(wallClock(value.ToTime())) {
		return
	}

	require.Equal(t, want, got, "%q", text)
}
