package manif

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Byte values for the C-style escape sequences upstream persist.lua may emit.
const (
	escBell      = 0x07 // \a
	escBackspace = 0x08 // \b
	escFormFeed  = 0x0C // \f
	escLineFeed  = 0x0A // \n
	escCarriage  = 0x0D // \r
	escTab       = 0x09 // \t
	escVTab      = 0x0B // \v
)

const (
	// decimalBase is the radix for parsing decimal escape sequences.
	decimalBase = 10

	// maxByteValue is the largest value a single byte can hold; decimal
	// escapes above it are out of range.
	maxByteValue = 255

	// hexDigitOffset maps a hex letter (a-f / A-F) to its numeric value
	// after subtracting the letter's base: 'a'-'a'+hexDigitOffset == 10.
	hexDigitOffset = 10
)

// Parse reads a serialized manifest (assignments mode) and returns a tree
// of native Go values:
//
//   - tables          → map[string]any (also for tables that mix string and
//     numeric keys; numeric keys are stringified)
//   - pure numeric    → []any when keys are exactly 1..N and dense
//     arrays
//   - Lua strings     → string
//   - Lua numbers     → int64 when integral, else float64
//   - true / false    → bool
//
// A bare nil literal in value position is a hard error, not a dropped entry —
// upstream persist.lua never emits one, so Parse never has to represent it.
//
// Parse intentionally supports only the subset emitted by upstream
// persist.lua: table constructors, string literals (short and long form),
// integer and decimal numbers (with optional leading minus), boolean
// literals, identifier or quoted/bracketed keys, and nested tables.
//
// Anything outside that subset — function literals, control-flow keywords
// other than the boolean literals, expressions, the concat operator, etc.
// — is a hard error: the parser never silently drops input. Every such
// error wraps [ErrParse].
func Parse(b []byte) (any, error) {
	p := &parser{src: b}
	p.skipWS()
	// Top-level form: a sequence of `key = value` assignments. We
	// accumulate into a map[string]any. End-of-input when nothing but
	// whitespace remains.
	out := map[string]any{}

	for p.pos < len(p.src) {
		key, err := p.parseTopLevelKey()
		if err != nil {
			return nil, err
		}

		p.skipWS()

		if !p.consume('=') {
			return nil, p.errorf("expected '=' after key %q", key)
		}

		p.skipWS()

		v, err := p.parseValue()
		if err != nil {
			return nil, err
		}

		out[key] = v

		p.skipWS()
	}

	return out, nil
}

type parser struct {
	src []byte
	pos int
}

func (p *parser) errorf(format string, args ...any) error {
	line, col := p.lineCol()

	return fmt.Errorf("manif.Parse: line %d col %d: %s: %w", line, col, fmt.Sprintf(format, args...), ErrParse)
}

func (p *parser) lineCol() (int, int) {
	line, col := 1, 1

	for i := 0; i < p.pos && i < len(p.src); i++ {
		if p.src[i] == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}

	return line, col
}

func (p *parser) skipWS() {
	for p.pos < len(p.src) {
		c := p.src[p.pos]

		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			p.pos++
		case c == '-' && p.pos+1 < len(p.src) && p.src[p.pos+1] == '-':
			// Persist never emits comments; but the format does support
			// them when humans hand-edit a manifest. Single-line only —
			// long comments are not in the persist surface and would
			// signal something unusual.
			if p.pos+3 < len(p.src) && p.src[p.pos+2] == '[' && (p.src[p.pos+3] == '[' || p.src[p.pos+3] == '=') {
				// We don't accept long comments — that's outside the
				// persist-emitted subset.
				return
			}

			p.pos += 2
			for p.pos < len(p.src) && p.src[p.pos] != '\n' {
				p.pos++
			}
		default:
			return
		}
	}
}

func (p *parser) peek() byte {
	if p.pos >= len(p.src) {
		return 0
	}

	return p.src[p.pos]
}

func (p *parser) consume(c byte) bool {
	if p.pos < len(p.src) && p.src[p.pos] == c {
		p.pos++

		return true
	}

	return false
}

func (p *parser) parseTopLevelKey() (string, error) {
	if !isIdentStart(p.peek()) {
		return "", p.errorf("expected identifier at start of top-level assignment, got %q", string(p.peek()))
	}

	return p.parseIdent(), nil
}

func isIdentStart(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func (p *parser) parseIdent() string {
	start := p.pos

	for p.pos < len(p.src) && isIdentPart(p.src[p.pos]) {
		p.pos++
	}

	return string(p.src[start:p.pos])
}

// parseValue dispatches on the next non-whitespace byte.
func (p *parser) parseValue() (any, error) {
	p.skipWS()

	if p.pos >= len(p.src) {
		return nil, p.errorf("unexpected end of input — expected value")
	}

	c := p.src[p.pos]

	switch {
	case c == '{':
		return p.parseTable()
	case c == '"' || c == '\'':
		return p.parseShortString()
	case c == '[':
		// long bracket string
		s, ok, err := p.tryParseLongBracket()
		if err != nil {
			return nil, err
		}

		if ok {
			return s, nil
		}

		return nil, p.errorf("unexpected '[' (expected long-bracket string)")
	case c == '-' || (c >= '0' && c <= '9'):
		return p.parseNumber()
	case isIdentStart(c):
		start := p.pos

		ident := p.parseIdent()
		switch ident {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "nil":
			// persist never emits a bare nil literal; treat it as a parse
			// error rather than returning an ambiguous (nil, nil).
			return nil, p.errorf("unexpected nil literal in value position")
		}
		// Anything else — function, control-flow, or an expression
		// involving a name — is outside the persist subset.
		p.pos = start

		return nil, p.errorf("unsupported identifier %q in value position (only true/false/nil are accepted)", ident)
	default:
		return nil, p.errorf("unexpected byte %q in value position", string(c))
	}
}

func (p *parser) parseShortString() (string, error) {
	quote := p.src[p.pos]
	p.pos++ // consume quote

	var b strings.Builder

	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == quote {
			p.pos++

			return b.String(), nil
		}

		if c == '\n' {
			return "", p.errorf("unterminated short string (newline before closing quote)")
		}

		if c != '\\' {
			b.WriteByte(c)

			p.pos++

			continue
		}
		// escape
		p.pos++
		if p.pos >= len(p.src) {
			return "", p.errorf("dangling backslash at end of input")
		}

		esc := p.src[p.pos]
		switch esc {
		case 'a':
			b.WriteByte(escBell)

			p.pos++
		case 'b':
			b.WriteByte(escBackspace)

			p.pos++
		case 'f':
			b.WriteByte(escFormFeed)

			p.pos++
		case 'n':
			b.WriteByte(escLineFeed)

			p.pos++
		case 'r':
			b.WriteByte(escCarriage)

			p.pos++
		case 't':
			b.WriteByte(escTab)

			p.pos++
		case 'v':
			b.WriteByte(escVTab)

			p.pos++
		case '\\', '"', '\'':
			b.WriteByte(esc)

			p.pos++
		case '\n':
			b.WriteByte('\n')

			p.pos++
		case 'x':
			// \xHH — Lua 5.2+ syntax. Persist emits decimal, so this
			// is only relevant if a human hand-edited the file.
			if p.pos+2 >= len(p.src) {
				return "", p.errorf(`\x escape requires two hex digits`)
			}

			h1, ok1 := hexDigit(p.src[p.pos+1])

			h2, ok2 := hexDigit(p.src[p.pos+2])

			if !ok1 || !ok2 {
				return "", p.errorf(`\x escape requires two hex digits`)
			}

			// h1,h2 are single hex digits (0-15) so h1*16+h2 is 0-255.
			b.WriteByte(byte(h1*16 + h2))

			p.pos += 3
		default:
			if esc >= '0' && esc <= '9' {
				// Up to three decimal digits.
				n := 0

				digits := 0

				for digits < 3 && p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
					n = n*decimalBase + int(p.src[p.pos]-'0')
					p.pos++
					digits++
				}

				if n > maxByteValue {
					return "", p.errorf("decimal escape out of range: %d", n)
				}

				b.WriteByte(byte(n))

				continue
			}

			return "", p.errorf("unknown escape sequence \\%s", string(esc))
		}
	}

	return "", p.errorf("unterminated short string")
}

func hexDigit(c byte) (int, bool) {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0'), true
	case c >= 'a' && c <= 'f':
		return int(c-'a') + hexDigitOffset, true
	case c >= 'A' && c <= 'F':
		return int(c-'A') + hexDigitOffset, true
	}

	return 0, false
}

// tryParseLongBracket consumes `[=*[ ... ]=*]` if the current position looks
// like one. Returns (value, true, nil) on success, (_, false, nil) if the
// position does not start a long bracket, and (_, _, err) on malformed
// content.
func (p *parser) tryParseLongBracket() (string, bool, error) {
	if p.peek() != '[' {
		return "", false, nil
	}

	save := p.pos
	p.pos++

	equals := 0
	for p.pos < len(p.src) && p.src[p.pos] == '=' {
		equals++
		p.pos++
	}

	if p.pos >= len(p.src) || p.src[p.pos] != '[' {
		// Not a long bracket — restore and let the caller deal.
		p.pos = save

		return "", false, nil
	}

	p.pos++ // consume opening '['

	// Per Lua spec, an immediate newline after the opening bracket is
	// ignored. Persist always writes such a newline.
	if p.pos < len(p.src) && p.src[p.pos] == '\n' {
		p.pos++
	} else if p.pos+1 < len(p.src) && p.src[p.pos] == '\r' && p.src[p.pos+1] == '\n' {
		p.pos += 2
	}

	closeBracket := "]" + strings.Repeat("=", equals) + "]"

	end := strings.Index(string(p.src[p.pos:]), closeBracket)
	if end < 0 {
		return "", false, p.errorf("unterminated long-bracket string")
	}

	val := string(p.src[p.pos : p.pos+end])
	p.pos += end + len(closeBracket)

	return val, true, nil
}

// parseNumber accepts an optional leading '-' followed by decimal digits
// and an optional fractional part. No exponent support — persist emits
// numbers via tostring, which renders integers and small floats without an
// exponent. If we encounter scientific notation we still parse it via
// strconv to stay tolerant of hand-edits.
func (p *parser) parseNumber() (any, error) {
	start := p.pos

	if p.src[p.pos] == '-' {
		p.pos++
	}

	for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
		p.pos++
	}

	isFloat := false
	if p.pos < len(p.src) && p.src[p.pos] == '.' {
		isFloat = true

		p.pos++
		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
	}

	if p.pos < len(p.src) && (p.src[p.pos] == 'e' || p.src[p.pos] == 'E') {
		isFloat = true

		p.pos++
		if p.pos < len(p.src) && (p.src[p.pos] == '+' || p.src[p.pos] == '-') {
			p.pos++
		}

		for p.pos < len(p.src) && p.src[p.pos] >= '0' && p.src[p.pos] <= '9' {
			p.pos++
		}
	}

	lit := string(p.src[start:p.pos])
	if lit == "" || lit == "-" {
		return nil, p.errorf("malformed number %q", lit)
	}

	if !isFloat {
		n, err := strconv.ParseInt(lit, 10, 64)
		if err == nil {
			return n, nil
		}
		// Falls through to float on overflow.
	}

	f, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		return nil, p.errorf("malformed number %q: %v", lit, err)
	}

	return f, nil
}

// parseTable handles both array-style and record-style entries.
//
// Output shape:
//   - If every entry has an integer key 1..N (dense), returns []any.
//   - Otherwise returns map[string]any with numeric keys stringified
//     (matching the loose shape used by upstream manifest data).
func (p *parser) parseTable() (any, error) {
	if !p.consume('{') {
		return nil, p.errorf("expected '{' starting table")
	}

	var entries []entry

	autoIdx := int64(1)

	for {
		p.skipWS()

		if p.consume('}') {
			break
		}

		var ent entry
		// Determine if this is a keyed entry by lookahead.
		switch {
		case p.peek() == '[':
			// bracketed key: [expr] = value
			p.pos++
			p.skipWS()

			key, err := p.parseValue()
			if err != nil {
				return nil, err
			}

			p.skipWS()

			if !p.consume(']') {
				return nil, p.errorf("expected ']' after bracketed key")
			}

			p.skipWS()

			if !p.consume('=') {
				return nil, p.errorf("expected '=' after bracketed key")
			}

			p.skipWS()

			v, err := p.parseValue()
			if err != nil {
				return nil, err
			}

			ent = entry{key: key, value: v}
		case isIdentStart(p.peek()) && p.identKeyLookahead():
			name := p.parseIdent()
			p.skipWS()

			if !p.consume('=') {
				return nil, p.errorf("expected '=' after ident key %q", name)
			}

			p.skipWS()

			v, err := p.parseValue()
			if err != nil {
				return nil, err
			}

			ent = entry{key: name, value: v}
		default:
			v, err := p.parseValue()
			if err != nil {
				return nil, err
			}

			ent = entry{key: autoIdx, value: v}
			autoIdx++
		}

		entries = append(entries, ent)

		p.skipWS()

		if p.consume(',') || p.consume(';') {
			continue
		}

		p.skipWS()

		if p.consume('}') {
			break
		}

		return nil, p.errorf("expected ',' or '}' in table")
	}

	return entriesToValue(entries), nil
}

// identKeyLookahead returns true if the position starts with an identifier
// followed (after whitespace) by '='. Used to distinguish `name = value`
// from a value that happens to be a boolean / nil literal.
func (p *parser) identKeyLookahead() bool {
	save := p.pos

	for p.pos < len(p.src) && isIdentPart(p.src[p.pos]) {
		p.pos++
	}
	// Skip whitespace but not newlines beyond — actually allow any WS.
	for p.pos < len(p.src) {
		c := p.src[p.pos]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			p.pos++

			continue
		}

		break
	}

	isAssign := p.pos < len(p.src) && p.src[p.pos] == '='
	// Distinguish '=' from '==' just in case (persist never emits '==').
	if isAssign && p.pos+1 < len(p.src) && p.src[p.pos+1] == '=' {
		isAssign = false
	}

	p.pos = save

	return isAssign
}

// entriesToValue collapses the entry slice into one of:
//
//   - []any              — pure dense numeric (keys 1..N, no string keys)
//   - map[string]any     — pure string-keyed (no numeric keys)
//   - *table             — mixed numeric + string keys; preserves the
//     distinction so the writer can pack numeric
//     entries inline and emit string entries
//     multi-line, matching persist.lua.
func entriesToValue(entries []entry) any {
	hasNum := false
	hasStr := false
	denseArray := true
	maxIdx := int64(0)

	for _, e := range entries {
		switch k := e.key.(type) {
		case int64:
			hasNum = true

			if k != maxIdx+1 {
				denseArray = false
			}

			maxIdx++
		case string:
			hasStr = true
			denseArray = false
		default:
			_ = k
			denseArray = false
		}
	}

	if hasNum && !hasStr && denseArray {
		arr := make([]any, len(entries))
		for i, e := range entries {
			arr[i] = e.value
		}

		return arr
	}

	if hasNum && hasStr {
		t := newTable()

		for _, e := range entries {
			switch k := e.key.(type) {
			case string:
				if _, ok := t.str[k]; !ok {
					t.strKeys = append(t.strKeys, k)
				}

				t.str[k] = e.value
			case int64:
				if _, ok := t.num[k]; !ok {
					t.numKeys = append(t.numKeys, k)
				}

				t.num[k] = e.value
			default:
				panic(fmt.Sprintf("manif: unexpected key type %T", e.key))
			}
		}

		sort.Strings(t.strKeys)
		slices.Sort(t.numKeys)

		return t
	}

	m := map[string]any{}

	for _, e := range entries {
		switch k := e.key.(type) {
		case string:
			m[k] = e.value
		case int64:
			m[strconv.FormatInt(k, 10)] = e.value
		case float64:
			m[strconv.FormatFloat(k, 'g', -1, 64)] = e.value
		default:
			panic(fmt.Sprintf("manif: unexpected key type %T", e.key))
		}
	}

	return m
}

type entry struct {
	key   any
	value any
}

// ErrParse is wrapped by every error Parse returns; test for it with
// errors.Is(err, ErrParse).
var ErrParse = errors.New("parse error")
