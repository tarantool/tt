package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const activeDirectoryMode = 0o755

// Selection identifies a product and the version to select in one scope.
type Selection struct {
	Program Program
	ID      string
}

var (
	errMissingBinary     = errors.New("binary is not installed in the selected scope")
	errMissingHeaders    = errors.New("headers are not installed in the selected scope")
	errActivePathNotLink = errors.New("active path is not a symbolic link")
)

type activeLink struct {
	path   string
	target string
}

// ValidateInstallation checks a product before changing any active selection.
func ValidateInstallation(root string, selected Selection) error {
	directory := InstallationPath(root, selected.Program, selected.ID)
	if !IsRegularFile(filepath.Join(directory, selected.Program.Exec())) {
		return fmt.Errorf("%w: %s", errMissingBinary, filepath.Base(directory))
	}

	if selected.Program.IsTarantool() &&
		!IsDir(filepath.Join(directory, IncDirName, selected.Program.Exec())) {
		return fmt.Errorf("%w: %s", errMissingHeaders, filepath.Base(directory))
	}

	return nil
}

// installationLinks builds link targets without validating the installation.
func installationLinks(root string, selected Selection) []activeLink {
	directory := InstallationPath(root, selected.Program, selected.ID)
	executable := selected.Program.Exec()
	links := []activeLink{{filepath.Join(root, BinDirName, executable),
		filepath.Join(directory, executable)}}

	if selected.Program.IsTarantool() {
		links = append(links, activeLink{filepath.Join(root, IncDirName, executable),
			filepath.Join(directory, IncDirName, executable)})
	}

	return links
}

// ReplaceLink points the symbolic link at path to target, or removes it when
// target is empty. The parent directory of path must exist.
//
// The link is replaced by renaming a new one over it, so a reader resolving
// path sees either the old target or the new one and never a missing link.
// tt package selects the active version of an installed application with it,
// <root>/apps/<app>, the same way the active links of the products are
// replaced here.
func ReplaceLink(path, target string) error {
	return replaceActiveLink(activeLink{path: path, target: target})
}

// replaceActiveLink prepares the new link before replacing the old selection.
func replaceActiveLink(link activeLink) error {
	if link.target == "" {
		err := os.Remove(link.path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		if err == nil {
			return nil
		}

		return fmt.Errorf("failed to remove active link: %w", err)
	}

	file, err := os.CreateTemp(filepath.Dir(link.path), ".active-*")
	if err != nil {
		return fmt.Errorf("failed to prepare active link: %w", err)
	}

	name := file.Name()

	_ = file.Close()

	defer func() { _ = os.Remove(name) }()

	err = os.Remove(name)
	if err != nil {
		return fmt.Errorf("failed to remove temporary file: %w", err)
	}

	err = os.Symlink(link.target, name)
	if err != nil {
		return fmt.Errorf("failed to prepare symbolic link: %w", err)
	}

	err = os.Rename(name, link.path)
	if err != nil {
		return fmt.Errorf("failed to replace active link: %w", err)
	}

	return nil
}

// ActivateSelections applies validated selections, restoring old links if a
// replacement fails. The caller must validate every product before activation.
func ActivateSelections(root string, selections []Selection) error {
	var links []activeLink

	for _, selection := range selections {
		selected := installationLinks(root, selection)

		links = append(links, selected...)
	}

	previous := make([]activeLink, len(links))
	for index, link := range links {
		previous[index].path = link.path

		info, err := os.Lstat(link.path)
		if errors.Is(err, os.ErrNotExist) && link.target == "" {
			continue
		}

		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to inspect active link %q: %w", link.path, err)
		}

		if err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return fmt.Errorf("%w: %q", errActivePathNotLink, link.path)
			}

			previous[index].target, err = os.Readlink(link.path)
			if err != nil {
				return fmt.Errorf("failed to read active link %q: %w", link.path, err)
			}
		}

		err = CreateDirectory(filepath.Dir(link.path), activeDirectoryMode)
		if err != nil {
			return err
		}
	}

	for index, link := range links {
		err := replaceActiveLink(link)
		if err != nil {
			for undo := index - 1; undo >= 0; undo-- {
				rollbackErr := replaceActiveLink(previous[undo])

				err = errors.Join(err, rollbackErr)
			}

			return fmt.Errorf("failed to switch active link %q: %w", link.path, err)
		}
	}

	return nil
}
