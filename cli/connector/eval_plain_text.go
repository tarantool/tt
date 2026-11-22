package connector

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"regexp"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	lua "github.com/yuin/gopher-lua"
	"go.yaml.in/yaml/v3"

	"github.com/tarantool/tt/sdk/formatter"
	"github.com/tarantool/tt/v3/cli/util"
)

var (
	errConnectionWasClosed    = errors.New("connection was closed")
	errExpectedOneResultFound = errors.New("expected one result, found ")
)

const (
	startOfYamlOutput = "---\n"

	endOfYAMLOutput = "\n...\n"
	endOfLuaOutput  = ";"

	tagPushPrefixYAML = `%TAG`
	tagPushPrefixLua  = `-- Push`

	decodeBufferSize = 256
	pushLineParts    = 2
)

type evaluationError string

func (err evaluationError) Error() string {
	return string(err)
}

// chunkPosition matches the position Lua puts in front of an error raised
// at level 1 in the chunk the console runs for an eval, the whole
// eval_func_template.lua on one line:
// `[string "local function func(...) local sharding = req..."]:1: `. It
// names no place a reader can look up.
var chunkPosition = regexp.MustCompile(`^\[string "local function func\(\.\.\.\).*?"\]:\d+: `)

// newEvaluationError returns the error an eval over the console ended with,
// without the position in the chunk of the eval.
func newEvaluationError(message string) evaluationError {
	return evaluationError(chunkPosition.ReplaceAllString(message, ""))
}

type EvalPlainTextOpts struct {
	ReadTimeout  time.Duration
	PushCallback func(any)
	ResData      any
}

type PlainTextEvalRes struct {
	DataEncBase64 string `yaml:"data_enc"`
}

// evalPlainTextConnYAML calls function on Tarantool instance
// Function should return `any`, `string` (res, err)
// to be correctly processed.
func evalPlainTextConn(conn net.Conn, funcBody string, args []any,
	opts EvalPlainTextOpts,
) ([]any, error) {
	err := formatAndSendEvalFunc(conn, funcBody, args, evalFuncTmpl)
	if err != nil {
		return nil, err
	}

	// recv from socket.
	resBytes, err := readFromPlainTextConn(conn, opts)
	if errors.Is(err, io.EOF) {
		return nil, err
	}

	if err != nil {
		return nil, fmt.Errorf("failed to check returned data: %w", err)
	}

	data, err := processEvalTarantoolRes(resBytes, opts.ResData)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func formatAndSendEvalFunc(conn net.Conn, funcBody string, args []any,
	evalFuncTmpl string,
) error {
	if args == nil {
		args = []any{}
	}

	argsEncoded, err := msgpack.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to encode args: %w", err)
	}

	evalFunc, err := util.GetTextTemplatedStr(&evalFuncTmpl, map[string]string{
		"FunctionBody": funcBody,
		"ArgsEncoded":  hex.EncodeToString(argsEncoded),
	})
	if err != nil {
		return fmt.Errorf("failed to instantiate eval function template: %w", err)
	}

	evalFuncFormatted := strings.Join(
		strings.Split(strings.TrimSpace(evalFunc), "\n"), " ",
	)

	evalFuncFormatted = strings.Join(strings.Fields(evalFuncFormatted), " ") + "\n"

	// write to socket.
	err = writeToPlainTextConn(conn, evalFuncFormatted)
	if err != nil {
		return fmt.Errorf("failed to send eval function to socket: %w", err)
	}

	return nil
}

func writeToPlainTextConn(conn net.Conn, data string) error {
	writer := bufio.NewWriter(conn)

	_, err := writer.WriteString(data)
	if err != nil {
		return fmt.Errorf("failed to send to socket: %w", err)
	}

	err = writer.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush: %w", err)
	}

	return nil
}

// readFromPlainTextConn function reads plain text from Tarantool connection
// These code was inspired by Tarantool console eval
// https://github.com/tarantool/tarantool/blob/3bc4a156e937102f23e2157ef88aa6c007759005/
// src/box/lua/console.lua#L469
//
// By default, Tarantool sends YAML-encoded values as user command response.
// In this case the end of output value is `\n...\n`.
// What about a case when return string contains this substring?
// Everything is OK, since yaml-encoded string is indented via two spaces,
// so in fact we never have `\n...\n` in output strings.
//
// E.g.
// tarantool> return '\n...\n'
// ---
// - '
//
//	...
//
//	'
//
// ...
//
// If Lua output is set, the end of input is just ";".
// And there are some problems.
// See https://github.com/tarantool/tarantool/issues/4603
//
// Code is read byte by byte to make parsing output simpler
// (in case of box.session.push() response we need to read 2 yaml-encoded values,
// it's not enough to catch end of output, we should be sure that only one
// yaml-encoded value was read).
func readFromPlainTextConn(conn net.Conn, opts EvalPlainTextOpts) ([]byte, error) {
	var dataBytes []byte

	buffer := bytes.Buffer{}

	for {
		// data is read in cycle because of `box.session.push` command
		// it prints a tag and returns pushed value, and then true is returned additionally
		// e.g.
		// myapp.router> box.session.push('xx')
		// %TAG !push! tag:tarantool.io/push,2018
		// --- xx
		// ...
		// ---
		// - true
		// ...
		//
		// So, when data portion starts with a tag prefix, we have to read one more value
		// received tag string can be handled via pushCallback function.
		//
		dataPortionBytes, err := readDataPortionFromPlainTextConn(conn, &buffer, opts.ReadTimeout)
		if errors.Is(err, io.EOF) {
			return nil, err
		}

		if err != nil {
			return nil, fmt.Errorf("failed to read from instance socket: %w", err)
		}

		dataPortion := string(dataPortionBytes)

		if !pushTagIsReceived(dataPortion) {
			dataBytes = dataPortionBytes
			break
		}

		if opts.PushCallback != nil {
			var pushedData any

			pushedData, err := getPushedData(dataPortionBytes)
			if err != nil {
				return nil, err
			}

			opts.PushCallback(pushedData)
		}
	}

	return dataBytes, nil
}

func readDataPortionFromPlainTextConn(conn net.Conn, buffer *bytes.Buffer,
	readTimeout time.Duration,
) ([]byte, error) {
	tmp := make([]byte, decodeBufferSize)
	data := make([]byte, 0)

	if readTimeout > 0 {
		_ = conn.SetReadDeadline(time.Now().Add(readTimeout))
	} else {
		_ = conn.SetReadDeadline(time.Time{})
	}

	hasYAMLOutputPrefix := false

	for {
		// We have to read from socket in medium parts (not in small parts, like 1 byte),
		// this greatly speeds up reading and responsiveness.
		//
		// But, due to the peculiarities of `box.session.push()`, we have to process the
		// data byte by byte - we use HasPrefix and HasSuffix methods.
		//
		// In addition, when reading in portion (more than 1 byte), we need
		// to save the read data (for this we use the bytes.Buffer), since we can read it,
		// but not process them in this function call (see examples above).
		// This structure allows us to save this data and process it in the next function call.
		//
		if buffer.Len() == 0 {
			readLen, err := conn.Read(tmp)

			switch {
			case err != nil && !errors.Is(err, io.EOF):
				return nil, fmt.Errorf("failed to read: %w", err)
			case readLen == 0 || errors.Is(err, io.EOF):
				return nil, io.EOF
			default:
				buffer.Write(tmp[:readLen])
			}
		}

		nextByte, err := buffer.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("failed to get byte from buffer: %w", err)
		}

		data = append(data, nextByte)

		// The checks look at the ends of data only: data grows a byte at a
		// time, and a check of the whole of it at every byte made the read
		// of an answer quadratic in its size, minutes for a megabyte.
		if isPrefixOf(data, endOfYAMLOutput) ||
			isPrefixOf(data, tagPushPrefixYAML) ||
			isPrefixOf(data, tagPushPrefixLua) {
			continue
		}

		if !hasYAMLOutputPrefix &&
			hasPrefix(data, startOfYamlOutput) ||
			hasPrefix(data, tagPushPrefixYAML) {
			hasYAMLOutputPrefix = true
		}

		if hasYAMLOutputPrefix && hasSuffix(data, endOfYAMLOutput) {
			break
		}

		if hasSuffix(data, endOfLuaOutput) {
			break
		}
	}

	if len(data) == 0 {
		return nil, errConnectionWasClosed
	}

	return data, nil
}

// isPrefixOf reports whether data is a prefix of marker, so that the bytes
// that follow may make marker of it.
func isPrefixOf(data []byte, marker string) bool {
	return len(data) <= len(marker) && marker[:len(data)] == string(data)
}

// hasPrefix reports whether data begins with prefix.
func hasPrefix(data []byte, prefix string) bool {
	return len(data) >= len(prefix) && string(data[:len(prefix)]) == prefix
}

// hasSuffix reports whether data ends with suffix.
func hasSuffix(data []byte, suffix string) bool {
	return len(data) >= len(suffix) && string(data[len(data)-len(suffix):]) == suffix
}

func pushTagIsReceived(dataPortion string) bool {
	if strings.HasPrefix(dataPortion, tagPushPrefixYAML) {
		return true
	}

	if strings.HasPrefix(dataPortion, tagPushPrefixLua) {
		return true
	}

	return false
}

func getPushedData(pushedDataBytes []byte) (any, error) {
	var pushedData any

	pushedDataString := string(pushedDataBytes)

	if strings.HasPrefix(pushedDataString, tagPushPrefixYAML) {
		// YAML - just decode tag.
		err := formatter.DecodeYAML(pushedDataBytes, &pushedData)
		if err != nil {
			return nil, fmt.Errorf("failed to unmarshal pushed data: %w", err)
		}
	} else {
		// Lua.

		// remove first line (-- Push).
		pushedDataString = strings.SplitN(pushedDataString, "\n", pushLineParts)[1]
		// remove ";".
		pushedDataString = strings.TrimRight(pushedDataString, ";")

		pushedData = pushedDataString
	}

	return pushedData, nil
}

func processEvalTarantoolRes(resBytes []byte, result any) ([]any, error) {
	var (
		err                 error
		evalResultEncBase64 string
	)

	var getResultEncBase64Func func([]byte) (string, error)

	// Result data is returned as a table
	// `{ data_enc = msgpack.encode(ret):hex() }`.
	// It can't be returned as a string because of Lua output -
	// error is showed as a string, so the returned table allows to
	// distinguish returned string data from the error.
	//
	// YAML output:
	// tarantool> return 'XXX'
	// ---
	// - 'XXX'
	// ...
	//
	// tarantool> error('XXX')
	// ---
	// - error: XXX
	// ...
	//
	// Lua output:
	// tarantool> return 'XXX'
	// "XXX";
	// tarantool> error('XXX')
	// "XXX";.

	resString := string(resBytes)
	if strings.HasPrefix(resString, startOfYamlOutput) {
		getResultEncBase64Func = getPlainTextEvalResYaml
	} else {
		getResultEncBase64Func = getPlainTextEvalResLua
	}

	evalResultEncBase64, err = getResultEncBase64Func(resBytes)
	if err != nil {
		return nil, err
	}

	dataEnc, err := base64.StdEncoding.DecodeString(evalResultEncBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex value: %w", err)
	}

	if result != nil {
		err = msgpack.Unmarshal(dataEnc, result)
		if err != nil {
			return nil, fmt.Errorf("failed to parse eval result: %w", err)
		}

		return nil, nil
	}

	var data []any

	err = msgpack.Unmarshal(dataEnc, &data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse eval result: %w", err)
	}

	return data, nil
}

func getPlainTextEvalResYaml(resBytes []byte) (string, error) {
	evalResults := []PlainTextEvalRes{}

	err := unmarshalStrict(resBytes, &evalResults)
	if err != nil {
		return "", getPlainTextEvalError(resBytes, err)
	}

	if len(evalResults) != 1 {
		return "", fmt.Errorf("%w%d", errExpectedOneResultFound, len(evalResults))
	}

	evalResult := evalResults[0]

	return evalResult.DataEncBase64, nil
}

// unmarshalStrict decodes the first YAML document of data into out and fails
// on a field out does not have and on a repeated key. No document leaves out
// as it is.
func unmarshalStrict(data []byte, out any) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	err := decoder.Decode(out)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("%w", err)
	}

	return nil
}

func getPlainTextEvalError(resBytes []byte, parseErr error) error {
	errorStrings := make([]map[string]string, 0)

	err := unmarshalStrict(resBytes, &errorStrings)
	if err != nil {
		return fmt.Errorf("failed to parse eval result: %w", parseErr)
	}

	if len(errorStrings) == 0 {
		return fmt.Errorf("failed to parse eval result: %w", parseErr)
	}

	errStr, found := errorStrings[0]["error"]
	if !found {
		return fmt.Errorf("failed to parse eval result: %w", parseErr)
	}

	return newEvaluationError(errStr)
}

func getPlainTextEvalResLua(resBytes []byte) (string, error) {
	luaState := lua.NewState()
	defer luaState.Close()

	doString := fmt.Sprintf(`res = %s`, resBytes)

	err := luaState.DoString(doString)
	if err != nil {
		return "", fmt.Errorf("failed to parse eval result: %w", err)
	}

	luaRes := luaState.Env.RawGetString("res")

	if luaRes.Type() == lua.LTString {
		return "", newEvaluationError(lua.LVAsString(luaRes))
	}

	encodedDataLV := luaState.GetTable(luaRes, lua.LString("data_enc"))

	encodedData := lua.LVAsString(encodedDataLV)

	return encodedData, nil
}
