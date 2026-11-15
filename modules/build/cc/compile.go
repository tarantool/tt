package cc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/tarantool/tt/sdk/build"
)

// filePerm and dirPerm are the modes of the files and the directories the
// build writes into its work directory.
const (
	filePerm = 0o644
	dirPerm  = 0o755
)

// maxJobs is the number of compiles that run at once: one a CPU, as the
// cmake backend builds.
func maxJobs() int {
	return runtime.NumCPU()
}

// compileAll runs compile for each of steps, up to jobs of them at once. The
// first that fails cancels the context of the others, which stop, and its
// error is the one returned; when ctx itself is done, its error is. No
// compile starts once one has failed.
func compileAll(ctx context.Context, steps []compileStep, jobs int,
	compile func(context.Context, compileStep) error,
) error {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var (
		group sync.WaitGroup
		// failed guards first, the error of the first compile that failed.
		failed sync.Mutex
		first  error
	)

	slots := make(chan struct{}, max(jobs, 1))

	for _, step := range steps {
		select {
		case slots <- struct{}{}:
		case <-runCtx.Done():
		}

		if runCtx.Err() != nil {
			break
		}

		group.Go(func() {
			defer func() { <-slots }()

			err := compile(runCtx, step)
			if err == nil {
				return
			}

			failed.Lock()
			defer failed.Unlock()

			if first == nil {
				first = err

				cancel()
			}
		})
	}

	group.Wait()

	err := ctx.Err()
	if err != nil {
		return fmt.Errorf("compiling: %w", err)
	}

	return first
}

// compile compiles the source of step into its object, run as a command of
// the build (build.Context.Command), with -o a temporary file of the
// directory of the objects renamed to the object once the compiler has
// succeeded: a compile that fails or is stopped leaves no object a later
// build would take for its own.
//
// The compiles of a build run at once (compileAll), and the commands of the
// Host of the core are run one at a time: Run streams what the command
// prints with tt -V, which would mix the lines of the compilers, and shows a
// spinner of its own otherwise. So compile runs the command itself, keeping
// what the compiler prints, and logs the command as the Host does; the
// output of a compile that fails is in its error, that of one that succeeds
// - its warnings - is returned, whole, for the build to show and to keep in
// the record of the object (runner.compile).
func compile(ctx context.Context, c *build.Context, step compileStep) (string, error) {
	tmp := step.object + ".tmp"
	argv := step.args(tmp)
	log := logger(c)

	cmd := c.Command(ctx, argv[0], argv[1:]...)

	var output bytes.Buffer

	cmd.Stdout = &output
	cmd.Stderr = &output

	log.DebugContext(ctx, "running "+commandLine(cmd.Args), "dir", cmd.Dir)

	err := cmd.Run()

	printed := strings.TrimRight(output.String(), "\n")

	if err != nil {
		_ = os.Remove(tmp)

		if ctx.Err() != nil {
			return "", fmt.Errorf("compiling %s: %w", step.source, ctx.Err())
		}

		return "", fmt.Errorf("compiling %s: %w\n%s\n%s", step.source, err,
			commandLine(cmd.Args), printed)
	}

	err = os.Rename(tmp, step.object)
	if err != nil {
		return "", fmt.Errorf("compiling %s: %w", step.source, err)
	}

	return printed, nil
}

// compilerOutput keeps what the compilers of a build printed, the warnings
// of the compiles that succeeded, each by the object it is of, for the build
// to show once its compiles are done, in the order of the sources: the
// compiles run at once, and their warnings would mix in the order they end.
type compilerOutput struct {
	mu      sync.Mutex
	printed map[string]string
}

// newCompilerOutput returns an empty compilerOutput.
func newCompilerOutput() *compilerOutput {
	return &compilerOutput{mu: sync.Mutex{}, printed: map[string]string{}}
}

// add keeps printed, what the compile of step printed; nothing is kept for
// a compile that printed nothing.
func (o *compilerOutput) add(step compileStep, printed string) {
	if printed == "" {
		return
	}

	o.mu.Lock()
	defer o.mu.Unlock()

	o.printed[step.object] = printed
}

// show shows with warn what the compiles of steps printed, each once, with
// the source it is of, in the order of steps.
func (o *compilerOutput) show(steps []compileStep, warn func(string)) {
	o.mu.Lock()
	defer o.mu.Unlock()

	for _, step := range steps {
		printed, ok := o.printed[step.object]
		if ok {
			warn(compilerWarning(step.source, printed))
		}
	}
}

// compilerWarning is the warning of what the compile of source printed: the
// output as the compiler printed it, after a line naming the source.
func compilerWarning(source, printed string) string {
	return "compiling " + source + ":\n" + printed
}

// runLink runs the link of argv, the words of the compiler that links first,
// as compile runs a compile: what the linker prints is in the error of a
// link that fails, with a hint when it says that an object is not
// position-independent (pieHint), and is logged at the debug level
// otherwise.
func runLink(ctx context.Context, c *build.Context, argv []string) error {
	log := logger(c)
	cmd := c.Command(ctx, argv[0], argv[1:]...)

	var output bytes.Buffer

	cmd.Stdout = &output
	cmd.Stderr = &output

	log.DebugContext(ctx, "running "+commandLine(cmd.Args), "dir", cmd.Dir)

	err := cmd.Run()

	printed := strings.TrimRight(output.String(), "\n")

	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("linking: %w", ctx.Err())
		}

		err = fmt.Errorf("linking: %w\n%s\n%s", err, commandLine(cmd.Args), printed)

		if hint := pieHint(printed); hint != "" {
			err = fmt.Errorf("%w\n%s", err, hint)
		}

		return err
	}

	if printed != "" {
		log.DebugContext(ctx, "the linker printed:\n"+printed)
	}

	return nil
}

// compileCommandsFile is the compilation database of a build, in its work
// directory: the format of clang's JSON Compilation Database, which clangd
// and other tools read to know how each source is compiled.
const compileCommandsFile = "compile_commands.json"

// compileCommand is an entry of compile_commands.json.
type compileCommand struct {
	// Directory is the working directory of the compile, absolute, which
	// file and the paths of arguments are relative to.
	Directory string `json:"directory"`
	// Arguments are the argv of the compile, the compiler first.
	Arguments []string `json:"arguments"`
	// File is the source.
	File string `json:"file"`
	// Output is its object.
	Output string `json:"output"`
}

// writeCompileCommands writes compile_commands.json into work: an entry for
// each of steps, run in cwd, with -o the object, whether or not this build
// compiles it. It is written before the compiles, so that an editor knows
// the flags of a source that does not compile.
func writeCompileCommands(work, cwd string, steps []compileStep) error {
	commands := make([]compileCommand, 0, len(steps))

	for _, step := range steps {
		commands = append(commands, compileCommand{
			Directory: cwd, Arguments: step.args(step.object), File: step.source,
			Output: step.object,
		})
	}

	data, err := json.MarshalIndent(commands, "", "  ")
	if err != nil {
		return fmt.Errorf("writing %s: %w", compileCommandsFile, err)
	}

	return writeFileAtomic(filepath.Join(work, compileCommandsFile), append(data, '\n'))
}

// removeStaleObjects creates dir, the directory of the objects, and removes
// from it what is of no object of steps: the objects of the sources the
// build no longer lists, and a temporary file a stopped compile left.
func removeStaleObjects(dir string, steps []compileStep) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	keep := map[string]bool{}

	for _, step := range steps {
		for _, file := range objectFiles(step) {
			keep[filepath.Base(file)] = true
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("reading %s: %w", dir, err)
	}

	for _, entry := range entries {
		if keep[entry.Name()] {
			continue
		}

		err = os.RemoveAll(filepath.Join(dir, entry.Name()))
		if err != nil {
			return fmt.Errorf("removing a stale object: %w", err)
		}
	}

	return nil
}

// objectFiles are the files the compile of step keeps: its object, the list
// of the files it read and its record.
func objectFiles(step compileStep) []string {
	return []string{step.object, step.depfile(), step.recordPath()}
}

// writeFileAtomic writes data into the file path through a temporary file of
// its directory renamed to it, so that a reader finds the old file or the
// new one, never a part of it.
func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	_, err = tmp.Write(data)

	closeErr := tmp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(tmp.Name(), filePerm)
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		removeErr := os.Remove(tmp.Name())
		if removeErr != nil && !errors.Is(removeErr, fs.ErrNotExist) {
			err = errors.Join(err, removeErr)
		}

		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}
