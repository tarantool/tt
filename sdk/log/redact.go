package log

import (
	"cmp"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
)

// Redacted is what a secret is replaced with.
const Redacted = "[REDACTED]"

// MinSecretLen is the shortest value a [Redactor] accepts. Redaction replaces
// every occurrence of a value anywhere in a message, so a short value masks
// ordinary text: a one-letter "secret" would mask that letter everywhere, and
// a three-letter one matches common words and path parts. Four characters is
// the shortest a real password or token plausibly is while still rare in
// prose. URI userinfo is masked by shape regardless of its length.
const MinSecretLen = 4

var (
	// uriUserinfo matches "scheme://userinfo@". The userinfo stops at the
	// first character that cannot be in an authority; within it the match runs
	// to the last '@', so an unescaped '@' in a password is masked too.
	uriUserinfo = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.\-]*://)[^/?#\s"'<>]+@`)
	// bareUserinfo matches "user:password@" without a scheme, the way Tarantool
	// URIs are usually written (admin:secret@localhost:3301). It must start
	// the string or follow a separator, and a password is required: a bare
	// "name@" without a scheme reads as an e-mail address, not a credential.
	bareUserinfo = regexp.MustCompile(
		`(^|[\s"'(<\[,;=])[^\s:/?#@"'<>\[\](,;=]+:[^\s/?#@"'<>]+@`,
	)
)

// Redactor masks secrets in log text: every value registered with it, and
// the userinfo of anything that looks like a URI. It is safe for concurrent
// use. The process has one, reached through [Secrets]; tests can make their
// own with [NewRedactor].
type Redactor struct {
	// mu serialises Register; Redact reads only replacer.
	mu     sync.Mutex
	values map[string]struct{}
	// replacer masks every value in values; nil until the first Register.
	replacer atomic.Pointer[strings.Replacer]
}

// NewRedactor returns a Redactor with no registered values. It still masks
// URI userinfo.
func NewRedactor() *Redactor {
	return &Redactor{
		mu:       sync.Mutex{},
		values:   make(map[string]struct{}),
		replacer: atomic.Pointer[strings.Replacer]{},
	}
}

// secrets is the process registry. It is global because a secret becomes
// known wherever it is read - a flag, a config file, an environment
// variable, a credentials prompt - and must be masked wherever it is logged,
// by the one handler the core installs.
//
//nolint:gochecknoglobals // The process-wide secret registry; see above.
var secrets = NewRedactor()

// Secrets returns the process-wide Redactor that [RegisterSecret] adds to and
// the core's log handler applies.
func Secrets() *Redactor {
	return secrets
}

// RegisterSecret adds value to the process-wide registry so that it is
// masked in every log record from then on. It reports whether the value was
// accepted: values shorter than [MinSecretLen] are ignored.
func RegisterSecret(value string) bool {
	return secrets.Register(value)
}

// Redact masks secrets in text with the process-wide registry.
func Redact(text string) string {
	return secrets.Redact(text)
}

// Register adds value to the set this Redactor masks. It reports whether the
// value was accepted: values shorter than [MinSecretLen] are ignored.
func (r *Redactor) Register(value string) bool {
	if len(value) < MinSecretLen {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, ok := r.values[value]; ok {
		return true
	}

	r.values[value] = struct{}{}

	// Longest first: strings.Replacer tries the pairs in argument order, so a
	// registered value that contains another shorter one is masked whole.
	ordered := make([]string, 0, len(r.values))
	for known := range r.values {
		ordered = append(ordered, known)
	}

	slices.SortFunc(ordered, func(a, b string) int {
		return cmp.Or(cmp.Compare(len(b), len(a)), strings.Compare(a, b))
	})

	pairs := make([]string, 0, len(ordered)+len(ordered))
	for _, known := range ordered {
		pairs = append(pairs, known, Redacted)
	}

	r.replacer.Store(strings.NewReplacer(pairs...))

	return true
}

// Redact returns text with every registered value and every URI userinfo
// replaced by [Redacted]. A URI with a scheme loses its whole userinfo, since
// a bare "token@" is as much a secret as "user:password@"; without a scheme
// only the "user:password@" form is recognised.
func (r *Redactor) Redact(text string) string {
	replacer := r.replacer.Load()
	if replacer != nil {
		text = replacer.Replace(text)
	}

	if !strings.Contains(text, "@") {
		return text
	}

	text = uriUserinfo.ReplaceAllString(text, "${1}"+Redacted+"@")

	return bareUserinfo.ReplaceAllString(text, "${1}"+Redacted+"@")
}

// RedactAttr returns attr with its secrets masked. The value is resolved
// first, so a slog.LogValuer is redacted by what it logs as. Strings are
// redacted, groups member by member at any depth, and any other value -
// an error, a struct - by its fmt "%+v" rendering: when that rendering holds
// a secret the value is replaced by the redacted text, otherwise it is kept
// as it is. Numbers, booleans, times and durations pass through.
func (r *Redactor) RedactAttr(attr slog.Attr) slog.Attr {
	value := attr.Value.Resolve()

	switch value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, r.Redact(value.String()))
	case slog.KindGroup:
		members := value.Group()
		redacted := make([]slog.Attr, 0, len(members))

		for _, member := range members {
			redacted = append(redacted, r.RedactAttr(member))
		}

		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(redacted...)}
	case slog.KindAny:
		return slog.Attr{Key: attr.Key, Value: r.redactAny(value)}
	default:
		return slog.Attr{Key: attr.Key, Value: value}
	}
}

// RedactRecord returns a copy of record with its message and attributes
// redacted. Attributes a handler holds from WithAttrs are not part of the
// record; the handler redacts those itself.
func (r *Redactor) RedactRecord(record slog.Record) slog.Record {
	out := slog.NewRecord(record.Time, record.Level, r.Redact(record.Message), record.PC)

	record.Attrs(func(attr slog.Attr) bool {
		out.AddAttrs(r.RedactAttr(attr))

		return true
	})

	return out
}

// redactAny redacts a KindAny value by its fmt rendering.
func (r *Redactor) redactAny(value slog.Value) slog.Value {
	text := fmt.Sprintf("%+v", value.Any())

	redacted := r.Redact(text)
	if redacted == text {
		return value
	}

	return slog.StringValue(redacted)
}

// RedactURL returns raw with its userinfo removed entirely and the remainder
// redacted with the process-wide registry.
//
// url.URL.Redacted is not enough: it masks only a password and keeps a bare
// username, and in https://<token>@host the username is the secret. A raw
// string that does not parse as a URL with userinfo - a Tarantool
// admin:secret@host:3301 parses as an opaque one - is redacted by [Redact].
func RedactURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User == nil {
		return Redact(raw)
	}

	parsed.User = nil

	return Redact(parsed.String())
}

// Secret holds a value that must never reach a log or an output: its fmt
// rendering under every verb, its slog value and its text and JSON encodings
// are all [Redacted]. Only [Secret.Reveal] returns the value.
//
// A Secret does not register its value; call [RegisterSecret] as well when
// the value may also leak through a path that does not hold the Secret, such
// as a URL or an error message.
type Secret struct {
	value string
}

// NewSecret wraps value.
func NewSecret(value string) Secret {
	return Secret{value: value}
}

// Reveal returns the wrapped value.
func (s Secret) Reveal() string {
	return s.value
}

// String returns [Redacted].
func (Secret) String() string {
	return Redacted
}

// GoString returns [Redacted], so %#v does not print the struct.
func (Secret) GoString() string {
	return Redacted
}

// Format writes [Redacted] for every fmt verb. Without it, verbs a Stringer
// does not cover, such as %d, would print the struct and its field.
func (Secret) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, Redacted)
}

// LogValue makes slog log [Redacted].
func (Secret) LogValue() slog.Value {
	return slog.StringValue(Redacted)
}

// MarshalText returns [Redacted]; encoding/json and YAML encoders use it.
func (Secret) MarshalText() ([]byte, error) {
	return []byte(Redacted), nil
}
