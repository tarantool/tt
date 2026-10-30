package app_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/app"
)

func TestParseTargets(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []app.Target
	}{
		{name: "no target", args: nil, want: nil},
		{
			name: "an instance",
			args: []string{"myapp:storage-1"},
			want: []app.Target{{App: "myapp", Instance: "storage-1"}},
		},
		{
			name: "every instance",
			args: []string{"myapp:*"},
			want: []app.Target{{App: "myapp", Instance: app.AllInstances}},
		},
		{
			name: "a list in one argument",
			args: []string{"myapp:storage-1,myapp:storage-2"},
			want: []app.Target{
				{App: "myapp", Instance: "storage-1"},
				{App: "myapp", Instance: "storage-2"},
			},
		},
		{
			name: "several arguments and applications, in order",
			args: []string{"b:router-1", "a:*,b:storage-1"},
			want: []app.Target{
				{App: "b", Instance: "router-1"},
				{App: "a", Instance: app.AllInstances},
				{App: "b", Instance: "storage-1"},
			},
		},
		{
			name: "the instance takes the rest after the first colon",
			args: []string{"myapp:a:b"},
			want: []app.Target{{App: "myapp", Instance: "a:b"}},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := app.ParseTargets(test.args)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestParseTargetsInvalid(t *testing.T) {
	cases := []struct {
		name string
		args []string
		msg  string // A part of the message.
	}{
		{
			name: "bare application",
			args: []string{"myapp"},
			msg:  "use myapp:<INSTANCE> or myapp:*",
		},
		{
			name: "bare application in a list",
			args: []string{"myapp:storage-1,myapp"},
			msg:  "an instance-level command needs an instance target",
		},
		{name: "no short form", args: []string{":storage-1"}, msg: "use <APP>:storage-1"},
		{name: "no instance", args: []string{"myapp:"}, msg: "the instance name is missing"},
		{name: "empty argument", args: []string{""}, msg: "empty target"},
		{name: "empty item", args: []string{"a:x,,a:y"}, msg: "empty target"},
		{name: "trailing comma", args: []string{"a:x,"}, msg: "empty target"},
		{name: "slash in application", args: []string{"../x:y"}, msg: "application name"},
		{name: "dot-dot application", args: []string{"..:y"}, msg: "application name"},
		{name: "star application", args: []string{"*:y"}, msg: "application name"},
		{name: "slash in instance", args: []string{"x:a/b"}, msg: "instance name"},
		{name: "dot instance", args: []string{"x:."}, msg: "instance name"},
		{name: "control character", args: []string{"x:a\tb"}, msg: "instance name"},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := app.ParseTargets(test.args)
			require.ErrorIs(t, err, app.ErrInvalidTarget)
			require.ErrorContains(t, err, test.msg)
			assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
			assert.Nil(t, got)
		})
	}
}

// A connection URI that falls through to the target grammar must not give
// its password to the message, on whichever side of the colon it is cut.
func TestParseTargetsDoNotLeakPassword(t *testing.T) {
	const password = "secretpw"

	for _, arg := range []string{
		"user:" + password + "@127.0.0.1:3311?transport=ssl&ssl_ca_file=/x",
		"user:" + password + "@127.0.0.1:3311/path",
		"tcp://user:" + password + "@127.0.0.1:3311",
		"user:pa/ss" + password + "@host:3301",
		"unix://user:" + password + "@/run/tarantool/s.sock",
		"user:" + password + "@[::1]:3301?transport=ssl",
		"user:" + password + "@bad\tname:3301",
		":" + password + "@host:3301",
		password + "@host:3301/x",
		"user:" + password + "@host:3301,other:" + password + "@host:3302/x",
	} {
		t.Run(arg, func(t *testing.T) {
			_, err := app.ParseTargets([]string{arg})
			require.ErrorIs(t, err, app.ErrInvalidTarget)
			assert.NotContains(t, err.Error(), password)
		})
	}
}

func TestParseScope(t *testing.T) {
	for _, value := range []string{"", "project", "user", "system"} {
		got, err := app.ParseScope(value)
		require.NoError(t, err)
		assert.Equal(t, app.Scope(value), got)
	}

	_, err := app.ParseScope("global")
	require.ErrorIs(t, err, app.ErrUnknownScope)
	require.ErrorContains(t, err, "use project, user or system")
}
