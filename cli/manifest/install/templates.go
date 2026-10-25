package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// checkTemplates refuses a project install that would lay the archive's
// .templates/ over one already in the project install-root. It runs before
// anything is written, like the name and runtime checks.
//
// Whatever is there is refused, whoever put it there. It may be the templates
// of the project's own package, those of a guest installed earlier, or the
// user's own files, and install keeps no record that would tell them apart;
// merging the archive's tree into it would leave a mix no single package
// describes, and replacing it could destroy what the user wrote. --force and
// --upgrade do not lift this either: they are about the package's own files,
// and the existing .templates/ is not provably the package's. Moving it away
// is the user's call, and the error says so.
//
// A version installed into the user or system scope needs no such check: its
// install root is its own, and nothing but the archive is ever laid into it.
func checkTemplates(header *Header, lay state.Layout) error {
	if !header.HasTemplates {
		return nil
	}

	path := filepath.Join(lay.Root, templatesDirName)

	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("checking %s: %w", path, err)
	}

	return stateErrorf("%w: %q carries instance templates that would be laid over %s; "+
		"move the existing directory away to install them",
		errTemplatesExist, header.Manifest.Package.Name, path)
}
