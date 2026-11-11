package build

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/internal/srctree"
)

const (
	// cacheDirName holds, under it, what an incremental build recorded.
	cacheDirName = "cache"
	// runtimeDirName is the directory of the runtime bundle of a project.
	runtimeDirName = "_runtime"
	// cacheFilePerm is the mode of a record.
	cacheFilePerm os.FileMode = 0o644
)

// incrementalRecord is what the last build of an incremental component left:
// the hash of the inputs it was built from, and the files it left, which a
// skipped build needs to be there.
type incrementalRecord struct {
	Inputs string `json:"inputs"`
	// Written is what the build wrote into TT_OUTPUT_DIR, relative to it: a
	// module's files under its namespace, an executable component's programs.
	Written []string `json:"written,omitempty"`
	// LaidOut is the files of the component the layout takes, relative to it.
	LaidOut []string `json:"laid_out,omitempty"`
}

// incrementalState is what skipUnchanged found of an incremental component:
// the record of its last build, the zero value when there is none, and the
// hash of its inputs now.
type incrementalState struct {
	last   incrementalRecord
	inputs string
}

// componentInputs hashes what the build of a component depends on: its build
// block and the files of dir, the component's directory. The files are the
// regular files and the symbolic links, but hidden ones (.git, .tarawasm, the
// .gitignore files themselves), those of _build and _runtime, and those the
// .gitignore files ignore - each for its subtree, from projectDir down, as
// git reads them. What a build generates and git does not keep is so left
// out, and the hash stays the same from one build to the next.
func componentInputs(projectDir, dir string, build manifest.Build) (string, error) {
	block, err := toml.Marshal(build)
	if err != nil {
		return "", fmt.Errorf("hashing the build block: %w", err)
	}

	rel, err := filepath.Rel(projectDir, dir)
	if err != nil {
		return "", fmt.Errorf("hashing %s: %w", dir, err)
	}

	var domain []string

	if rel != "." {
		domain = strings.Split(filepath.ToSlash(rel), "/")
	}

	patterns, err := srctree.Ancestors(projectDir, domain)
	if err != nil {
		return "", err
	}

	digest := sha256.New()

	_, _ = digest.Write(block)

	err = srctree.Walk(dir, domain, patterns, skipInput, func(
		full string, path []string, entry fs.DirEntry,
	) error {
		return hashInput(digest, full, strings.Join(path, "/"), entry)
	})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(digest.Sum(nil)), nil
}

// skipInput leaves out of the inputs of a component the hidden files (.git,
// .tarawasm, the .gitignore files themselves) and what is in _build and
// _runtime.
func skipInput(path []string, _ fs.DirEntry) bool {
	name := path[len(path)-1]

	return strings.HasPrefix(name, ".") || name == buildDirName || name == runtimeDirName
}

// hashInput writes one input, the file at full whose path from the project is
// rel, to digest: a symbolic link by its target, a regular file by its mode
// and content. Anything else is no input.
func hashInput(digest io.Writer, full, rel string, entry fs.DirEntry) error {
	var err error

	switch {
	case entry.Type()&fs.ModeSymlink != 0:
		var target string

		target, err = os.Readlink(full)
		_, _ = fmt.Fprintf(digest, "link %s %s\n", rel, target)
	case entry.Type().IsRegular():
		err = hashFile(digest, full, rel)
	}

	if err != nil {
		return fmt.Errorf("hashing %s: %w", full, err)
	}

	return nil
}

// hashFile writes the path, the mode and the content hash of a file.
func hashFile(digest io.Writer, path, rel string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("hashing: %w", err)
	}

	defer func() { _ = file.Close() }()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("hashing: %w", err)
	}

	content := sha256.New()

	_, err = io.Copy(content, file)
	if err != nil {
		return fmt.Errorf("hashing: %w", err)
	}

	_, _ = fmt.Fprintf(digest, "file %s %o %x\n", rel, info.Mode().Perm(), content.Sum(nil))

	return nil
}

// incrementalPath is where the last build of component is recorded, under
// stateDir, the directory of the build's own state (project.stateDir).
func incrementalPath(stateDir, component string) string {
	return filepath.Join(stateDir, cacheDirName, component+".json")
}

// readIncremental reads the record of the last build of component; false
// when there is none, or one that cannot be read: the next build writes it
// again.
func readIncremental(stateDir, component string) (incrementalRecord, bool) {
	var record incrementalRecord

	data, err := os.ReadFile(incrementalPath(stateDir, component))
	if err != nil {
		return record, false
	}

	return record, json.Unmarshal(data, &record) == nil
}

// writeIncremental records the build of component.
func writeIncremental(stateDir, component string, record incrementalRecord) error {
	path := incrementalPath(stateDir, component)

	data, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("recording the build of %q: %w", component, err)
	}

	err = os.MkdirAll(filepath.Dir(path), dirPerm)
	if err == nil {
		err = os.WriteFile(path, data, cacheFilePerm)
	}

	if err != nil {
		return fmt.Errorf("recording the build of %q: %w", component, err)
	}

	return nil
}

// allPresent reports whether every file of files, relative to dir, is there.
func allPresent(dir string, files []string) bool {
	for _, file := range files {
		_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(file)))
		if err != nil {
			return false
		}
	}

	return true
}

// laidOutFiles lists the files of the component at compAbsPath the layout
// takes, relative to it.
func laidOutFiles(component manifest.Component, compAbsPath string) ([]string, error) {
	filter := newFileFilter(component)

	var files []string

	err := filepath.WalkDir(compAbsPath, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, relErr := relComponents(compAbsPath, path)
		if relErr != nil || len(rel) == 0 {
			return relErr
		}

		if entry.IsDir() {
			if filter.pruneDir(rel) {
				return fs.SkipDir
			}

			return nil
		}

		if entry.Type().IsRegular() && filter.keepFile(rel) {
			files = append(files, strings.Join(rel, "/"))
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing the files of %s: %w", compAbsPath, err)
	}

	return files, nil
}

// stampTree records the files under dir, at any depth, by their paths
// relative to it. A missing directory has none.
func stampTree(dir string) (map[string]fileStamp, error) {
	stamps := map[string]fileStamp{}

	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == dir {
			return fs.SkipAll
		}

		if err != nil || entry.IsDir() {
			return err
		}

		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("stamping: %w", err)
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return fmt.Errorf("stamping: %w", err)
		}

		stamps[filepath.ToSlash(rel)] = fileStamp{info.Size(), info.ModTime(), info.Mode()}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", dir, err)
	}

	return stamps, nil
}

// writtenFiles lists what a build wrote into a directory stamped before and
// after it ran: the files it created or changed, and those of written, the
// files an earlier build wrote, that are still there - a make target that was
// up to date changes nothing.
func writtenFiles(before, after map[string]fileStamp, written []string) []string {
	files := changedFiles(before, after)

	for _, file := range written {
		if _, ok := after[file]; ok {
			files = append(files, file)
		}
	}

	slices.Sort(files)

	return slices.Compact(files)
}
