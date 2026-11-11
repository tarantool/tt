package pack

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// srcRockArch is the arch of a source rock.
	srcRockArch = "src"
	// gitDirName is the directory of a git repository.
	gitDirName = ".git"
	// execBits are the executable bits of a mode.
	execBits fs.FileMode = 0o111
	// srcFileMode and srcExecMode are the modes of the files of a source
	// rock: its entries carry no other bits, so the same files are the same
	// rock whoever packs them.
	srcFileMode fs.FileMode = 0o644
	srcExecMode fs.FileMode = 0o755
)

// zipEpoch is the modification time of every entry of a source rock, the
// earliest a zip can say: an entry carries no time of its own, so the same
// files are the same bytes.
var zipEpoch = time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)

// srcExcluded are the top-level directories of a project that are no source
// of it: what tt and git keep there.
var srcExcluded = []string{gitDirName, buildDirName, rocksDirName, runtimeDirName}

// writeSrcRock writes the source rock of the package at dest: the rockspec
// at the root and, under srcDir, the files of the project and a VERSION file
// holding the version it was packed at, which a build of the unpacked rock,
// having no git history, derives its version from.
//
// The files are those git tracks in the project, those of its submodules
// included, as they are in the working tree, or, out of a git repository,
// every file of the project; the directories tt and git keep (srcExcluded)
// are left out either way. The rock is written through a temporary file
// renamed into place.
func writeSrcRock(
	ctx context.Context, dest, projectDir, srcDir, rockspecName string, rockspec []byte,
	semver string,
) error {
	files, err := sourceFiles(ctx, projectDir)
	if err != nil {
		return err
	}

	var buf bytes.Buffer

	archive := zip.NewWriter(&buf)

	err = addZipEntry(archive, rockspecName, srcFileMode, bytes.NewReader(rockspec))
	if err != nil {
		return err
	}

	err = addZipEntry(archive, path.Join(srcDir, versionFileName), srcFileMode,
		strings.NewReader(semver+"\n"))
	if err != nil {
		return err
	}

	for _, rel := range files {
		err = addSourceFile(archive, projectDir, srcDir, rel)
		if err != nil {
			return err
		}
	}

	err = archive.Close()
	if err != nil {
		return fmt.Errorf("writing the source rock: %w", err)
	}

	return writeAtomically(dest, buf.Bytes())
}

// addSourceFile adds the project file rel, slash-separated, under srcDir. A
// file git tracks that the working tree no longer has is left out, as is
// anything but a regular file or a link to one.
func addSourceFile(archive *zip.Writer, projectDir, srcDir, rel string) error {
	abs := filepath.Join(projectDir, filepath.FromSlash(rel))

	info, err := os.Stat(abs)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	if !info.Mode().IsRegular() {
		return nil
	}

	mode := srcFileMode
	if info.Mode()&execBits != 0 {
		mode = srcExecMode
	}

	file, err := os.Open(abs)
	if err != nil {
		return fmt.Errorf("reading %s: %w", rel, err)
	}

	defer func() { _ = file.Close() }()

	return addZipEntry(archive, path.Join(srcDir, rel), mode, file)
}

// addZipEntry adds a file entry of name and mode with the content of content.
func addZipEntry(archive *zip.Writer, name string, mode fs.FileMode, content io.Reader) error {
	header := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipEpoch}
	header.SetMode(mode)

	writer, err := archive.CreateHeader(header)
	if err != nil {
		return fmt.Errorf("writing %s into the source rock: %w", name, err)
	}

	_, err = io.Copy(writer, content)
	if err != nil {
		return fmt.Errorf("writing %s into the source rock: %w", name, err)
	}

	return nil
}

// sourceFiles lists the source files of the project, slash-separated and
// sorted, without the version file the source rock writes itself: those git
// tracks, submodules included, or, when the project is no git work tree,
// every file under it. The manifest is one whether git tracks it or not: a
// source rock without it is of no package.
func sourceFiles(ctx context.Context, projectDir string) ([]string, error) {
	files, err := gitFiles(ctx, projectDir)
	if err != nil {
		files, err = walkFiles(projectDir)
		if err != nil {
			return nil, err
		}
	}

	files = append(files, manifestFileName)

	out := make([]string, 0, len(files))

	for _, rel := range files {
		top, _, _ := strings.Cut(rel, "/")
		if slices.Contains(srcExcluded, top) || rel == versionFileName {
			continue
		}

		out = append(out, rel)
	}

	slices.Sort(out)

	return slices.Compact(out), nil
}

// gitFiles lists the files git tracks under projectDir, those of its
// submodules included. It fails when projectDir is no git work tree or git
// cannot be run.
func gitFiles(ctx context.Context, projectDir string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", projectDir,
		"ls-files", "-z", "--cached", "--recurse-submodules")

	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("listing the files git tracks: %w", err)
	}

	var files []string

	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" {
			files = append(files, name)
		}
	}

	return files, nil
}

// untrackedSources lists the files under paths of the project, relative to
// it, that git neither tracks nor ignores: the files a build of the project
// lays out that its source rock leaves out. It lists none out of a git work
// tree.
func untrackedSources(ctx context.Context, projectDir string, paths []string) []string {
	if len(paths) == 0 {
		return nil
	}

	args := append([]string{"-C", projectDir,
		"ls-files", "-z", "--others", "--exclude-standard", "--"}, paths...)

	out, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		return nil
	}

	var files []string

	for name := range strings.SplitSeq(string(out), "\x00") {
		if name != "" && name != manifestFileName && name != versionFileName {
			files = append(files, name)
		}
	}

	slices.Sort(files)

	return files
}

// untrackedWarning is the warning about the files untrackedSources lists, or
// "" when there are none.
func untrackedWarning(files []string) string {
	const shown = 5

	if len(files) == 0 {
		return ""
	}

	list := strings.Join(files[:min(len(files), shown)], ", ")
	if len(files) > shown {
		list += fmt.Sprintf(" and %d more", len(files)-shown)
	}

	return fmt.Sprintf("the source rock leaves out the files git does not track in the "+
		"components: %s; a build of the source rock lacks them, so commit them, or make "+
		"git ignore them", list)
}

// walkFiles lists every file under projectDir, slash-separated, skipping the
// directories srcExcluded names at its top.
func walkFiles(projectDir string) ([]string, error) {
	var files []string

	err := filepath.WalkDir(projectDir, func(abs string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(projectDir, abs)
		if err != nil {
			return fmt.Errorf("relativizing %s: %w", abs, err)
		}

		if entry.IsDir() {
			if rel != "." && slices.Contains(srcExcluded, filepath.ToSlash(rel)) {
				return fs.SkipDir
			}

			return nil
		}

		files = append(files, filepath.ToSlash(rel))

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("listing the files of %s: %w", projectDir, err)
	}

	return files, nil
}

// srcDirName is the directory of the source rock the project's files are in:
// the one LuaRocks unpacks the source of url into, the last element of the
// URL without .git, or the package name when there is no URL.
func srcDirName(url, name string) string {
	if url == "" {
		return name
	}

	base := path.Base(strings.TrimSuffix(strings.TrimSuffix(url, "/"), ".git"))
	if base == "" || base == "." || base == "/" {
		return name
	}

	return base
}

// writeAtomically writes data at dest through a temporary file in its
// directory renamed into place, so a server never serves half a rock.
func writeAtomically(dest string, data []byte) error {
	temp, err := os.CreateTemp(filepath.Dir(dest), filepath.Base(dest)+".*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}

	defer func() { _ = os.Remove(temp.Name()) }()

	_, err = temp.Write(data)
	if err == nil {
		err = temp.Chmod(srcFileMode)
	}

	closeErr := temp.Close()
	if err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(temp.Name(), dest)
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", dest, err)
	}

	return nil
}
