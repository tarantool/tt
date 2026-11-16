package instances_test

import (
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/instances"
)

func TestParseTCM(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  instances.Instance
	}{
		{
			name:  "bare",
			input: "tcm:\n  kind: tcm\n",
			want: instances.Instance{
				Name: "tcm", Restart: instances.RestartNo, TCM: &instances.TCM{},
			},
		},
		{
			name: "every field",
			input: `manager:
  password_file: secrets/admin
  http: 0.0.0.0:8081
  kind: tcm
  config: tcm.yaml
  env: {TCM_MODE: development}
  restart: on-failure
  stop_signal: SIGINT
  stop_timeout: 10s
`,
			want: instances.Instance{
				Name:        "manager",
				Env:         map[string]string{"TCM_MODE": "development"},
				StopSignal:  syscall.SIGINT,
				StopTimeout: 10 * time.Second,
				Restart:     instances.RestartOnFailure,
				// The paths are kept as written: they are relative to the
				// pair, which the parser does not know.
				TCM: &instances.TCM{
					HTTP: "0.0.0.0:8081", Config: "tcm.yaml", PasswordFile: "secrets/admin",
				},
			},
		},
		{
			name:  "IPv6 address",
			input: "tcm: {kind: tcm, http: \"[::1]:8080\"}\n",
			want: instances.Instance{
				Name: "tcm", Restart: instances.RestartNo,
				TCM: &instances.TCM{HTTP: "[::1]:8080"},
			},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			desc, err := instances.Parse([]byte(test.input), appDir)
			require.NoError(t, err)
			require.Len(t, desc.Instances, 1)
			assert.Equal(t, test.want, desc.Instances[0])
			assert.Equal(t, instances.KindTCM, desc.Instances[0].Kind())
		})
	}
}

func TestParseTCMNextToOthers(t *testing.T) {
	desc, err := instances.Parse([]byte("\"*\": {}\ntcm: {kind: tcm}\nweb: {binary: web}\n"),
		appDir)
	require.NoError(t, err)

	require.NotNil(t, desc.Wildcard)
	assert.Equal(t, 0, desc.Wildcard.At)
	require.Len(t, desc.Instances, 2)
	assert.Equal(t, instances.KindTCM, desc.Instances[0].Kind())
	assert.Equal(t, instances.KindExternal, desc.Instances[1].Kind())
}

func TestParseTCMErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "unknown kind",
			input: "a: {kind: tarantool, http: 127.0.0.1:1}\n",
			want: `line 1: instance "a": field "kind": "tarantool" is not a kind of ` +
				`instance; the one kind is tcm, for TCM, and a record without kind is a ` +
				`Tarantool instance, or an external process with binary`,
		},
		{
			name:  "fields of TCM without kind",
			input: "a:\n  http: 127.0.0.1:8080\n  config: tcm.yaml\n  password_file: s\n",
			want: "line 2: instance \"a\": field \"http\" is only for TCM, a record with " +
				"kind: tcm\n" +
				"line 3: instance \"a\": field \"config\" is only for TCM, a record with " +
				"kind: tcm\n" +
				"line 4: instance \"a\": field \"password_file\" is only for TCM, a record " +
				"with kind: tcm",
		},
		{
			name: "fields TCM cannot have",
			input: "tcm:\n  kind: tcm\n  binary: tcm\n  args: [-c, x]\n  workdir: w\n" +
				"  logrotate_signal: HUP\n",
			want: "line 3: instance \"tcm\": field \"binary\" is not allowed in the record " +
				"of TCM: tt runs the binary of TCM it finds\n" +
				"line 4: instance \"tcm\": field \"args\" is not allowed in the record of " +
				"TCM: tt runs TCM with the config it writes; settings go to the file " +
				"config names\n" +
				"line 5: instance \"tcm\": field \"workdir\" is not allowed in the record " +
				"of TCM: TCM runs in its data directory\n" +
				"line 6: instance \"tcm\": field \"logrotate_signal\" is not allowed in the " +
				"record of TCM: TCM logs to tt.log, which tt instance logrotate reopens",
		},
		{
			name:  "script in TCM",
			input: "tcm: {kind: tcm, script: init.lua}\n",
			want: `line 1: instance "tcm": field "script" is not allowed in the record ` +
				`of TCM: TCM is not a Tarantool instance`,
		},
		{
			name:  "two of TCM",
			input: "tcm:\n  kind: tcm\nother:\n  kind: tcm\n",
			want: `line 3: instance "other": a second record of TCM, after "tcm" at line 1; ` +
				`an application has one TCM`,
		},
		{
			name:  "kind in the wildcard",
			input: "\"*\": {kind: tcm}\n",
			want: `line 1: record "*": field "kind" is not allowed: "*" stands for ` +
				`Tarantool instances configured by the cluster config; an instance that is ` +
				`TCM is declared by its name`,
		},
		{
			name:  "no port",
			input: "tcm: {kind: tcm, http: localhost}\n",
			want: `line 1: instance "tcm": field "http": "localhost" is not an address ` +
				`such as 127.0.0.1:8080: address localhost: missing port in address`,
		},
		{
			name:  "port out of range",
			input: "tcm: {kind: tcm, http: \"127.0.0.1:70000\"}\n",
			want: `line 1: instance "tcm": field "http": "127.0.0.1:70000" is not an ` +
				`address such as 127.0.0.1:8080: port "70000" is not a number from 1 to 65535`,
		},
		{
			name:  "no host",
			input: "tcm: {kind: tcm, http: \":8080\"}\n",
			want: `line 1: instance "tcm": field "http": ":8080" is not an address such ` +
				`as 127.0.0.1:8080: it names no host; 0.0.0.0 listens on every interface`,
		},
		{
			name:  "empty config",
			input: "tcm: {kind: tcm, config: \"\"}\n",
			want:  `line 1: instance "tcm": field "config" is empty`,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := instances.Parse([]byte(test.input), appDir)
			require.ErrorIs(t, err, instances.ErrInvalid)
			require.EqualError(t, err, test.want)
			assert.Zero(t, got)
		})
	}
}
