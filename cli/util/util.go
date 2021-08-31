package util

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime/debug"

	"gopkg.in/yaml.v2"
)

// VersionFunc is a type of function that return
// string with current Tarantool CLI version.
type VersionFunc func(bool, bool) string

// GetFileContentBytes returns file content as a bytes slice.
func GetFileContentBytes(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileContent, err := ioutil.ReadAll(file)
	if err != nil {
		return nil, err
	}

	return fileContent, nil
}

// JoinAbspath concat paths and makes the resulting path absolute.
func JoinAbspath(paths ...string) (string, error) {
	var err error

	path := filepath.Join(paths...)
	if path, err = filepath.Abs(path); err != nil {
		return "", fmt.Errorf("Failed to get absolute path: %s", err)
	}

	return path, nil
}

// Find find index of specified string in the slice.
func Find(src []string, find string) int {
	for i, elem := range src {
		if find == elem {
			return i
		}
	}

	return -1
}

// InternalError shows error information, version of tt and call stack.
func InternalError(format string, f VersionFunc, err ...interface{}) error {
	internalErrorFmt := `Whoops! It looks like something is wrong with this version of Tarantool CLI.
Error: %s
Version: %s
Stacktrace:
%s
`
	version := f(false, false)
	return fmt.Errorf(internalErrorFmt, fmt.Sprintf(format, err...), version, debug.Stack())
}

// ParseYAML parse yaml file at specified path.
func ParseYAML(path string) (map[string]interface{}, error) {
	fileContent, err := GetFileContentBytes(path)
	if err != nil {
		return nil, fmt.Errorf(`Failed to read "%s" file: %s`, path, err)
	}

	var raw map[string]interface{}
	if err := yaml.Unmarshal(fileContent, &raw); err != nil {
		return nil, fmt.Errorf("Failed to parse Tarantool CLI configuration: %s", err)
	}

	return raw, nil
}
