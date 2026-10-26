package systemd

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"gopkg.in/yaml.v3"
)

// MarkFileName is the mark of the units of an application in the directory
// of its host files, <root>/etc/<app>/.
const MarkFileName = "systemd.yaml"

// markHeader opens the mark, for whoever comes across it on a host.
const markHeader = `# Written by tt service systemd install: the instances below run under
# systemd units, and tt instance start, stop, restart and kill leave them
# to systemctl. tt service systemd uninstall removes this file with the units.
`

const (
	// markPerm is the mode of the mark: what the units manage is no secret.
	markPerm = 0o644
	// markIndent is the indentation of the mark, as in instances.yaml.
	markIndent = 2
)

// ErrInvalidMark is wrapped by the error for a mark that cannot be read.
var ErrInvalidMark = errors.New("invalid mark of systemd units")

// Mark tells which instances of an application run under the systemd units
// tt service systemd install wrote, and where the units are.
type Mark struct {
	// UnitDir is the directory the units are in.
	UnitDir string `yaml:"unit_dir"`
	// Template is the name of the template unit, <app>@.service.
	Template string `yaml:"template"`
	// RunAs is the user and the group the units run the instances as,
	// <user>:<group>, so that an install that names none keeps them. Empty
	// is the default, DefaultRunAs.
	RunAs string `yaml:"run_as,omitempty"`
	// Instances maps the name of every instance the units manage to its
	// unit, <app>@<inst>.service.
	Instances map[string]string `yaml:"instances"`
}

// Files returns the paths of the files of the units the mark records,
// relative to UnitDir: the template and the drop-in of every instance, in
// the order of the instances' names.
func (mark *Mark) Files() []string {
	names := make([]string, 0, len(mark.Instances))
	for name := range mark.Instances {
		names = append(names, name)
	}

	slices.Sort(names)

	files := make([]string, len(names)+1)

	files[0] = mark.Template

	for i, name := range names {
		files[i+1] = DropInPath(mark.Instances[name])
	}

	return files
}

// MarkPath returns the path of the mark in hostDir, the directory of the
// host files of an application.
func MarkPath(hostDir string) string {
	return filepath.Join(hostDir, MarkFileName)
}

// ReadMark reads the mark in hostDir. found is false when there is none. A
// mark that does not parse, or lacks what it must hold, is an error wrapping
// ErrInvalidMark: what it says the units manage cannot be told then.
func ReadMark(hostDir string) (Mark, bool, error) {
	path := MarkPath(hostDir)

	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Mark{UnitDir: "", Template: "", RunAs: "", Instances: nil}, false, nil
	}

	if err != nil {
		return Mark{}, false, fmt.Errorf("cannot read %s: %w", path, err)
	}

	var mark Mark

	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)

	err = decoder.Decode(&mark)
	if err != nil {
		return Mark{}, false, fmt.Errorf("%w %s: %w", ErrInvalidMark, path, err)
	}

	if !filepath.IsAbs(mark.UnitDir) || mark.Template == "" {
		return Mark{}, false, fmt.Errorf("%w %s: unit_dir must be an absolute path "+
			"and template a unit name", ErrInvalidMark, path)
	}

	for name, unit := range mark.Instances {
		if name == "" || unit == "" {
			return Mark{}, false, fmt.Errorf("%w %s: an instance and its unit must be named",
				ErrInvalidMark, path)
		}
	}

	if mark.RunAs != "" {
		_, err = ParseRunAs(mark.RunAs)
		if err != nil {
			return Mark{}, false, fmt.Errorf("%w %s: run_as: %w", ErrInvalidMark, path, err)
		}
	}

	return mark, true, nil
}

// WriteMark writes mark into hostDir, which it creates if need be, replacing
// the mark there at once: a reader sees the old mark or the new one.
func WriteMark(hostDir string, mark Mark) error {
	data := bytes.NewBufferString(markHeader)

	encoder := yaml.NewEncoder(data)
	encoder.SetIndent(markIndent)

	err := encoder.Encode(mark)
	if err == nil {
		err = encoder.Close()
	}

	if err != nil {
		return fmt.Errorf("cannot render the mark of the units: %w", err)
	}

	err = os.MkdirAll(hostDir, dirPerm)
	if err != nil {
		return fmt.Errorf("cannot create %s: %w", hostDir, err)
	}

	return writeAtomically(MarkPath(hostDir), data.Bytes(), markPerm)
}

// RemoveMark removes the mark in hostDir, if there is one.
func RemoveMark(hostDir string) error {
	err := os.Remove(MarkPath(hostDir))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove the mark of the units: %w", err)
	}

	return nil
}
