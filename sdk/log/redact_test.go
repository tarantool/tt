package log_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log"
)

const secretValue = "hunter2-s3cret"

// secretValuer logs as a string that holds the secret.
type secretValuer struct{}

func (secretValuer) LogValue() slog.Value {
	return slog.StringValue("token " + secretValue)
}

// groupValuer logs as a group that holds the secret.
type groupValuer struct{}

func (groupValuer) LogValue() slog.Value {
	return slog.GroupValue(slog.String("password", secretValue), slog.Int("port", 3301))
}

// credentials is a struct a caller might log with slog.Any.
type credentials struct {
	User     string
	Password string
}

func newRedactor(t *testing.T) *log.Redactor {
	t.Helper()

	redactor := log.NewRedactor()
	require.True(t, redactor.Register(secretValue))

	return redactor
}

func TestRedactText(t *testing.T) {
	t.Parallel()

	redactor := newRedactor(t)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"no secret", "nothing to hide", "nothing to hide"},
		{
			"registered value", "password " + secretValue + " leaked",
			"password [REDACTED] leaked",
		},
		{
			"registered value twice", secretValue + "/" + secretValue,
			"[REDACTED]/[REDACTED]",
		},
		{
			"scheme user and password", "GET https://user:pass@host/x failed",
			"GET https://[REDACTED]@host/x failed",
		},
		{
			"scheme bare token", "cloning https://ghp_token@github.com/o/r",
			"cloning https://[REDACTED]@github.com/o/r",
		},
		{
			"etcd endpoint", "etcd: http://root:pw@127.0.0.1:2379/prefix",
			"etcd: http://[REDACTED]@127.0.0.1:2379/prefix",
		},
		{
			"tarantool scheme", "tcp://admin:secret@localhost:3301",
			"tcp://[REDACTED]@localhost:3301",
		},
		{
			"unescaped at in password", "https://u:p@ss@host/",
			"https://[REDACTED]@host/",
		},
		{
			"schemeless user and password", "admin:secret@localhost:3301",
			"[REDACTED]@localhost:3301",
		},
		{
			"schemeless in a sentence", "connecting to admin:secret@host:3301 now",
			"connecting to [REDACTED]@host:3301 now",
		},
		{"schemeless after equals", "uri=admin:pw@host", "uri=[REDACTED]@host"},
		{"quoted uri", `"https://tok@h"`, `"https://[REDACTED]@h"`},
		{"e-mail address", "mail bob@example.com", "mail bob@example.com"},
		{"at in the query", "https://host/p?who=a@b", "https://host/p?who=a@b"},
		{"at in the path", "https://host/@user", "https://host/@user"},
		{"empty userinfo", "https://@host", "https://@host"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, redactor.Redact(tc.in))
		})
	}
}

func TestRedactLongestValueFirst(t *testing.T) {
	t.Parallel()

	redactor := log.NewRedactor()
	require.True(t, redactor.Register("abcd"))
	require.True(t, redactor.Register("abcdefgh"))

	assert.Equal(t, "[REDACTED] [REDACTED]", redactor.Redact("abcdefgh abcd"))
}

func TestRedactIgnoresShortValues(t *testing.T) {
	t.Parallel()

	redactor := log.NewRedactor()

	for _, value := range []string{"", "a", "ab", "abc"} {
		assert.False(t, redactor.Register(value), "%q", value)
	}

	assert.True(t, redactor.Register("abcd"))
	assert.True(t, redactor.Register("abcd"), "registering twice is accepted")
	assert.Equal(t, "a ab abc [REDACTED]", redactor.Redact("a ab abc abcd"))
}

func TestRedactAttr(t *testing.T) {
	t.Parallel()

	redactor := newRedactor(t)
	errPlain := errors.New("dial failed")

	tests := []struct {
		name string
		attr slog.Attr
		want slog.Attr
	}{
		{
			name: "string",
			attr: slog.String("password", secretValue),
			want: slog.String("password", log.Redacted),
		},
		{
			name: "string with uri",
			attr: slog.String("uri", "http://root:pw@etcd:2379"),
			want: slog.String("uri", "http://[REDACTED]@etcd:2379"),
		},
		{
			name: "nested group",
			attr: slog.Group("outer", slog.Int("n", 1),
				slog.Group("inner", slog.String("token", "x "+secretValue))),
			want: slog.Group("outer", slog.Int("n", 1),
				slog.Group("inner", slog.String("token", "x "+log.Redacted))),
		},
		{
			name: "log valuer",
			attr: slog.Any("auth", secretValuer{}),
			want: slog.String("auth", "token "+log.Redacted),
		},
		{
			name: "log valuer resolving to a group",
			attr: slog.Any("auth", groupValuer{}),
			want: slog.Group("auth", slog.String("password", log.Redacted),
				slog.Int("port", 3301)),
		},
		{
			name: "error text",
			attr: slog.Any("err", fmt.Errorf("auth as %s: %w", secretValue, errPlain)),
			want: slog.String("err", "auth as "+log.Redacted+": dial failed"),
		},
		{
			name: "struct",
			attr: slog.Any("creds", credentials{User: "admin", Password: secretValue}),
			want: slog.String("creds", "{User:admin Password:"+log.Redacted+"}"),
		},
		{
			name: "secret type",
			attr: slog.Any("password", log.NewSecret(secretValue)),
			want: slog.String("password", log.Redacted),
		},
		{
			name: "int",
			attr: slog.Int("port", 3301),
			want: slog.Int("port", 3301),
		},
		{
			name: "duration",
			attr: slog.Duration("took", time.Second),
			want: slog.Duration("took", time.Second),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := redactor.RedactAttr(tc.attr)
			assert.True(t, tc.want.Equal(got), "want %v, got %v", tc.want, got)
		})
	}
}

func TestRedactAttrKeepsCleanErrors(t *testing.T) {
	t.Parallel()

	redactor := newRedactor(t)
	errPlain := errors.New("dial failed")

	got := redactor.RedactAttr(slog.Any("err", errPlain))

	assert.Equal(t, slog.KindAny, got.Value.Kind())
	assert.Same(t, errPlain, got.Value.Any())
}

func TestRedactRecord(t *testing.T) {
	t.Parallel()

	redactor := newRedactor(t)
	now := time.Now()

	record := slog.NewRecord(now, slog.LevelWarn, "login with "+secretValue, 42)
	record.AddAttrs(
		slog.String("password", secretValue),
		slog.Group("g", slog.String("uri", "https://tok@host")),
	)

	got := redactor.RedactRecord(record)

	assert.Equal(t, now, got.Time)
	assert.Equal(t, slog.LevelWarn, got.Level)
	assert.Equal(t, uintptr(42), got.PC)
	assert.Equal(t, "login with "+log.Redacted, got.Message)

	var attrs []slog.Attr

	got.Attrs(func(attr slog.Attr) bool {
		attrs = append(attrs, attr)

		return true
	})

	require.Len(t, attrs, 2)
	assert.True(t, slog.String("password", log.Redacted).Equal(attrs[0]))
	assert.True(t, slog.Group("g", slog.String("uri", "https://[REDACTED]@host")).
		Equal(attrs[1]), "%v", attrs[1])
}

func TestRegisterSecretConcurrent(t *testing.T) {
	t.Parallel()

	redactor := log.NewRedactor()

	const writers = 16

	var group sync.WaitGroup

	for i := range writers {
		group.Go(func() {
			assert.True(t, redactor.Register(fmt.Sprintf("concurrent-secret-%02d", i)))
		})
		group.Go(func() {
			_ = redactor.Redact("concurrent-secret-00 and friends")
		})
	}

	group.Wait()

	for i := range writers {
		value := fmt.Sprintf("concurrent-secret-%02d", i)
		assert.Equal(t, log.Redacted, redactor.Redact(value), value)
	}
}

func TestProcessRegistry(t *testing.T) {
	t.Parallel()

	const value = "process-registry-value-7c1f"

	assert.Equal(t, value, log.Redact(value))
	assert.True(t, log.RegisterSecret(value))
	assert.False(t, log.RegisterSecret("abc"))
	assert.Equal(t, "x "+log.Redacted, log.Redact("x "+value))
	assert.Equal(t, "x "+log.Redacted, log.Secrets().Redact("x "+value))
}

func TestRedactURL(t *testing.T) {
	t.Parallel()

	const queryValue = "redact-url-query-value-91ab"

	require.True(t, log.RegisterSecret(queryValue))

	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			"http user and password", "http://user:pass@etcd:2379/prefix",
			"http://etcd:2379/prefix",
		},
		{
			"https bare token", "https://ghp_token@github.com/o/r.git",
			"https://github.com/o/r.git",
		},
		{"etcd with ipv6", "http://root:pw@[::1]:2379", "http://[::1]:2379"},
		{"no userinfo", "https://host/p?q=1", "https://host/p?q=1"},
		{
			"registered value in the query", "https://host/p?token=" + queryValue,
			"https://host/p?token=" + log.Redacted,
		},
		{
			"schemeless tarantool uri", "admin:secret@localhost:3301",
			"[REDACTED]@localhost:3301",
		},
		{"unparseable", "http://u:p@[::1", "http://[REDACTED]@[::1"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, log.RedactURL(tc.in))
		})
	}
}

func TestSecretNeverRendersValue(t *testing.T) {
	t.Parallel()

	secret := log.NewSecret(secretValue)

	assert.Equal(t, secretValue, secret.Reveal())

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q", "%x", "%d", "%10s"} {
		rendered := fmt.Sprintf(verb, secret)
		assert.NotContains(t, rendered, secretValue, verb)
		assert.Contains(t, rendered, log.Redacted, verb)
	}

	encoded, err := json.Marshal(struct {
		Password log.Secret `json:"password"`
	}{Password: secret})
	require.NoError(t, err)
	assert.JSONEq(t, `{"password":"[REDACTED]"}`, string(encoded))

	assert.Equal(t, log.Redacted, slog.AnyValue(secret).Resolve().String())
	assert.NotContains(t, fmt.Sprint([]log.Secret{secret}), secretValue)
}
