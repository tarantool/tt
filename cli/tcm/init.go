// Package tcm holds what tt tcm init does to a project: it declares TCM, the
// Tarantool Cluster Manager, as an instance of the application in the
// instances.yaml of a template, and the version of TCM the package bundles in
// [platform].tcm of its manifest. TCM then runs as any instance does, under
// tt instance.
package tcm

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/instances"
)

const (
	// RecordName is the name tt tcm init gives the record of TCM.
	RecordName = "tcm"
	// DefaultHTTP is the address TCM listens on by default, as its
	// documentation gives http.host and http.port.
	DefaultHTTP = "127.0.0.1:8080"
)

var (
	// ErrNameTaken is wrapped by the error for a description that has a
	// record named RecordName that is not TCM.
	ErrNameTaken = errors.New("the name of the record of TCM is taken")
	// ErrNoVersion is wrapped by the error for no version of TCM to set
	// [platform].tcm to.
	ErrNoVersion = errors.New("no version of TCM to require")
)

// Declared tells what AddRecord did.
type Declared struct {
	// Existing is the name of the record of TCM the description has
	// already, empty when AddRecord added one.
	Existing string
	// HTTP is the http field of the existing record, empty for none.
	HTTP string
}

// AddRecord returns the description src, the instances.yaml of a template in
// dir, with the record of TCM added at its end, listening on http, and what
// it did. src nil is a template without instances.yaml: the description
// AddRecord returns is new, and starts with "*": {} when the template has a
// config.yaml (withConfig), since a template of a config alone runs every
// instance of its topology and must keep doing so. A description that has
// TCM already is returned as it is, with Declared.Existing set; one whose
// record named RecordName is not TCM is an error.
//
// The record is appended as text, so the comments and the order of the
// description stay as they were; the result is parsed before it is
// returned, which checks http.
func AddRecord(src []byte, dir string, withConfig bool, http string) ([]byte, Declared, error) {
	var declared Declared

	if src != nil {
		desc, err := instances.Parse(src, dir)
		if err != nil {
			return nil, declared, err
		}

		for _, record := range desc.Instances {
			switch {
			case record.Kind() == instances.KindTCM:
				declared.Existing = record.Name
				declared.HTTP = record.TCM.HTTP

				return src, declared, nil
			case record.Name == RecordName:
				return nil, declared, fmt.Errorf("%w: the instance %q is declared already, "+
					"and is not TCM; add the record of TCM by hand, under another name, with "+
					"kind: %s", ErrNameTaken, RecordName, instances.KindTCMName)
			}
		}
	}

	if http == "" {
		http = DefaultHTTP
	}

	var out bytes.Buffer

	switch {
	case src != nil:
		out.Write(src)

		if len(src) > 0 && !bytes.HasSuffix(src, []byte("\n")) {
			out.WriteString("\n")
		}

		if len(bytes.TrimSpace(src)) > 0 {
			out.WriteString("\n")
		}
	case withConfig:
		out.WriteString("# The instances of the topology of config.yaml, in the order of " +
			"their names.\n\"*\": {}\n\n")
	}

	fmt.Fprintf(&out, "# TCM, the Tarantool Cluster Manager of the application, added by "+
		"tt tcm init.\n%s:\n  kind: %s\n  http: %s\n"+
		"  # config: tcm.yaml   # settings of TCM of your own, relative to this file\n",
		RecordName, instances.KindTCMName, http)

	_, err := instances.Parse(out.Bytes(), dir)
	if err != nil {
		return nil, declared, err
	}

	return out.Bytes(), declared, nil
}

// Constraint returns the [platform].tcm constraint for the version given to
// tt tcm init --version: a constraint as it is, such as ">=1.3,<2", and a
// bare version as the lowest one allowed, ">=1.3".
func Constraint(given string) string {
	given = strings.TrimSpace(given)
	if given == "" || strings.ContainsAny(given[:1], "<>=~^!") {
		return given
	}

	return ">=" + given
}

// NewestInstalled returns the newest release of TCM installed under the
// roots, the scope roots tt binary installs into, as major.minor.patch;
// false when none is. A component of an SDK is passed over, since its
// directory names the version of the SDK rather than that of TCM, and so is
// a development build.
func NewestInstalled(roots []string) (string, bool) {
	var newest *version.Version

	for _, root := range roots {
		installed, err := binary.InstalledVersions(root, binary.ProgramTcm)
		if err != nil {
			continue
		}

		for _, selected := range installed {
			if strings.HasPrefix(selected, binary.SDKVersionPrefix) ||
				strings.HasPrefix(selected, binary.DevVersionPrefix) {
				continue
			}

			parsed, err := version.Parse(selected)
			if err != nil {
				continue
			}

			if newest == nil || version.IsLess(*newest, parsed) {
				newest = &parsed
			}
		}
	}

	if newest == nil {
		return "", false
	}

	return fmt.Sprintf("%d.%d.%d", newest.Major, newest.Minor, newest.Patch), true
}
