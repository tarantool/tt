package install

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// templatesInstalled reports whether a scope lays an archive's .templates/ into
// its install-root. Only project does: its install-root is the application
// directory, where tt instance looks for .templates/<name>/. The user and system
// scopes are shared LuaRocks trees with no application directory of their own,
// so there is nowhere meaningful to put the templates and they stay in the
// archive, exactly as before .templates/ was packed at all.
func templatesInstalled(scope Scope) bool {
	return scope == ScopeProject
}

// checkTemplates refuses an install that would lay the archive's .templates/
// over one already in the project install-root. It runs before anything is
// written, like the name and runtime checks.
//
// Whatever is there is refused, whoever put it there. It may be the templates
// of the project's own package, those of a guest installed earlier, or the
// user's own files, and install keeps no record that would tell them apart;
// merging the archive's tree into it would leave a mix no single package
// describes, and replacing it could destroy what the user wrote. --force and
// --upgrade do not lift this either: they are about the package's own files,
// and the existing .templates/ is not provably the package's. Moving it away
// is the user's call, and the error says so.
func checkTemplates(opts Options, header *Header, lay state.Layout) error {
	if !header.HasTemplates || !templatesInstalled(opts.Scope) {
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
