package systemd

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// dirPerm is the mode of the directories tt creates: drop-in directories
	// and the directory of the host files, which systemd and tt read.
	dirPerm = 0o755
	// unitPerm is the mode of a unit file, as systemd expects it: readable by
	// all, writable by its owner.
	unitPerm = 0o644
)

var (
	// ErrForeignFile is wrapped by the error for a file in the place of a
	// unit that tt did not write: tt does not replace it.
	ErrForeignFile = errors.New("was not written by tt service systemd")
	// ErrNoUnitDir is wrapped by the error for a unit directory that does
	// not exist.
	ErrNoUnitDir = errors.New("no unit directory")
)

// Installation is what Install writes.
type Installation struct {
	// UnitDir is the directory the units go to.
	UnitDir string
	// HostDir is the directory of the host files of the application, which
	// the mark goes to.
	HostDir string
	// Files are the files of the units.
	Files []File
	// Mark is the mark of the units.
	Mark Mark
	// Stale are the files of the units installed before, relative to
	// UnitDir, that the new ones do not have: the drop-ins of instances gone
	// from the description. Install removes them.
	Stale []string
}

// Install writes the units and the mark of installation, and removes the
// stale files of the units installed before.
//
// The unit directory must exist: it is the directory of the service
// manager, which tt does not create. A file in the place of a unit that tt
// did not write is an error before anything is written (ErrForeignFile).
//
// The units go next to their places first, then the mark is written, then
// the units are moved into their places. A unit directory tt cannot write to
// fails the install before the mark is written, and once the mark is there
// the instance commands leave the instances to the units before the units
// can start them: tt never supervises an instance next to a unit that has
// not been marked.
func Install(installation Installation) error {
	staged, err := stage(installation.UnitDir, installation.Files)
	if err != nil {
		return err
	}

	err = WriteMark(installation.HostDir, installation.Mark)
	if err != nil {
		staged.discard()

		return err
	}

	err = staged.commit()
	if err != nil {
		return err
	}

	for _, name := range installation.Stale {
		_, err = removeGenerated(installation.UnitDir, name)
		if err != nil {
			return err
		}
	}

	return nil
}

// WriteFiles writes files into dir, which it creates if need be, as
// tt service systemd generate writes them into a directory: without a mark,
// and replacing only the files tt wrote (ErrForeignFile).
func WriteFiles(dir string, files []File) error {
	err := os.MkdirAll(dir, dirPerm)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", dir, err)
	}

	staged, err := stage(dir, files)
	if err != nil {
		return err
	}

	return staged.commit()
}

// Uninstall removes the files of the units mark records from the unit
// directory of mark, then the drop-in directories it leaves empty, and then
// the mark in hostDir. A file tt did not write is left in place and returned
// in kept, by its path; a file that is gone already is no failure.
//
// A failure stops the removal before the mark is removed, so that
// tt service systemd uninstall can be run again.
func Uninstall(hostDir string, mark Mark) ([]string, error) {
	var kept []string

	for _, name := range mark.Files() {
		removed, err := removeGenerated(mark.UnitDir, name)
		if err != nil {
			return kept, err
		}

		if !removed {
			kept = append(kept, filepath.Join(mark.UnitDir, name))
		}
	}

	return kept, RemoveMark(hostDir)
}

// stagedFile is a file of the units written next to its place.
type stagedFile struct {
	temp string
	path string
}

// stagedFiles are the files of the units written next to their places.
type stagedFiles []stagedFile

// stage writes files next to their places in dir, after checking that none
// replaces a file tt did not write. A failure removes what it has written.
func stage(dir string, files []File) (stagedFiles, error) {
	info, err := os.Stat(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w %s", ErrNoUnitDir, dir)
	}

	if err != nil {
		return nil, fmt.Errorf("cannot read the unit directory: %w", err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s is not a directory", ErrNoUnitDir, dir)
	}

	for _, file := range files {
		path := filepath.Join(dir, file.Name)

		generated, found, err := readGenerated(path)
		if err != nil {
			return nil, err
		}

		if found && !generated {
			return nil, fmt.Errorf("%s %w; move it away to install the units", path,
				ErrForeignFile)
		}
	}

	staged := make(stagedFiles, 0, len(files))

	for _, file := range files {
		path := filepath.Join(dir, file.Name)

		err = os.MkdirAll(filepath.Dir(path), dirPerm)
		if err != nil {
			staged.discard()

			return nil, fmt.Errorf("cannot create %s: %w", filepath.Dir(path), err)
		}

		temp, err := writeTemp(path, file.Content, unitPerm)
		if err != nil {
			staged.discard()

			return nil, err
		}

		staged = append(staged, stagedFile{temp: temp, path: path})
	}

	return staged, nil
}

// commit moves the staged files into their places.
func (staged stagedFiles) commit() error {
	for i, file := range staged {
		err := os.Rename(file.temp, file.path)
		if err != nil {
			staged[i:].discard()

			return fmt.Errorf("cannot write %s: %w", file.path, err)
		}
	}

	return nil
}

// discard removes the staged files.
func (staged stagedFiles) discard() {
	for _, file := range staged {
		_ = os.Remove(file.temp)
	}
}

// readGenerated reports whether the file at path is there, and whether tt
// wrote it.
func readGenerated(path string) (bool, bool, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, false, nil
	}

	if err != nil {
		return false, false, fmt.Errorf("cannot read %s: %w", path, err)
	}

	return IsGenerated(content), true, nil
}

// removeGenerated removes name, a file of the units relative to dir, if tt
// wrote it, and then its drop-in directory if that is left empty. It reports
// whether the file is gone: false for a file tt did not write.
func removeGenerated(dir, name string) (bool, error) {
	path := filepath.Join(dir, name)

	generated, found, err := readGenerated(path)
	if err != nil {
		return false, err
	}

	if found && !generated {
		return false, nil
	}

	if found {
		err = os.Remove(path)
		if err != nil {
			return false, fmt.Errorf("cannot remove %s: %w", path, err)
		}
	}

	parent := filepath.Dir(path)
	if parent != filepath.Clean(dir) {
		// A drop-in directory with files of someone else stays.
		_ = os.Remove(parent)
	}

	return true, nil
}

// writeAtomically writes content into path with perm, replacing the file
// there at once.
func writeAtomically(path string, content []byte, perm os.FileMode) error {
	temp, err := writeTemp(path, content, perm)
	if err != nil {
		return err
	}

	err = os.Rename(temp, path)
	if err != nil {
		_ = os.Remove(temp)

		return fmt.Errorf("cannot write %s: %w", path, err)
	}

	return nil
}

// writeTemp writes content with perm into a new hidden file next to path,
// and returns the path of the file.
func writeTemp(path string, content []byte, perm os.FileMode) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}

	_, err = file.Write(content)
	if err == nil {
		err = file.Chmod(perm)
	}

	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		_ = os.Remove(file.Name())

		return "", fmt.Errorf("cannot write %s: %w", path, err)
	}

	return file.Name(), nil
}
