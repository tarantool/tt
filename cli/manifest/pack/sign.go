package pack

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/internal/instances"
)

// Names the signature of a package adds to it: the hashes files, each with
// its signature, at the root of the tree they list.
const (
	appHashesFileName = "hashes.json"
	envHashesFileName = "env_hashes.json"
	signatureSuffix   = ".sig"
	// rocksManifestsDir is tt's own install state in a rocks tree. An
	// install drops it from an archive (install's versionMapper), so it is
	// not part of what the package is signed for.
	rocksManifestsDir = rocksDirName + "/manifests"
	// includeDirName is where a bundled tarantool keeps its headers, as tt
	// binary installs them: include/tarantool/.
	includeDirName = "include"
	// runtimeBinDir is the bin/ level most bundled components have; the
	// Enterprise SDK is flat and has none.
	runtimeBinDir = "bin"
)

var (
	// errSignedWithoutDeps is the error of a signed pack --without-deps: the
	// dependencies the install fetches from the registry are signed by
	// nobody, and a check would refuse them.
	errSignedWithoutDeps = errors.New(
		"a package packed --without-deps cannot be signed: the dependencies its " +
			"install fetches are not part of it")
	// errOutsidePackage is wrapped by the error for an instance record of a
	// signed package that runs a file the package does not hold.
	errOutsidePackage = errors.New("runs a file outside the package")
	// errAmbiguousRuntime is wrapped by the error for a bundled component
	// holding its executable both at its top and in bin/: the install would
	// take one of them, and the signature cannot say which.
	errAmbiguousRuntime = errors.New("holds its executable twice")
)

// signStage signs the staged tree: every runtime component in _runtime/ as
// it is to be installed (signRuntime), then the package itself, without
// _runtime/, each file Tarantool loads listed for it, the scripts the
// instance records of its templates run among them.
func signStage(stageDir, projectDir string, signer integrity.Signer) error {
	for _, name := range []string{appHashesFileName, envHashesFileName} {
		for _, file := range []string{name, name + signatureSuffix} {
			_, err := os.Lstat(filepath.Join(stageDir, file))
			if err == nil {
				return stateErrorf("%s: %w: signing writes it", file, errReservedName)
			}
		}
	}

	for _, name := range []string{runtimeTarantool, runtimeTt, runtimeTcm} {
		err := signRuntime(stageDir, name, signer)
		if err != nil {
			return err
		}
	}

	scripts, err := templateRuns(stageDir, projectDir)
	if err != nil {
		return err
	}

	err = signer.Sign(stageDir, integrity.SignOptions{
		Skip:   []string{runtimeDirName, rocksManifestsDir},
		Loaded: scripts,
	})
	if err != nil {
		return fmt.Errorf("signing the package: %w", err)
	}

	return nil
}

// signRuntime signs the bundled component name, when the archive carries it,
// in the layout tt package install lays it out in, which is the layout of a
// tt binary installation: the executable at the top, taken from bin/ or from
// the top of the component, and for tarantool the headers under
// include/tarantool/ (install's runtimeMapper). Nothing else of the component
// is installed, so nothing else is listed. The hashes file goes into the
// component in _runtime/, and install lays it next to the executable.
func signRuntime(stageDir, name string, signer integrity.Signer) error {
	dir := filepath.Join(stageDir, runtimeDirName, name)

	_, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("signing the bundled %s: %w", name, err)
	}

	executable, err := runtimeExecutable(dir, name)
	if err != nil {
		return err
	}

	files := []integrity.SignedFile{{Path: name, Source: executable}}

	if name == runtimeTarantool {
		headers, err := runtimeHeaders(dir, name)
		if err != nil {
			return err
		}

		files = append(files, headers...)
	}

	err = signer.SignFiles(dir, files)
	if err != nil {
		return fmt.Errorf("signing the bundled %s: %w", name, err)
	}

	return nil
}

// runtimeExecutable returns the executable of the bundled component in dir,
// named name: in bin/, or at the top of the flat Enterprise SDK.
func runtimeExecutable(dir, name string) (string, error) {
	var found []string

	for _, candidate := range []string{filepath.Join(dir, runtimeBinDir, name),
		filepath.Join(dir, name)} {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() {
			found = append(found, candidate)
		}
	}

	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return "", stateErrorf("signing the bundled %s: %w: no executable %s in %s",
			name, errNoRuntime, name, dir)
	default:
		return "", stateErrorf("signing the bundled %s: %s %w: %s and %s", name, dir,
			errAmbiguousRuntime, found[0], found[1])
	}
}

// runtimeHeaders lists the headers of the bundled tarantool in dir under
// include/<name>/, by the paths they are installed at.
func runtimeHeaders(dir, name string) ([]integrity.SignedFile, error) {
	root := filepath.Join(dir, includeDirName, name)

	_, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	var headers []integrity.SignedFile

	err = filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(dir, file)
		if err != nil {
			return err //nolint:wrapcheck // Wrapped once below.
		}

		headers = append(headers, integrity.SignedFile{Path: filepath.ToSlash(rel), Source: file})

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("signing the headers of the bundled %s: %w", name, err)
	}

	return headers, nil
}

// templateRuns checks the instance records of every template the stage
// holds: a signed package runs only its own files, so a record may name a
// script or a binary by a path only inside the package, and a program only
// when the package builds it into .rocks/bin/. It returns the scripts, as
// paths relative to the package, for Tarantool to check: it runs them.
//
// The descriptions are parsed against the project, as pack checked them
// (checkTemplate), and the paths they resolve to are mapped back into the
// package: the project directory is the installed version.
func templateRuns(stageDir, projectDir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(stageDir, templatesDirName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", templatesDirName, err)
	}

	var (
		scripts  []string
		problems []error
	)

	for _, entry := range entries {
		label := templatesDirName + "/" + entry.Name() + "/" + templateInstancesFile

		data, err := os.ReadFile(filepath.Join(stageDir, templatesDirName, entry.Name(),
			templateInstancesFile))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", label, err)
		}

		desc, err := instances.Parse(data, projectDir)
		if err != nil {
			return nil, templateParseError(label, err)
		}

		for _, record := range desc.Instances {
			script, problem := recordRun(stageDir, projectDir, record)
			if problem != nil {
				problems = append(problems, fmt.Errorf("%s: instance %q %w", label,
					record.Name, problem))
			}

			if script != "" {
				scripts = append(scripts, script)
			}
		}
	}

	joined := errors.Join(problems...)
	if joined != nil {
		return nil, stateErrorf("signing the package: %w", joined)
	}

	return scripts, nil
}

// recordRun checks what the instance record runs and returns its script, as
// a path relative to the package, or an empty string for a record that runs
// none.
func recordRun(stageDir, projectDir string, record instances.Instance) (string, error) {
	switch {
	case record.Script != "":
		return insidePackage(projectDir, record.Script)
	case record.Binary == "":
		return "", nil
	case strings.ContainsRune(record.Binary, '/'):
		_, err := insidePackage(projectDir, record.Binary)

		return "", err
	}

	program := path.Join(rocksDirName, runtimeBinDir, record.Binary)

	info, err := os.Stat(filepath.Join(stageDir, filepath.FromSlash(program)))
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%w: %q is not in %s of the package, and a signed package "+
			"runs no program of the host", errOutsidePackage, record.Binary,
			path.Join(rocksDirName, runtimeBinDir))
	}

	return "", nil
}

// insidePackage returns file, an absolute path a description resolved
// against projectDir, relative to the package, or an error when it is
// outside of it.
func insidePackage(projectDir, file string) (string, error) {
	rel, err := filepath.Rel(projectDir, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %s", errOutsidePackage, file)
	}

	return filepath.ToSlash(rel), nil
}
