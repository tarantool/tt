package instances

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// The short tags of the YAML types the decoder tells apart.
const (
	tagNull      = "!!null"
	tagStr       = "!!str"
	tagInt       = "!!int"
	tagFloat     = "!!float"
	tagBool      = "!!bool"
	tagTimestamp = "!!timestamp"
	tagMerge     = "!!merge"
)

// The fields of a record.
const (
	fieldBinary      = "binary"
	fieldScript      = "script"
	fieldArgs        = "args"
	fieldEnv         = "env"
	fieldWorkdir     = "workdir"
	fieldStopSignal  = "stop_signal"
	fieldStopTimeout = "stop_timeout"
	fieldRestart     = "restart"
	// fieldLogrotateSignal is the signal that makes an external process
	// reopen its logs.
	fieldLogrotateSignal = "logrotate_signal"
	// fieldKind names a kind of instance tt knows more of than of an
	// external process: tcm alone.
	fieldKind = "kind"
	// fieldHTTP, fieldConfig and fieldPasswordFile are the fields of the
	// record of TCM.
	fieldHTTP         = "http"
	fieldConfig       = "config"
	fieldPasswordFile = "password_file"
)

// wildcardRefused names, for each field the record "*" cannot have, the kind
// of instance the field makes, as a clause of the message refusing it.
var wildcardRefused = map[string]string{
	fieldBinary:       "is an external process",
	fieldScript:       "runs its script",
	fieldKind:         isTCM,
	fieldHTTP:         isTCM,
	fieldConfig:       isTCM,
	fieldPasswordFile: isTCM,
}

// isTCM is the clause of wildcardRefused for the fields of TCM.
const isTCM = "is TCM"

// tcmOnlyFields are the fields only the record of TCM has, in the order a
// message lists them.
var tcmOnlyFields = []string{fieldHTTP, fieldConfig, fieldPasswordFile}

// tcmRefusedFields are the fields the record of TCM cannot have, and why: tt
// runs the binary of TCM with the config it writes, in the data directory
// of the instance, and TCM logs to its standard output, which goes to
// tt.log.
var tcmRefusedFields = []struct{ field, why string }{
	{fieldBinary, "tt runs the binary of TCM it finds"},
	{fieldScript, "TCM is not a Tarantool instance"},
	{fieldArgs, "tt runs TCM with the config it writes; settings go to the file config names"},
	{fieldWorkdir, "TCM runs in its data directory"},
	{fieldLogrotateSignal, "TCM logs to tt.log, which tt instance logrotate reopens"},
}

// recordFields are what a record holds besides the fields of Instance: the
// fields of TCM, which mean something only together with kind, written in
// any order, and the lines of the fields, for the checks made once the whole
// record is read.
type recordFields struct {
	kind  string
	tcm   TCM
	lines map[string]int
}

// The reasons a node holds no string. Each one completes a sentence about
// the node, such as `field "binary" has no value`.
var (
	errNoValue   = errors.New("has no value")
	errAlias     = errors.New("is an alias, and aliases are not supported")
	errMergeKey  = errors.New("is the merge key <<, and merge keys are not supported")
	errNotString = errors.New("must be a string")
)

// decodeDocument returns the root node of the only YAML document in data. An
// input without a document yields a null node, like an empty document.
func decodeDocument(data []byte) (*yaml.Node, error) {
	dec := yaml.NewDecoder(bytes.NewReader(data))

	var doc yaml.Node

	err := dec.Decode(&doc)
	if errors.Is(err, io.EOF) {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tagNull}, nil
	}

	if err != nil {
		return nil, syntaxError(err, data)
	}

	// Decoding stops after the first document, and yaml.Unmarshal would drop
	// the rest without a word; a second document is a mistake to report.
	var next yaml.Node

	err = dec.Decode(&next)
	if err == nil {
		return nil, &Error{
			Line:     next.Line,
			Instance: "",
			Field:    "",
			Msg:      "a second YAML document; a description is a single one",
		}
	}

	if !errors.Is(err, io.EOF) {
		return nil, syntaxError(err, data)
	}

	// The parser gives a document exactly one child; this guards the index.
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, &Error{Line: doc.Line, Instance: "", Field: "", Msg: "not a YAML document"}
	}

	return doc.Content[0], nil
}

// unnamedAlias is the problem the YAML parser reports for a * that starts no
// alias name, as the * of an unquoted "*" does.
const unnamedAlias = "did not find expected alphabetic or numeric character"

// syntaxLine finds the line the YAML parser puts first in its message, as
// "line 3: ". The parser leaves the first line unnamed.
var syntaxLine = regexp.MustCompile(`^line ([0-9]+): `)

// bareWildcard matches a line that declares the record "*" without quotes,
// in the block style or in a flow mapping.
var bareWildcard = regexp.MustCompile(`(?:^|[{,])[ \t]*\*[ \t]*:`)

// syntaxError turns an error of the YAML parser into an *Error. The parser
// puts the line into its message, which is kept.
//
// A * starts an alias in YAML, so the record "*" written without quotes is
// a syntax error whose message says nothing of the record. It is the one
// mistake every author of "*" is bound to make once, so the message gets a
// hint when the line it names declares *.
func syntaxError(err error, data []byte) error {
	msg := strings.TrimPrefix(err.Error(), "yaml: ")

	if strings.HasSuffix(msg, unnamedAlias) && bareWildcard.Match(lineOf(data, msg)) {
		msg += fmt.Sprintf(`: an unquoted * starts a YAML alias; write %q: in quotes `+
			`for the instances of the topology`, WildcardName)
	}

	return &Error{
		Line:     0,
		Instance: "",
		Field:    "",
		Msg:      msg,
	}
}

// lineOf returns the line of data the message of the YAML parser names.
func lineOf(data []byte, msg string) []byte {
	line := 1

	match := syntaxLine.FindStringSubmatch(msg)
	if match != nil {
		number, err := strconv.Atoi(match[1])
		if err != nil {
			return nil
		}

		line = number
	}

	number := 0

	for text := range bytes.Lines(data) {
		number++

		if number == line {
			return text
		}
	}

	return nil
}

// decoder walks the nodes of a description. It collects the problems it
// finds rather than stopping at the first one, so that all of them are
// reported at once.
type decoder struct {
	appDir string
	errs   []error
}

// failf records a problem.
func (dec *decoder) failf(line int, instance, field, format string, args ...any) {
	dec.errs = append(dec.errs, &Error{
		Line:     line,
		Instance: instance,
		Field:    field,
		Msg:      fmt.Sprintf(format, args...),
	})
}

// description decodes the root node of a description into its records, in
// the order they are written in.
func (dec *decoder) description(root *yaml.Node) Description {
	var desc Description

	if isNull(root) {
		return desc
	}

	if root.Kind != yaml.MappingNode {
		dec.failf(root.Line, "", "", "the description must be a mapping of instance names "+
			"to records, got %s", describe(root))

		return desc
	}

	desc.Instances = make([]Instance, 0, entryCount(root))
	// firstLine is where each name was declared, for the message about a
	// name declared again: the node tree keeps duplicate keys.
	firstLine := make(map[string]int, entryCount(root))
	// tcmName and tcmLine are the first record of TCM, as each application
	// has one TCM.
	tcmName, tcmLine := "", 0

	for key, value := range entries(root) {
		name, ok := dec.name(key)
		if !ok {
			continue
		}

		if line, seen := firstLine[name]; seen {
			what := "instance"
			if name == WildcardName {
				what = "record"
			}

			dec.failf(key.Line, name, "", "the %s is declared twice, first at line %d", what,
				line)

			continue
		}

		firstLine[name] = key.Line

		inst, ok := dec.record(name, key.Line, value)

		switch {
		case !ok:
		case inst.TCM != nil && tcmName != "":
			dec.failf(key.Line, name, fieldKind, "a second record of TCM, after %q at line %d; "+
				"an application has one TCM", tcmName, tcmLine)
		case inst.TCM != nil:
			tcmName, tcmLine = name, key.Line

			desc.Instances = append(desc.Instances, inst)
		case name == WildcardName:
			desc.Wildcard = &Wildcard{At: len(desc.Instances), Fields: inst}
		default:
			desc.Instances = append(desc.Instances, inst)
		}
	}

	return desc
}

// name returns the instance name a key holds. A name ends up as a directory
// under the runtime directories of the application, so it has to be a valid
// file name that does not lead anywhere else.
func (dec *decoder) name(key *yaml.Node) (string, bool) {
	name, err := scalarText(key)
	if err != nil {
		dec.failf(key.Line, "", "", "an instance name %v", err)

		return "", false
	}

	problem := ""

	switch {
	case name == "":
		problem = "is empty"
	case name == "." || name == "..":
		problem = "cannot name a directory"
	case strings.Contains(name, "/"):
		problem = "contains a slash"
	case strings.ContainsFunc(name, unicode.IsControl):
		problem = "contains a control character"
	}

	if problem != "" {
		dec.failf(key.Line, "", "", "the instance name %q %s", name, problem)

		return "", false
	}

	return name, true
}

// record decodes the record of an instance declared at line. It returns
// false when the record has a problem, which it has reported.
func (dec *decoder) record(name string, line int, node *yaml.Node) (Instance, bool) {
	inst := Instance{
		Name:            name,
		Binary:          "",
		Script:          "",
		Args:            nil,
		Env:             nil,
		Workdir:         "",
		StopSignal:      0,
		StopTimeout:     0,
		Restart:         RestartNo,
		LogrotateSignal: 0,
		TCM:             nil,
	}

	switch {
	case isNull(node):
		// A bare name, the way every record of tt2 is written.
		return inst, true
	case node.Kind == yaml.AliasNode:
		dec.failf(node.Line, name, "", "the record %v", errAlias)

		return inst, false
	case node.Kind != yaml.MappingNode:
		dec.failf(node.Line, name, "", "the record must be a mapping of fields, got %s",
			describe(node))

		return inst, false
	}

	failed := len(dec.errs)
	firstLine := make(map[string]int, entryCount(node))
	extra := recordFields{kind: "", tcm: TCM{HTTP: "", Config: "", PasswordFile: ""},
		lines: firstLine}

	for key, value := range entries(node) {
		field, err := scalarText(key)
		if err != nil {
			dec.failf(key.Line, name, "", "a field name %v", err)

			continue
		}

		if first, seen := firstLine[field]; seen {
			dec.failf(key.Line, name, field, "field %q is set twice, first at line %d", field,
				first)

			continue
		}

		firstLine[field] = key.Line

		dec.field(&inst, &extra, field, key, value)
	}

	if inst.Binary != "" && inst.Script != "" {
		dec.failf(line, name, "", "binary and script are mutually exclusive")
	}

	dec.tcmRecord(&inst, &extra)

	return inst, len(dec.errs) == failed
}

// tcmRecord makes inst the record of TCM when its kind is tcm, refusing the
// fields that record cannot have, and refuses the fields of TCM in any other
// record.
func (dec *decoder) tcmRecord(inst *Instance, extra *recordFields) {
	if extra.kind != KindTCMName {
		// A kind that is not one has been reported already.
		_, kindSet := extra.lines[fieldKind]

		for _, field := range tcmOnlyFields {
			if line, set := extra.lines[field]; set && !kindSet {
				dec.failf(line, inst.Name, field, "field %q is only for TCM, a record with "+
					"%s: %s", field, fieldKind, KindTCMName)
			}
		}

		return
	}

	for _, refused := range tcmRefusedFields {
		if line, set := extra.lines[refused.field]; set {
			dec.failf(line, inst.Name, refused.field, "field %q is not allowed in the "+
				"record of TCM: %s", refused.field, refused.why)
		}
	}

	tcm := extra.tcm

	inst.TCM = &tcm
}

// field decodes the value of one field into the instance, or into extra for
// the fields that mean something only with the others of the record.
func (dec *decoder) field(inst *Instance, extra *recordFields, field string,
	key, value *yaml.Node,
) {
	if _, refused := wildcardRefused[field]; refused && inst.Name == WildcardName {
		dec.failf(key.Line, inst.Name, field, "field %q is not allowed: %q stands for "+
			"Tarantool instances configured by the cluster config; an instance that %s "+
			"is declared by its name", field, WildcardName, wildcardRefused[field])

		return
	}

	switch field {
	case fieldBinary:
		inst.Binary = dec.binary(inst.Name, value)
	case fieldScript:
		inst.Script = dec.path(inst.Name, field, value)
	case fieldWorkdir:
		inst.Workdir = dec.path(inst.Name, field, value)
	case fieldArgs:
		inst.Args = dec.args(inst.Name, value)
	case fieldEnv:
		inst.Env = dec.env(inst.Name, value)
	case fieldStopSignal:
		inst.StopSignal = dec.signal(inst.Name, field, value)
	case fieldLogrotateSignal:
		inst.LogrotateSignal = dec.signal(inst.Name, field, value)
	case fieldStopTimeout:
		inst.StopTimeout = dec.stopTimeout(inst.Name, value)
	case fieldRestart:
		inst.Restart = dec.restart(inst.Name, value)
	case fieldKind:
		extra.kind = dec.kind(inst.Name, value)
	case fieldHTTP:
		extra.tcm.HTTP = dec.http(inst.Name, value)
	case fieldConfig:
		extra.tcm.Config, _ = dec.pathText(inst.Name, field, value)
	case fieldPasswordFile:
		extra.tcm.PasswordFile, _ = dec.pathText(inst.Name, field, value)
	default:
		dec.failf(key.Line, inst.Name, field, "unknown field %q", field)
	}
}

// str returns the string a field holds.
func (dec *decoder) str(instance, field string, node *yaml.Node) (string, bool) {
	text, err := scalarText(node)
	if err != nil {
		dec.failf(node.Line, instance, field, "field %q %v", field, err)

		return "", false
	}

	return text, true
}

// path returns the path a field holds, resolved against the application
// directory unless it is absolute.
func (dec *decoder) path(instance, field string, node *yaml.Node) string {
	text, ok := dec.pathText(instance, field, node)
	if !ok {
		return ""
	}

	return dec.resolve(text)
}

// binary returns the executable the binary field holds. A value with a slash
// is a path and is resolved like the other path fields. A value without one
// names a program and is kept as it is: it is looked up when the instance
// starts, in .rocks/bin of the application, where tt builds its executable
// components, and then in PATH, the way a shell and exec look up a command,
// so "binary: python3" runs the python3 of the host rather than a file of
// that name in the application directory.
func (dec *decoder) binary(instance string, node *yaml.Node) string {
	text, ok := dec.pathText(instance, fieldBinary, node)

	switch {
	case !ok:
		return ""
	case strings.ContainsRune(text, '/'):
		return dec.resolve(text)
	case text == "." || text == "..":
		dec.failf(node.Line, instance, fieldBinary,
			"field %q names a directory, not a program", fieldBinary)

		return ""
	default:
		return text
	}
}

// pathText returns the text of a field that holds a path or a program name,
// failing when it cannot be one.
func (dec *decoder) pathText(instance, field string, node *yaml.Node) (string, bool) {
	text, ok := dec.str(instance, field, node)

	switch {
	case !ok:
		return "", false
	case text == "":
		dec.failf(node.Line, instance, field, "field %q is empty", field)

		return "", false
	case hasNUL(text):
		dec.failf(node.Line, instance, field, "field %q contains a NUL byte", field)

		return "", false
	default:
		return text, true
	}
}

// resolve returns a path resolved against the application directory unless
// it is absolute.
func (dec *decoder) resolve(text string) string {
	if filepath.IsAbs(text) {
		return text
	}

	return filepath.Join(dec.appDir, text)
}

// args returns the arguments the args field holds. An empty argument is
// valid: some programs take one.
func (dec *decoder) args(instance string, node *yaml.Node) []string {
	if !dec.expect(instance, fieldArgs, node, yaml.SequenceNode, "a sequence of strings") ||
		len(node.Content) == 0 {
		return nil
	}

	args := make([]string, 0, len(node.Content))

	for index, item := range node.Content {
		text, err := scalarText(item)

		switch {
		case err != nil:
			dec.failf(item.Line, instance, fieldArgs, "item %d of field %q %v", index+1,
				fieldArgs, err)
		case hasNUL(text):
			dec.failf(item.Line, instance, fieldArgs, "item %d of field %q contains a NUL byte",
				index+1, fieldArgs)
		default:
			args = append(args, text)
		}
	}

	return args
}

// env returns the variables the env field holds. An empty value is valid;
// a name cannot be empty or hold "=", which would make the entry set
// another variable than the one written.
func (dec *decoder) env(instance string, node *yaml.Node) map[string]string {
	if !dec.expect(instance, fieldEnv, node, yaml.MappingNode,
		"a mapping of variable names to values") || len(node.Content) == 0 {
		return nil
	}

	env := make(map[string]string, entryCount(node))
	firstLine := make(map[string]int, entryCount(node))

	for key, value := range entries(node) {
		name, err := scalarText(key)
		if err != nil {
			dec.failf(key.Line, instance, fieldEnv, "a variable name in field %q %v", fieldEnv,
				err)

			continue
		}

		switch {
		case name == "":
			dec.failf(key.Line, instance, fieldEnv, "a variable name in field %q is empty",
				fieldEnv)

			continue
		case strings.ContainsAny(name, "=\x00"):
			dec.failf(key.Line, instance, fieldEnv,
				"variable name %q in field %q contains = or a NUL byte", name, fieldEnv)

			continue
		}

		if first, seen := firstLine[name]; seen {
			dec.failf(key.Line, instance, fieldEnv,
				"variable %q is set twice in field %q, first at line %d", name, fieldEnv, first)

			continue
		}

		firstLine[name] = key.Line

		text, err := scalarText(value)

		switch {
		case err != nil:
			dec.failf(value.Line, instance, fieldEnv, "variable %q in field %q %v", name,
				fieldEnv, err)
		case hasNUL(text):
			dec.failf(value.Line, instance, fieldEnv,
				"variable %q in field %q contains a NUL byte", name, fieldEnv)
		default:
			env[name] = text
		}
	}

	return env
}

// signal returns the signal a field, stop_signal or logrotate_signal, names.
func (dec *decoder) signal(instance, field string, node *yaml.Node) syscall.Signal {
	text, ok := dec.str(instance, field, node)
	if !ok {
		return 0
	}

	sig, ok := parseSignal(text)
	if !ok {
		dec.failf(node.Line, instance, field,
			"field %q: %q is not a signal name such as SIGTERM or TERM", field, text)

		return 0
	}

	return sig
}

// stopTimeout returns the duration the stop_timeout field holds.
func (dec *decoder) stopTimeout(instance string, node *yaml.Node) time.Duration {
	text, ok := dec.str(instance, fieldStopTimeout, node)
	if !ok {
		return 0
	}

	timeout, err := time.ParseDuration(text)

	switch {
	case err != nil:
		dec.failf(node.Line, instance, fieldStopTimeout,
			"field %q: %q is not a duration such as 30s", fieldStopTimeout, text)
	case timeout <= 0:
		dec.failf(node.Line, instance, fieldStopTimeout, "field %q: %q is not positive",
			fieldStopTimeout, text)
	default:
		return timeout
	}

	return 0
}

// restart returns the policy the restart field names.
func (dec *decoder) restart(instance string, node *yaml.Node) RestartPolicy {
	text, ok := dec.str(instance, fieldRestart, node)
	if !ok {
		return RestartNo
	}

	switch policy := RestartPolicy(text); policy {
	case RestartNo, RestartOnFailure, RestartAlways:
		return policy
	}

	dec.failf(node.Line, instance, fieldRestart, "field %q: %q is not one of %s, %s and %s",
		fieldRestart, text, RestartNo, RestartOnFailure, RestartAlways)

	return RestartNo
}

// kind returns the kind the kind field names: tcm, the one kind tt knows
// more of than of an external process.
func (dec *decoder) kind(instance string, node *yaml.Node) string {
	text, ok := dec.str(instance, fieldKind, node)
	if !ok {
		return ""
	}

	if text != KindTCMName {
		dec.failf(node.Line, instance, fieldKind, "field %q: %q is not a kind of instance; "+
			"the one kind is %s, for TCM, and a record without kind is a Tarantool "+
			"instance, or an external process with binary", fieldKind, text, KindTCMName)

		return ""
	}

	return text
}

// maxPort is the highest TCP port.
const maxPort = 65535

// http returns the address the http field holds, host:port, with a host and
// a port number.
func (dec *decoder) http(instance string, node *yaml.Node) string {
	text, ok := dec.str(instance, fieldHTTP, node)
	if !ok {
		return ""
	}

	problem := httpProblem(text)
	if problem != "" {
		dec.failf(node.Line, instance, fieldHTTP, "field %q: %q is not an address such as "+
			"127.0.0.1:8080: %s", fieldHTTP, text, problem)

		return ""
	}

	return text
}

// httpProblem says what keeps text from being an address host:port, with a
// host and a port number; empty when nothing does.
func httpProblem(text string) string {
	host, port, err := net.SplitHostPort(text)
	if err != nil {
		return err.Error()
	}

	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > maxPort {
		return fmt.Sprintf("port %q is not a number from 1 to %d", port, maxPort)
	}

	if host == "" {
		return "it names no host; 0.0.0.0 listens on every interface"
	}

	return ""
}

// expect reports whether a field holds a node of the kind and reports what
// the field holds otherwise; want names the kind for the message.
func (dec *decoder) expect(instance, field string, node *yaml.Node, kind yaml.Kind,
	want string,
) bool {
	switch {
	case node.Kind == kind:
		return true
	case isNull(node):
		dec.failf(node.Line, instance, field, "field %q %v", field, errNoValue)
	case node.Kind == yaml.AliasNode:
		dec.failf(node.Line, instance, field, "field %q %v", field, errAlias)
	default:
		dec.failf(node.Line, instance, field, "field %q must be %s, got %s", field, want,
			describe(node))
	}

	return false
}

// scalarText returns the text of a scalar a string is taken from: a scalar
// of one of the plain YAML types, by the text it is written with, so that
// 1.50 stays "1.50". A null has no text; a value with another tag, such as
// !!binary or a local one, has a text that is not the value its author meant.
func scalarText(node *yaml.Node) (string, error) {
	switch node.Kind {
	case yaml.ScalarNode:
	case yaml.AliasNode:
		return "", errAlias
	default:
		return "", fmt.Errorf("%w, got %s", errNotString, describe(node))
	}

	switch node.ShortTag() {
	case tagStr, tagInt, tagFloat, tagBool, tagTimestamp:
		return node.Value, nil
	case tagNull:
		return "", errNoValue
	case tagMerge:
		return "", errMergeKey
	default:
		return "", fmt.Errorf("%w, got %s", errNotString, describe(node))
	}
}

// entryNodes is how many nodes of the content of a mapping node make one of
// its entries: the key and then the value.
const entryNodes = 2

// entries yields the keys and the values of a mapping node in order.
func entries(node *yaml.Node) iter.Seq2[*yaml.Node, *yaml.Node] {
	return func(yield func(key, value *yaml.Node) bool) {
		for i := 0; i+1 < len(node.Content); i += entryNodes {
			if !yield(node.Content[i], node.Content[i+1]) {
				return
			}
		}
	}
}

// entryCount is how many entries a mapping node has.
func entryCount(node *yaml.Node) int {
	return len(node.Content) / entryNodes
}

// isNull reports whether a node is a null: written as nothing, ~ or null.
func isNull(node *yaml.Node) bool {
	return node.Kind == yaml.ScalarNode && node.ShortTag() == tagNull
}

// hasNUL reports whether a string holds a NUL byte, which no path, argument
// or environment variable passed to a process can hold.
func hasNUL(text string) bool {
	return strings.IndexByte(text, 0) >= 0
}

// describe names what a node holds, for a message that says what was
// expected instead.
func describe(node *yaml.Node) string {
	switch node.Kind {
	case yaml.SequenceNode:
		return "a sequence"
	case yaml.MappingNode:
		return "a mapping"
	case yaml.AliasNode:
		return "an alias"
	case yaml.ScalarNode:
		return describeScalar(node)
	default:
		return "a document"
	}
}

// describeScalar names the type of a scalar.
func describeScalar(node *yaml.Node) string {
	switch node.ShortTag() {
	case tagStr:
		return "a string"
	case tagInt:
		return "an integer"
	case tagFloat:
		return "a number"
	case tagBool:
		return "a boolean"
	case tagTimestamp:
		return "a timestamp"
	case tagNull:
		return "no value"
	default:
		return "a value tagged " + node.Tag
	}
}
