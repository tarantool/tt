package connector

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"

	"github.com/vmihailenco/msgpack/v5"
	lua "github.com/yuin/gopher-lua"

	"github.com/tarantool/tt/cli/util"
	"gopkg.in/yaml.v2"
)

const (
	startOfYamlOutput = "---\n"

	endOfYAMLOutput = "\n...\n"
	endOfLuaOutput  = ";"

	tagPushPrefixYAML = `%TAG`
	tagPushPrefixLua  = `-- Push`
)

type EvalPlainTextOpts struct {
	ReadTimeout  time.Duration
	PushCallback func(interface{})
	ResData      interface{}
}

type PlainTextEvalRes struct {
	DataEncBase64 string `yaml:"data_enc"`
}

// callPlainTextConnYAML calls function on Tarantool instance
// Function should return `interface{}`, `string` (res, err)
// to be correctly processed.
func callPlainTextConn(conn net.Conn, funcName string, args []interface{},
	opts EvalPlainTextOpts) ([]interface{}, error) {
	evalFunc, err := util.GetTextTemplatedStr(&callFuncTmpl, map[string]string{
		"FunctionName": funcName,
	})

	if err != nil {
		return nil, fmt.Errorf("failed to instantiate call function template: %s", err)
	}

	return evalPlainTextConn(conn, evalFunc, args, opts)
}

// evalPlainTextConnYAML calls function on Tarantool instance
// Function should return `interface{}`, `string` (res, err)
// to be correctly processed.
func evalPlainTextConn(conn net.Conn, funcBody string, args []interface{},
	opts EvalPlainTextOpts) ([]interface{}, error) {
	if err := formatAndSendEvalFunc(conn, funcBody, args, evalFuncTmpl); err != nil {
		return nil, err
	}

	// recv from socket
	resBytes, err := readFromPlainTextConn(conn, opts)
	if err == io.EOF {
		return nil, err
	}

	if err != nil {
		return nil, fmt.Errorf("failed to check returned data: %s", err)
	}

	data, err := processEvalTarantoolRes(resBytes, opts.ResData)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func formatAndSendEvalFunc(conn net.Conn, funcBody string, args []interface{},
	evalFuncTmpl string) error {
	if args == nil {
		args = []interface{}{}
	}

	argsEncoded, err := msgpack.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to encode args: %s", err)
	}

	evalFunc, err := util.GetTextTemplatedStr(&evalFuncTmpl, map[string]string{
		"FunctionBody": funcBody,
		"ArgsEncoded":  fmt.Sprintf("%x", argsEncoded),
	})

	if err != nil {
		return fmt.Errorf("failed to instantiate eval function template: %s", err)
	}

	evalFuncFormatted := strings.Join(
		strings.Split(strings.TrimSpace(evalFunc), "\n"), " ",
	)
	evalFuncFormatted = strings.Join(strings.Fields(evalFuncFormatted), " ") + "\n"

	// write to socket
	if err := writeToPlainTextConn(conn, evalFuncFormatted); err != nil {
		return fmt.Errorf("failed to send eval function to socket: %s", err)
	}

	return nil
}

func writeToPlainTextConn(conn net.Conn, data string) error {
	writer := bufio.NewWriter(conn)
	if _, err := writer.WriteString(data); err != nil {
		return fmt.Errorf("failed to send to socket: %s", err)
	}

	if err := writer.Flush(); err != nil {
		return fmt.Errorf("failed to flush: %s", err)
	}

	return nil
}

// readFromPlainTextConn function reads plain text from Tarantool connection
// These code was inspired by Tarantool console eval
// https://github.com/tarantool/tarantool/blob/3bc4a156e937102f23e2157ef88aa6c007759005/src/box/lua/console.lua#L469
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
		// received tag string can be handled via pushCallback function
		//
		dataPortionBytes, err := readDataPortionFromPlainTextConn(conn, &buffer, opts.ReadTimeout)
		if err == io.EOF {
			return nil, err
		}

		if err != nil {
			return nil, fmt.Errorf("failed to read from instance socket: %s", err)
		}

		dataPortion := string(dataPortionBytes)

		if !pushTagIsReceived(dataPortion) {
			dataBytes = dataPortionBytes
			break
		}

		if opts.PushCallback != nil {
			var pushedData interface{}

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
	readTimeout time.Duration) ([]byte, error) {
	tmp := make([]byte, 256)
	data := make([]byte, 0)

	if readTimeout > 0 {
		conn.SetReadDeadline(time.Now().Add(readTimeout))
	} else {
		conn.SetReadDeadline(time.Time{})
	}

	hasYAMLOutputPrefix := false

	for {
		// We have to read from socket in medium parts (not in small parts, like 1 byte),
		// this greatly speeds up reading and responsiveness.
		//
		// But, due to the peculiarities of `box.session.push()`, we have to process the
		// data byte by byte - we use HasPrefix and HasSufix merhods.
		//
		// In addition, when reading in portion (more than 1 byte), we need
		// to save the read data (for this we use the bytes.Buffer), since we can read it,
		// but not process them in this function call (see examples above).
		// This structure allows us to save this data and process it in the next function call.
		//

		if buffer.Len() == 0 {
			if n, err := conn.Read(tmp); err != nil && err != io.EOF {
				return nil, fmt.Errorf("failed to read: %s", err)
			} else if n == 0 || err == io.EOF {
				return nil, io.EOF
			} else {
				buffer.Write(tmp[:n])
			}
		}

		nextByte, err := buffer.ReadByte()
		if err != nil {
			return nil, fmt.Errorf("failed to get byte from buffer: %s", err)
		}

		data = append(data, nextByte)
		dataString := string(data)

		if strings.HasPrefix(endOfYAMLOutput, dataString) ||
			strings.HasPrefix(tagPushPrefixYAML, dataString) ||
			strings.HasPrefix(tagPushPrefixLua, dataString) {
			continue
		}

		if !hasYAMLOutputPrefix &&
			strings.HasPrefix(dataString, startOfYamlOutput) ||
			strings.HasPrefix(dataString, tagPushPrefixYAML) {
			hasYAMLOutputPrefix = true
		}

		if hasYAMLOutputPrefix && strings.HasSuffix(dataString, endOfYAMLOutput) {
			break
		}

		if strings.HasSuffix(dataString, endOfLuaOutput) {
			break
		}
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("connection was closed")
	}

	return data, nil
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

func getPushedData(pushedDataBytes []byte) (interface{}, error) {
	var pushedData interface{}
	pushedDataString := string(pushedDataBytes)

	if strings.HasPrefix(pushedDataString, tagPushPrefixYAML) {
		// YAML - just decode tag
		if err := yaml.Unmarshal(pushedDataBytes, &pushedData); err != nil {
			return nil, fmt.Errorf("failed to unmarshal pushed data: %s", err)
		}
	} else {
		// Lua

		// remove first line (-- Push)
		pushedDataString = strings.SplitN(pushedDataString, "\n", 2)[1]
		// remove ";"
		pushedDataString = strings.TrimRight(pushedDataString, ";")

		pushedData = pushedDataString
	}

	return pushedData, nil
}

func processEvalTarantoolRes(resBytes []byte, result interface{}) ([]interface{}, error) {
	var err error
	var evalResultEncBase64 string

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
	// "XXX";

	resString := string(resBytes)
	if strings.HasPrefix(resString, startOfYamlOutput) {
		getResultEncBase64Func = getPlainTextEvalResYaml
	} else {
		getResultEncBase64Func = getPlainTextEvalResLua
	}

	if evalResultEncBase64, err = getResultEncBase64Func(resBytes); err != nil {
		return nil, err
	}

	dataEnc, err := base64.StdEncoding.DecodeString(evalResultEncBase64)
	if err != nil {
		return nil, fmt.Errorf("failed to decode hex value: %s", err)
	}

	if result != nil {
		if err := msgpack.Unmarshal(dataEnc, result); err != nil {
			return nil, fmt.Errorf("failed to parse eval result: %s", err)
		}

		return nil, nil
	}

	var data []interface{}
	if err := msgpack.Unmarshal(dataEnc, &data); err != nil {
		return nil, fmt.Errorf("failed to parse eval result: %s", err)
	}

	return data, nil
}

func getPlainTextEvalResYaml(resBytes []byte) (string, error) {
	evalResults := []PlainTextEvalRes{}
	if err := yaml.UnmarshalStrict(resBytes, &evalResults); err != nil {
		errorStrings := make([]map[string]string, 0)
		if err := yaml.UnmarshalStrict(resBytes, &errorStrings); err == nil {
			if len(errorStrings) > 0 {
				errStr, found := errorStrings[0]["error"]
				if found {
					return "", errors.New(errStr)
				}
			}

		}

		return "", fmt.Errorf("failed to parse eval result: %s", err)
	}

	if len(evalResults) != 1 {
		return "", fmt.Errorf("expected one result, found %d", len(evalResults))
	}

	evalResult := evalResults[0]
	return evalResult.DataEncBase64, nil
}

func getPlainTextEvalResLua(resBytes []byte) (string, error) {
	L := lua.NewState()
	defer L.Close()

	doString := fmt.Sprintf(`res = %s`, resBytes)

	if err := L.DoString(doString); err != nil {
		return "", err
	}

	luaRes := L.Env.RawGetString("res")

	if luaRes.Type() == lua.LTString {
		return "", errors.New(lua.LVAsString(luaRes))
	}

	encodedDataLV := L.GetTable(luaRes, lua.LString("data_enc"))

	encodedData := lua.LVAsString(encodedDataLV)

	return encodedData, nil
}
