package instances_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/internal/instances"
)

// appDir is the application directory the tests resolve paths against. Parse
// reads nothing from it, so it does not have to exist.
const appDir = "/srv/myapp"

// bare is the record of a name written with no fields.
func bare(name string) instances.Instance {
	return instances.Instance{Name: name, Restart: instances.RestartNo}
}

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []instances.Instance
	}{
		{name: "no document", input: ""},
		{name: "only a comment", input: "# no instances yet\n"},
		{name: "empty document", input: "---\n"},
		{name: "null document", input: "~\n"},
		{name: "empty mapping", input: "{}\n"},
		{
			name:  "bare names as in tt2",
			input: "storage-master:\n\nstorage-replica:\n",
			want:  []instances.Instance{bare("storage-master"), bare("storage-replica")},
		},
		{
			name:  "null records written out",
			input: "a: ~\nb: null\n",
			want:  []instances.Instance{bare("a"), bare("b")},
		},
		{
			name:  "empty mapping record",
			input: "storage-1: {}\n",
			want:  []instances.Instance{bare("storage-1")},
		},
		{
			name: "every field of an external process",
			input: `worker-1:
  binary: ./bin/worker
  args: [--port, 8080, ""]
  env:
    LOG_LEVEL: info
    EMPTY: ""
  workdir: var/worker
  stop_signal: SIGINT
  stop_timeout: 1m30s
  logrotate_signal: HUP
  restart: on-failure
`,
			want: []instances.Instance{{
				Name:            "worker-1",
				Binary:          "/srv/myapp/bin/worker",
				Args:            []string{"--port", "8080", ""},
				Env:             map[string]string{"LOG_LEVEL": "info", "EMPTY": ""},
				Workdir:         "/srv/myapp/var/worker",
				StopSignal:      syscall.SIGINT,
				StopTimeout:     90 * time.Second,
				Restart:         instances.RestartOnFailure,
				LogrotateSignal: syscall.SIGHUP,
			}},
		},
		{
			name:  "script",
			input: "legacy-1: {script: init.lua, restart: always}\n",
			want: []instances.Instance{{
				Name: "legacy-1", Script: "/srv/myapp/init.lua", Restart: instances.RestartAlways,
			}},
		},
		{
			name:  "fields of a Tarantool instance",
			input: "storage-1: {restart: no, stop_timeout: 500ms, env: {A: b}}\n",
			want: []instances.Instance{{
				Name: "storage-1", Env: map[string]string{"A": "b"},
				StopTimeout: 500 * time.Millisecond, Restart: instances.RestartNo,
			}},
		},
		{
			name:  "absolute paths are kept as written",
			input: "w: {binary: /opt//bin/../w, workdir: /var/lib/w/}\ns: {script: /x.lua}\n",
			want: []instances.Instance{
				{
					Name: "w", Binary: "/opt//bin/../w", Workdir: "/var/lib/w/",
					Restart: instances.RestartNo,
				},
				{Name: "s", Script: "/x.lua", Restart: instances.RestartNo},
			},
		},
		{
			name:  "relative paths are joined and cleaned",
			input: "w: {binary: ./bin/../tools/w, workdir: .}\n",
			want: []instances.Instance{{
				Name: "w", Binary: "/srv/myapp/tools/w", Workdir: "/srv/myapp",
				Restart: instances.RestartNo,
			}},
		},
		{
			name:  "a binary without a slash is a program name for PATH",
			input: "w: {binary: python3}\n",
			want: []instances.Instance{{
				Name: "w", Binary: "python3", Restart: instances.RestartNo,
			}},
		},
		{
			name:  "a binary with a slash is a path, even without a leading dot",
			input: "w: {binary: bin/gw}\n",
			want: []instances.Instance{{
				Name: "w", Binary: "/srv/myapp/bin/gw", Restart: instances.RestartNo,
			}},
		},
		{
			name:  "a script without a slash is still a path",
			input: "s: {script: init.lua}\n",
			want: []instances.Instance{{
				Name: "s", Script: "/srv/myapp/init.lua", Restart: instances.RestartNo,
			}},
		},
		{
			name:  "a relative path may leave the application directory",
			input: "w: {workdir: ../shared}\n",
			want: []instances.Instance{{
				Name: "w", Workdir: "/srv/shared", Restart: instances.RestartNo,
			}},
		},
		{
			name: "scalars keep the text they are written with",
			input: `w:
  binary: "1"
  args: [1.50, 0755, true, 1e3, 0x1F, 2026-10-25, !!str 7]
  env: {RATIO: 1.50, MODE: 0755, DEBUG: true, COUNTRY: NO, PORT: 8080}
`,
			want: []instances.Instance{{
				Name:   "w",
				Binary: "1",
				Args:   []string{"1.50", "0755", "true", "1e3", "0x1F", "2026-10-25", "7"},
				Env: map[string]string{
					"RATIO": "1.50", "MODE": "0755", "DEBUG": "true", "COUNTRY": "NO",
					"PORT": "8080",
				},
				Restart: instances.RestartNo,
			}},
		},
		{
			name:  "empty args and env",
			input: "w: {binary: w, args: [], env: {}}\n",
			want: []instances.Instance{{
				Name: "w", Binary: "w", Restart: instances.RestartNo,
			}},
		},
		{
			name:  "names keep the text they are written with",
			input: "001:\ntrue:\n1.5:\n'<<':\nstorage 1:\n",
			want: []instances.Instance{
				bare("001"), bare("true"), bare("1.5"), bare("<<"), bare("storage 1"),
			},
		},
		{
			name:  "an anchor nothing refers to",
			input: "a: &a {binary: a}\n",
			want: []instances.Instance{{
				Name: "a", Binary: "a", Restart: instances.RestartNo,
			}},
		},
		{
			name:  "document markers",
			input: "---\na:\n...\n",
			want:  []instances.Instance{bare("a")},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := instances.Parse([]byte(test.input), appDir)
			require.NoError(t, err)
			assert.Nil(t, got.Wildcard)

			if len(test.want) == 0 {
				assert.Empty(t, got.Instances)

				return
			}

			assert.Equal(t, test.want, got.Instances)
		})
	}
}

// TestParseSignals pins both forms of a signal name to the same signal and
// the refusal of everything else, numbers and lower case included.
func TestParseSignals(t *testing.T) {
	valid := map[string]syscall.Signal{
		"SIGTERM": syscall.SIGTERM, "TERM": syscall.SIGTERM,
		"SIGINT": syscall.SIGINT, "INT": syscall.SIGINT,
		"SIGKILL": syscall.SIGKILL, "QUIT": syscall.SIGQUIT,
		"HUP": syscall.SIGHUP, "SIGUSR1": syscall.SIGUSR1, "USR2": syscall.SIGUSR2,
		"WINCH": syscall.SIGWINCH,
	}
	for name, want := range valid {
		t.Run(name, func(t *testing.T) {
			desc, err := instances.Parse(
				[]byte("a: {stop_signal: "+name+", logrotate_signal: "+name+"}\n"), appDir)
			require.NoError(t, err)

			got := desc.Instances
			require.Len(t, got, 1)
			assert.Equal(t, want, got[0].StopSignal)
			assert.Equal(t, want, got[0].LogrotateSignal)
		})
	}

	// PWR is Linux-only and INFO macOS-only: a description runs on both.
	invalid := []string{"sigterm", "Term", "SIG", "SIGSIGTERM", "15", "PWR", "INFO", "TERM "}
	for _, field := range []string{"stop_signal", "logrotate_signal"} {
		for _, name := range invalid {
			t.Run(field+"/"+name, func(t *testing.T) {
				_, err := instances.Parse(fmt.Appendf(nil, "a: {%s: %q}\n", field, name), appDir)
				require.ErrorIs(t, err, instances.ErrInvalid)
				assert.EqualError(t, err, fmt.Sprintf(`line 1: instance "a": field %q: `+
					"%q is not a signal name such as SIGTERM or TERM", field, name))
			})
		}
	}
}

// TestParseKeepsOrder checks the records come out in the order of the
// description, which is not the order of their names, over enough records
// that an order taken from a map would not match by chance.
func TestParseKeepsOrder(t *testing.T) {
	const count = 100

	var input strings.Builder

	want := make([]string, 0, count)

	for index := range count {
		// 37 and 100 are coprime, so this visits every number once, shuffled.
		name := fmt.Sprintf("inst-%03d", index*37%count)

		want = append(want, name)

		switch index % 3 {
		case 0:
			fmt.Fprintf(&input, "%s:\n", name)
		case 1:
			fmt.Fprintf(&input, "%s: {script: init.lua}\n", name)
		default:
			fmt.Fprintf(&input, "%s: {binary: ./bin/w}\n", name)
		}
	}

	desc, err := instances.Parse([]byte(input.String()), appDir)
	require.NoError(t, err)

	names := make([]string, 0, len(desc.Instances))
	for i := range desc.Instances {
		names = append(names, desc.Instances[i].Name)
	}

	assert.Equal(t, want, names)
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name  string
		input string
		// want is the whole message: one line per problem.
		want string
	}{
		{
			name:  "sequence at the top",
			input: "- storage-1\n",
			want: "line 1: the description must be a mapping of instance names to records, " +
				"got a sequence",
		},
		{
			name:  "string at the top",
			input: "storage-1\n",
			want: "line 1: the description must be a mapping of instance names to records, " +
				"got a string",
		},
		{
			name:  "syntax error",
			input: "a: [b\n",
			want:  "line 1: did not find expected ',' or ']'",
		},
		{
			name:  "second document",
			input: "a:\n---\nb:\n",
			want:  "line 2: a second YAML document; a description is a single one",
		},
		{
			name:  "empty second document",
			input: "a:\n---\n",
			want:  "line 2: a second YAML document; a description is a single one",
		},
		{
			name:  "unknown field",
			input: "router:\n  advertise_uri: localhost:3301\n  http_port: 8081\n",
			want: "line 2: instance \"router\": unknown field \"advertise_uri\"\n" +
				"line 3: instance \"router\": unknown field \"http_port\"",
		},
		{
			name:  "field names are case-sensitive",
			input: "a: {Binary: a}\n",
			want:  `line 1: instance "a": unknown field "Binary"`,
		},
		{
			name:  "binary and script",
			input: "a:\n  binary: a\n  script: b.lua\n",
			want:  `line 1: instance "a": binary and script are mutually exclusive`,
		},
		{
			name:  "field without a value",
			input: "a:\n  binary:\n  args: ~\n  env: null\n  restart:\n",
			want: "line 2: instance \"a\": field \"binary\" has no value\n" +
				"line 3: instance \"a\": field \"args\" has no value\n" +
				"line 4: instance \"a\": field \"env\" has no value\n" +
				"line 5: instance \"a\": field \"restart\" has no value",
		},
		{
			name:  "empty paths",
			input: "a: {binary: '', workdir: \"\"}\n",
			want: "line 1: instance \"a\": field \"binary\" is empty\n" +
				"line 1: instance \"a\": field \"workdir\" is empty",
		},
		{
			name:  "binary naming a directory",
			input: "a: {binary: .}\nb: {binary: ..}\n",
			want: "line 1: instance \"a\": field \"binary\" names a directory, not a program\n" +
				"line 2: instance \"b\": field \"binary\" names a directory, not a program",
		},
		{
			name:  "path with a NUL byte",
			input: "a: {script: \"init\\0.lua\"}\n",
			want:  `line 1: instance "a": field "script" contains a NUL byte`,
		},
		{
			name:  "path that is not a string",
			input: "a: {binary: [a], script: {a: b}}\n",
			want: "line 1: instance \"a\": field \"binary\" must be a string, got a sequence\n" +
				"line 1: instance \"a\": field \"script\" must be a string, got a mapping",
		},
		{
			name:  "scalar with a tag that is not a plain type",
			input: "a: {binary: !!binary aGk=, workdir: !path /x}\n",
			want: "line 1: instance \"a\": field \"binary\" must be a string, " +
				"got a value tagged !!binary\n" +
				"line 1: instance \"a\": field \"workdir\" must be a string, " +
				"got a value tagged !path",
		},
		{
			name:  "duration without a unit",
			input: "a: {stop_timeout: 30}\n",
			want:  `line 1: instance "a": field "stop_timeout": "30" is not a duration such as 30s`,
		},
		{
			name:  "duration that is not one",
			input: "a: {stop_timeout: soon}\n",
			want: `line 1: instance "a": field "stop_timeout": "soon" is not a duration ` +
				"such as 30s",
		},
		{
			name:  "zero duration",
			input: "a: {stop_timeout: 0s}\n",
			want:  `line 1: instance "a": field "stop_timeout": "0s" is not positive`,
		},
		{
			name:  "negative duration",
			input: "a: {stop_timeout: -1s}\n",
			want:  `line 1: instance "a": field "stop_timeout": "-1s" is not positive`,
		},
		{
			name:  "unknown restart policy",
			input: "a: {restart: sometimes}\n",
			want: `line 1: instance "a": field "restart": "sometimes" is not one of ` +
				"no, on-failure and always",
		},
		{
			name:  "restart policies are case-sensitive",
			input: "a: {restart: On-Failure}\n",
			want: `line 1: instance "a": field "restart": "On-Failure" is not one of ` +
				"no, on-failure and always",
		},
		{
			name:  "restart as a boolean",
			input: "a: {restart: false}\n",
			want: `line 1: instance "a": field "restart": "false" is not one of ` +
				"no, on-failure and always",
		},
		{
			name:  "args that are not a sequence",
			input: "a: {args: --port 8080}\n",
			want:  `line 1: instance "a": field "args" must be a sequence of strings, got a string`,
		},
		{
			name:  "args with bad items",
			input: "a:\n  args:\n    - ok\n    -\n    - [x]\n    - \"\\0\"\n",
			want: "line 4: instance \"a\": item 2 of field \"args\" has no value\n" +
				"line 5: instance \"a\": item 3 of field \"args\" must be a string, " +
				"got a sequence\n" +
				"line 6: instance \"a\": item 4 of field \"args\" contains a NUL byte",
		},
		{
			name:  "env that is not a mapping",
			input: "a: {env: [A=b]}\n",
			want: `line 1: instance "a": field "env" must be a mapping of variable names ` +
				"to values, got a sequence",
		},
		{
			name: "env with bad names",
			input: "a:\n  env:\n    '': x\n    A=B: c\n    \"N\\0\": x\n    [L]: x\n" +
				"    ~: x\n",
			want: "line 3: instance \"a\": a variable name in field \"env\" is empty\n" +
				"line 4: instance \"a\": variable name \"A=B\" in field \"env\" " +
				"contains = or a NUL byte\n" +
				"line 5: instance \"a\": variable name \"N\\x00\" in field \"env\" " +
				"contains = or a NUL byte\n" +
				"line 6: instance \"a\": a variable name in field \"env\" must be a string, " +
				"got a sequence\n" +
				"line 7: instance \"a\": a variable name in field \"env\" has no value",
		},
		{
			name:  "env with bad values",
			input: "a:\n  env:\n    A:\n    B: {c: d}\n    C: \"\\0\"\n",
			want: "line 3: instance \"a\": variable \"A\" in field \"env\" has no value\n" +
				"line 4: instance \"a\": variable \"B\" in field \"env\" must be a string, " +
				"got a mapping\n" +
				"line 5: instance \"a\": variable \"C\" in field \"env\" contains a NUL byte",
		},
		{
			name:  "variable set twice",
			input: "a:\n  env:\n    A: b\n    A: c\n",
			want: `line 4: instance "a": variable "A" is set twice in field "env", ` +
				"first at line 3",
		},
		{
			name:  "instance declared twice",
			input: "a:\nb:\na: {binary: x}\n",
			want:  `line 3: instance "a": the instance is declared twice, first at line 1`,
		},
		{
			name:  "field set twice",
			input: "a:\n  restart: no\n  restart: always\n",
			want:  `line 3: instance "a": field "restart" is set twice, first at line 2`,
		},
		{
			name:  "record that is not a mapping",
			input: "a: init.lua\nb: [x]\nc: 1\nd: ''\n",
			want: "line 1: instance \"a\": the record must be a mapping of fields, got a string\n" +
				"line 2: instance \"b\": the record must be a mapping of fields, " +
				"got a sequence\n" +
				"line 3: instance \"c\": the record must be a mapping of fields, " +
				"got an integer\n" +
				"line 4: instance \"d\": the record must be a mapping of fields, got a string",
		},
		{
			name:  "alias for a record",
			input: "a: &w {binary: w}\nb: *w\n",
			want:  `line 2: instance "b": the record is an alias, and aliases are not supported`,
		},
		{
			name: "aliases inside a record",
			input: "a: {args: &l [x], env: &e {A: b}, binary: &p w}\n" +
				"b: {args: *l, env: *e}\nc: {binary: *p, args: [*p]}\n",
			want: "line 2: instance \"b\": field \"args\" is an alias, and aliases " +
				"are not supported\n" +
				"line 2: instance \"b\": field \"env\" is an alias, and aliases " +
				"are not supported\n" +
				"line 3: instance \"c\": field \"binary\" is an alias, and aliases " +
				"are not supported\n" +
				"line 3: instance \"c\": item 1 of field \"args\" is an alias, and aliases " +
				"are not supported",
		},
		{
			name:  "merge key",
			input: "a: &w {binary: w}\nb:\n  <<: *w\n  args: [x]\n",
			want: `line 3: instance "b": a field name is the merge key <<, and merge keys ` +
				"are not supported",
		},
		{
			name:  "bad instance names",
			input: "'': {}\n..:\n.:\na/b:\n\"a\\tb\":\n~:\n? [a]\n: {}\n<<: {}\n",
			want: "line 1: the instance name \"\" is empty\n" +
				"line 2: the instance name \"..\" cannot name a directory\n" +
				"line 3: the instance name \".\" cannot name a directory\n" +
				"line 4: the instance name \"a/b\" contains a slash\n" +
				"line 5: the instance name \"a\\tb\" contains a control character\n" +
				"line 6: an instance name has no value\n" +
				"line 7: an instance name must be a string, got a sequence\n" +
				"line 9: an instance name is the merge key <<, and merge keys are not supported",
		},
		{
			name:  "every problem is reported",
			input: "a: {foo: 1, stop_signal: X}\nb:\nc: {restart: maybe}\n",
			want: "line 1: instance \"a\": unknown field \"foo\"\n" +
				"line 1: instance \"a\": field \"stop_signal\": \"X\" is not a signal name " +
				"such as SIGTERM or TERM\n" +
				"line 3: instance \"c\": field \"restart\": \"maybe\" is not one of no, " +
				"on-failure and always",
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

// TestParseErrorLocation checks the location of a problem is there for
// errors.As, not only in the message.
func TestParseErrorLocation(t *testing.T) {
	_, err := instances.Parse([]byte("a:\nb:\n  env:\n    A: x\n    A: y\n"), appDir)

	var problem *instances.Error

	require.ErrorAs(t, err, &problem)
	assert.Equal(t, &instances.Error{
		Line:     5,
		Instance: "b",
		Field:    "env",
		Msg:      `variable "A" is set twice in field "env", first at line 4`,
	}, problem)
}

// TestParseRelativeAppDir checks a relative application directory is the
// caller's mistake, not a problem with the description.
func TestParseRelativeAppDir(t *testing.T) {
	for _, dir := range []string{"", ".", "myapp", "./myapp"} {
		t.Run(dir, func(t *testing.T) {
			got, err := instances.Parse([]byte("a:\n"), dir)
			require.Error(t, err)
			require.NotErrorIs(t, err, instances.ErrInvalid)
			assert.Zero(t, got)
		})
	}
}

func TestInstanceKind(t *testing.T) {
	cases := []struct {
		name string
		inst instances.Instance
		want instances.Kind
	}{
		{name: "empty", inst: bare("a"), want: instances.KindCluster},
		{
			name: "fields without binary or script",
			inst: instances.Instance{Name: "a", Args: []string{"x"}, Workdir: "/w"},
			want: instances.KindCluster,
		},
		{
			name: "script",
			inst: instances.Instance{Name: "a", Script: "/a/init.lua"},
			want: instances.KindScript,
		},
		{
			name: "binary",
			inst: instances.Instance{Name: "a", Binary: "/a/bin/w"},
			want: instances.KindExternal,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, test.inst.Kind())
		})
	}
}

// TestParseRunningFixtures parses the descriptions of the projects the
// integration tests of tt instance start, most of them the instances.yml of
// their tt2 days moved into .templates/default/ as they were: that a tt2
// description is valid as it is, and needs fields added only to use them, is
// the promise of the format.
func TestParseRunningFixtures(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "test", "integration", "running", "*",
		".templates", "default", "instances.yaml"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "the fixtures have moved")

	for _, path := range paths {
		// The path is <project>/.templates/default/instances.yaml.
		project := filepath.Dir(filepath.Dir(filepath.Dir(path)))

		t.Run(filepath.Base(project), func(t *testing.T) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)

			desc, err := instances.Parse(data, appDir)
			require.NoError(t, err)

			got := desc.Instances
			require.NotEmpty(t, got)

			for i := range got {
				assert.Equal(t, bare(got[i].Name), got[i])
				assert.Equal(t, instances.KindCluster, got[i].Kind())
			}
		})
	}
}

// TestErrorMessage pins how the parts of an Error make up its message.
func TestErrorMessage(t *testing.T) {
	cases := []struct {
		err  instances.Error
		want string
	}{
		{err: instances.Error{Msg: "m"}, want: "m"},
		{err: instances.Error{Line: 3, Msg: "m"}, want: "line 3: m"},
		{err: instances.Error{Instance: "a", Field: "f", Msg: "m"}, want: `instance "a": m`},
		{err: instances.Error{Line: 3, Instance: "a", Msg: "m"}, want: `line 3: instance "a": m`},
		{err: instances.Error{Line: 3, Instance: "*", Msg: "m"}, want: `line 3: record "*": m`},
	}

	for _, test := range cases {
		t.Run(test.want, func(t *testing.T) {
			assert.Equal(t, test.want, test.err.Error())
			assert.ErrorIs(t, &test.err, instances.ErrInvalid)
		})
	}
}

// TestParseWildcard pins that "*" comes apart from the records that name
// instances, with its fields and its place among them, in either quoting.
func TestParseWildcard(t *testing.T) {
	cases := []struct {
		name  string
		input string
		names []string // The names of Description.Instances.
		want  instances.Wildcard
	}{
		{
			name:  "alone",
			input: "\"*\":\n",
			names: []string{},
			want:  instances.Wildcard{At: 0, Fields: bare("*")},
		},
		{
			name:  "single quotes and an empty mapping",
			input: "'*': {}\n",
			names: []string{},
			want:  instances.Wildcard{At: 0, Fields: bare("*")},
		},
		{
			name: "between records",
			input: "router-1:\n\"*\":\nworker-1: {binary: ./bin/worker}\n" +
				"legacy-1: {script: x.lua}\n",
			names: []string{"router-1", "worker-1", "legacy-1"},
			want:  instances.Wildcard{At: 1, Fields: bare("*")},
		},
		{
			name:  "last",
			input: "a:\nb:\n\"*\":\n",
			names: []string{"a", "b"},
			want:  instances.Wildcard{At: 2, Fields: bare("*")},
		},
		{
			name: "the fields of a Tarantool instance",
			input: "\"*\":\n  restart: on-failure\n  stop_signal: INT\n  stop_timeout: 10s\n" +
				"  env: {A: b}\n",
			names: []string{},
			want: instances.Wildcard{At: 0, Fields: instances.Instance{
				Name:        "*",
				Env:         map[string]string{"A": "b"},
				StopSignal:  syscall.SIGINT,
				StopTimeout: 10 * time.Second,
				Restart:     instances.RestartOnFailure,
			}},
		},
		{
			// The parser accepts these in every record; whoever builds the
			// instances refuses those a Tarantool instance cannot have.
			name:  "the fields of an external process",
			input: "\"*\": {workdir: var, args: [-x], logrotate_signal: HUP}\n",
			names: []string{},
			want: instances.Wildcard{At: 0, Fields: instances.Instance{
				Name:            "*",
				Args:            []string{"-x"},
				Workdir:         filepath.Join(appDir, "var"),
				Restart:         instances.RestartNo,
				LogrotateSignal: syscall.SIGHUP,
			}},
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got, err := instances.Parse([]byte(test.input), appDir)
			require.NoError(t, err)

			names := make([]string, 0, len(got.Instances))
			for i := range got.Instances {
				names = append(names, got.Instances[i].Name)
			}

			assert.Equal(t, test.names, names, `"*" is no instance`)
			require.NotNil(t, got.Wildcard)
			assert.Equal(t, test.want, *got.Wildcard)
			assert.Equal(t, instances.KindCluster, got.Wildcard.Fields.Kind())
		})
	}
}

// TestWildcardInstance checks the instances "*" stands for get its fields
// under their names, and share none of them with each other.
func TestWildcardInstance(t *testing.T) {
	desc, err := instances.Parse([]byte("\"*\": {restart: always, env: {A: b}, "+
		"stop_timeout: 5s}\n"), appDir)
	require.NoError(t, err)
	require.NotNil(t, desc.Wildcard)

	first := desc.Wildcard.Instance("storage-1")
	second := desc.Wildcard.Instance("storage-2")

	assert.Equal(t, instances.Instance{
		Name:        "storage-1",
		Env:         map[string]string{"A": "b"},
		StopTimeout: 5 * time.Second,
		Restart:     instances.RestartAlways,
	}, first)
	assert.Equal(t, "storage-2", second.Name)

	first.Env["A"] = "c"

	assert.Equal(t, "b", second.Env["A"])
	assert.Equal(t, "b", desc.Wildcard.Fields.Env["A"])
	assert.Equal(t, "*", desc.Wildcard.Fields.Name)
}

func TestParseWildcardErrors(t *testing.T) {
	// hint is what the message about an unquoted * ends with.
	const hint = `did not find expected alphabetic or numeric character: an unquoted * ` +
		`starts a YAML alias; write "*": in quotes for the instances of the topology`

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "binary",
			input: "\"*\":\n  binary: ./bin/worker\n",
			want: `line 2: record "*": field "binary" is not allowed: "*" stands for ` +
				"Tarantool instances configured by the cluster config; an instance that " +
				"is an external process is declared by its name",
		},
		{
			name:  "script",
			input: "\"*\": {script: init.lua, restart: always}\n",
			want: `line 1: record "*": field "script" is not allowed: "*" stands for ` +
				"Tarantool instances configured by the cluster config; an instance that " +
				"runs its script is declared by its name",
		},
		{
			name:  "declared twice",
			input: "\"*\":\na:\n'*': {}\n",
			want:  `line 3: record "*": the record is declared twice, first at line 1`,
		},
		{
			name:  "a problem in a field",
			input: "\"*\": {restart: sometimes}\n",
			want: `line 1: record "*": field "restart": "sometimes" is not one of no, ` +
				"on-failure and always",
		},
		{name: "unquoted on the first line", input: "*:\n", want: hint},
		{name: "unquoted with a record", input: "*: {}\n", want: hint},
		{name: "unquoted with a space", input: "* :\n", want: hint},
		{
			name:  "unquoted after a comment",
			input: "# every instance\nrouter-1:\n*:\n  restart: always\n",
			want:  "line 3: " + hint,
		},
		{name: "unquoted in a flow mapping", input: "{a: {}, *: {}}\n", want: hint},
		{
			// An alias without a name elsewhere gets no hint: it is no "*".
			name:  "an alias without a name in a value",
			input: "a: *\n",
			want:  "did not find expected alphabetic or numeric character",
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
