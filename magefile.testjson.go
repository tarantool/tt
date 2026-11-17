//go:build mage

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

// TT_TEST_JSON_DIR is the directory mage unit:* writes the events of go test
// -json of each module into, <dir>/<module>.json, the root module being
// root.json: what each test did and printed, for a tool that reads what ran
// and what skipped, as ci/backend_skips.py does in CI, with no second run of
// the tests. The run prints what go test prints without -json.
const testJSONEnv = "TT_TEST_JSON_DIR"

// testJSONDirPerm is the mode of the directory of TT_TEST_JSON_DIR.
const testJSONDirPerm = 0o755

// testEvent is the part of an event of go test -json (go doc test2json) that
// testPrinter reads.
type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

// testPrinter takes the output of go test -json: it writes it into file as
// it is, and prints to out what go test prints without -json, the output of
// the packages and the output of a test that failed, or with -v, verbose,
// the output of every test.
type testPrinter struct {
	file    io.Writer
	out     io.Writer
	verbose bool
	// partial is the start of a line whose end has not come yet.
	partial []byte
	// tests is the output of the running top-level tests, by package and
	// test, with that of their subtests that have not passed: what a
	// top-level test that fails prints, as it does when a subtest fails.
	tests map[[2]string][]testOutput
}

// testOutput is a line a test or a subtest printed.
type testOutput struct {
	test   string
	output string
}

func newTestPrinter(file, out io.Writer, verbose bool) *testPrinter {
	return &testPrinter{
		file: file, out: out, verbose: verbose, partial: nil,
		tests: map[[2]string][]testOutput{},
	}
}

// Write takes the next bytes of the output of go test -json.
func (p *testPrinter) Write(data []byte) (int, error) {
	_, err := p.file.Write(data)
	if err != nil {
		return 0, fmt.Errorf("writing the events of go test: %w", err)
	}

	p.partial = append(p.partial, data...)

	for {
		end := bytes.IndexByte(p.partial, '\n')
		if end < 0 {
			break
		}

		line := p.partial[:end+1]

		p.partial = p.partial[end+1:]

		p.line(line)
	}

	return len(data), nil
}

// finish prints what is left once go test has ended: a line with no end,
// and the output of tests that never ended, as of a test binary that
// crashed before its package failed.
func (p *testPrinter) finish() {
	if len(p.partial) > 0 {
		p.line(slices.Concat(p.partial, []byte{'\n'}))

		p.partial = nil
	}

	for key := range p.tests {
		p.flush(key)
	}
}

// line prints a line of the output of go test -json as go test would print
// it without -json. A line that is not an event, which go test prints
// before the events of a package it cannot test, is printed as it is.
func (p *testPrinter) line(line []byte) {
	var event testEvent

	err := json.Unmarshal(line, &event)
	if err != nil || event.Action == "" {
		p.print(string(line))

		return
	}

	key := [2]string{event.Package, rootTest(event.Test)}

	switch event.Action {
	case "build-output":
		p.print(event.Output)
	case "output":
		switch {
		case event.Test == "":
			// The PASS of a package that passed is printed by go test -v
			// alone; its ok line, and what its tests print outside of
			// them, always.
			if p.verbose || event.Output != "PASS\n" {
				p.print(event.Output)
			}
		case p.verbose:
			p.print(event.Output)
		default:
			p.tests[key] = append(p.tests[key], testOutput{test: event.Test, output: event.Output})
		}
	case "fail":
		switch event.Test {
		case "":
			p.flushPackage(event.Package)
		case key[1]:
			p.flush(key)
		}
	case "pass", "skip":
		switch event.Test {
		case "":
			p.dropPackage(event.Package)
		case key[1]:
			delete(p.tests, key)
		default:
			p.dropSubtest(key, event.Test)
		}
	}
}

// rootTest is the top-level test of test, a subtest or a test itself.
func rootTest(test string) string {
	root, _, _ := strings.Cut(test, "/")

	return root
}

// flush prints the output of the test of key and forgets it.
func (p *testPrinter) flush(key [2]string) {
	for _, line := range p.tests[key] {
		p.print(line.output)
	}

	delete(p.tests, key)
}

// dropSubtest forgets the output of a subtest that passed or skipped, and
// of its subtests, from the output of its top-level test of key.
func (p *testPrinter) dropSubtest(key [2]string, test string) {
	p.tests[key] = slices.DeleteFunc(p.tests[key], func(line testOutput) bool {
		return line.test == test || strings.HasPrefix(line.test, test+"/")
	})
}

// flushPackage prints the output of the tests of a failed package that did
// not end: a panic of one ends the test binary, and the package fails.
func (p *testPrinter) flushPackage(pkg string) {
	for key := range p.tests {
		if key[0] == pkg {
			p.flush(key)
		}
	}
}

// dropPackage forgets the output of the tests of a package that ended well.
func (p *testPrinter) dropPackage(pkg string) {
	for key := range p.tests {
		if key[0] == pkg {
			delete(p.tests, key)
		}
	}
}

func (p *testPrinter) print(text string) {
	_, _ = io.WriteString(p.out, text)
}

// testJSONFile is the file of TT_TEST_JSON_DIR the events of the tests of
// module go into.
func testJSONFile(dir, module string) string {
	name := "root"
	if module != "." {
		name = strings.ReplaceAll(filepath.ToSlash(module), "/", "-")
	}

	return filepath.Join(dir, name+".json")
}

// runTestsJSON runs go test with args, which ask for -json, in testEnv: the
// events go into path, and the output go test prints without -json to the
// standard output.
func runTestsJSON(path string, args ...string) error {
	env, err := testEnv()
	if err != nil {
		return err
	}

	err = os.MkdirAll(filepath.Dir(path), testJSONDirPerm)
	if err != nil {
		return fmt.Errorf("failed to create the directory of %s: %w", testJSONEnv, err)
	}

	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create the events file: %w", err)
	}

	printer := newTestPrinter(file, os.Stdout, mg.Verbose())

	_, err = sh.Exec(env, printer, os.Stderr, goExecutableName, args...)

	printer.finish()

	return errors.Join(err, file.Close())
}
