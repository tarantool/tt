package integrity

import (
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	// hashesFileMode is the creation mode of hashes files and signatures:
	// anyone who checks the tree reads them.
	hashesFileMode fs.FileMode = 0o644

	luaSuffix = ".lua"
)

var (
	errNotRegularFile = errors.New("not a regular file")
	errNotDirectory   = errors.New("not a directory")
	errNotInTree      = errors.New("not a file of the tree")
	errInvalidPath    = errors.New("invalid path to list")
	errDuplicatePath  = errors.New("listed twice")
)

// tarantoolSuffixes are the name suffixes of the files Tarantool loads: Lua
// sources and shared libraries.
func tarantoolSuffixes() []string {
	return []string{luaSuffix, ".so", ".dylib"}
}

// clusterConfigNames are the names of the cluster configs Tarantool reads,
// wherever they are in the tree: tt hands Tarantool the one of the pair it
// runs from.
func clusterConfigNames() []string {
	return []string{"config.yaml", "config.yml"}
}

// rsaSigner writes the hashes files of a tree and signs them with an RSA
// private key.
type rsaSigner struct {
	privateKey *rsa.PrivateKey
}

var _ Signer = rsaSigner{privateKey: nil}

// Sign writes the hashes files of the package tree rooted at dir, each with
// its signature: hashes.json with the files Tarantool loads, env_hashes.json
// with the others. The files of each are listed depth-first, the entries of
// a directory in byte order of their names.
func (signer rsaSigner) Sign(dir string, opts SignOptions) error {
	dir = filepath.Clean(dir)

	skipped := make(map[string]bool, len(opts.Skip))
	for _, rel := range opts.Skip {
		skipped[filepath.Join(dir, filepath.FromSlash(rel))] = true
	}

	loaded := make(map[string]bool, len(opts.Loaded))
	for _, rel := range opts.Loaded {
		loaded[rel] = false
	}

	var tarantoolFiles, otherFiles []fileDigest

	walker := treeWalker{
		skipDir: func(path string) bool { return skipped[path] },
		visit: func(relPath, path string, _ fs.FileInfo) error {
			if isSignerOutput(path) {
				return nil
			}

			digest, err := hashFile(path)
			if err != nil {
				return err
			}

			file := fileDigest{path: relPath, digest: digest}

			_, isLoaded := loaded[relPath]
			if isLoaded {
				loaded[relPath] = true
			}

			if isLoaded || loadedByTarantool(relPath) {
				tarantoolFiles = append(tarantoolFiles, file)
			} else {
				otherFiles = append(otherFiles, file)
			}

			return nil
		},
	}

	err := walker.walk(dir, "")
	if err != nil {
		return err
	}

	for _, rel := range opts.Loaded {
		if !loaded[rel] {
			return fmt.Errorf("failed to sign %q: %q is %w", dir, rel, errNotInTree)
		}
	}

	err = signer.writeHashes(filepath.Join(dir, appHashesFileName), tarantoolFiles)
	if err != nil {
		return err
	}

	return signer.writeHashes(filepath.Join(dir, envHashesFileName), otherFiles)
}

// SignFiles writes the environment hashes file of dir listing files, in the
// order of their paths, and signs it.
func (signer rsaSigner) SignFiles(dir string, files []SignedFile) error {
	digests := make([]fileDigest, 0, len(files))
	listed := make(map[string]bool, len(files))

	for _, file := range files {
		err := checkListedPath(file.Path)
		if err != nil {
			return err
		}

		if listed[file.Path] {
			return fmt.Errorf("failed to sign %q: %q is %w", dir, file.Path, errDuplicatePath)
		}

		listed[file.Path] = true

		digest, err := hashFile(file.Source)
		if err != nil {
			return err
		}

		digests = append(digests, fileDigest{path: file.Path, digest: digest})
	}

	slices.SortFunc(digests, func(left, right fileDigest) int {
		return strings.Compare(left.path, right.path)
	})

	return signer.writeHashes(filepath.Join(dir, envHashesFileName), digests)
}

// checkListedPath checks that name names a file below the directory of a
// hashes file the way the hashes file lists it: relative, slash-separated
// and clean, with no "." or ".." among its elements.
func checkListedPath(name string) error {
	if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "\\") {
		return fmt.Errorf("%w %q", errInvalidPath, name)
	}

	for element := range strings.SplitSeq(name, "/") {
		if element == "" || element == "." || element == ".." {
			return fmt.Errorf("%w %q", errInvalidPath, name)
		}
	}

	return nil
}

// loadedByTarantool reports whether Tarantool loads the file of a package at
// the relative path relPath, by its name: a Lua source, a shared library or
// a cluster config.
func loadedByTarantool(relPath string) bool {
	for _, suffix := range tarantoolSuffixes() {
		if strings.HasSuffix(relPath, suffix) {
			return true
		}
	}

	return slices.Contains(clusterConfigNames(), path.Base(relPath))
}

// writeHashes writes the hashes file at path listing digests, then its
// signature next to it. An existing file is overwritten in place and keeps
// its mode.
func (signer rsaSigner) writeHashes(path string, digests []fileDigest) error {
	data, err := encodeHashes(digests)
	if err != nil {
		return fmt.Errorf("failed to write %q: %w", path, err)
	}

	signature, err := signData(signer.privateKey, data)
	if err != nil {
		return fmt.Errorf("failed to sign %q: %w", path, err)
	}

	err = os.WriteFile(path, data, hashesFileMode)
	if err != nil {
		return fmt.Errorf("failed to write hashes: %w", err)
	}

	err = os.WriteFile(path+signatureSuffix, signature, hashesFileMode)
	if err != nil {
		return fmt.Errorf("failed to write signature: %w", err)
	}

	return nil
}

// isSignerOutput reports whether the file at path has the name of a file
// that Sign or SignFiles writes: a hashes file or a signature. A tree never
// lists one, wherever it is: writing it would change the digest just listed,
// and the signed tree would fail every check.
func isSignerOutput(path string) bool {
	switch filepath.Base(path) {
	case appHashesFileName, appHashesFileName + signatureSuffix,
		envHashesFileName, envHashesFileName + signatureSuffix:
		return true
	default:
		return false
	}
}

// treeWalker walks a directory tree depth-first, the entries of each
// directory in ascending byte order of their names. The root may be a
// symbolic link to a directory; below it, a symbolic link to a directory is
// skipped and a symbolic link to anything else stands for its target under
// its own path. A symbolic link that does not resolve is an error.
type treeWalker struct {
	// skipDir reports whether the directory at a lexical path is skipped.
	skipDir func(path string) bool
	// visit is called for every entry that is not a directory, with its
	// path relative to the root of the tree, its lexical path and the file
	// information of what it resolves to.
	visit func(relPath, path string, info fs.FileInfo) error
}

// walk visits the tree rooted at root. relRoot is the relative path of the
// root, empty when the relative paths start below it.
func (walker treeWalker) walk(root, relRoot string) error {
	info, err := os.Stat(root)
	if err != nil {
		return fmt.Errorf("failed to walk: %w", err)
	}

	if !info.IsDir() {
		return fmt.Errorf("failed to walk %q: %w", root, errNotDirectory)
	}

	return walker.walkDir(root, relRoot)
}

// walkDir visits the entries of the directory dir. The directory is read
// whole and closed before its entries are visited, so the walk holds at
// most one directory open.
func (walker treeWalker) walkDir(dir, relDir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())

		relPath := entry.Name()
		if relDir != "" {
			relPath = relDir + "/" + entry.Name()
		}

		err = walker.walkEntry(entry, path, relPath)
		if err != nil {
			return err
		}
	}

	return nil
}

// walkEntry visits the directory entry at path.
func (walker treeWalker) walkEntry(entry fs.DirEntry, path, relPath string) error {
	switch {
	case entry.IsDir():
		if walker.skipDir(path) {
			return nil
		}

		return walker.walkDir(path, relPath)
	case entry.Type()&fs.ModeSymlink != 0:
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("failed to resolve symbolic link: %w", err)
		}

		if info.IsDir() {
			return nil
		}

		return walker.visit(relPath, path, info)
	default:
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("failed to walk %q: %w", path, err)
		}

		return walker.visit(relPath, path, info)
	}
}

// hashFile returns the SHA-256 digest of the content of the regular file
// at path, following symbolic links.
func hashFile(path string) ([]byte, error) {
	file, err := openRegularFile(path)
	if err != nil {
		return nil, err
	}

	digest, err := hashContent(file)
	closeErr := file.Close()

	if err != nil {
		return nil, err
	}

	if closeErr != nil {
		return nil, fmt.Errorf("failed to close file: %w", closeErr)
	}

	return digest, nil
}

// hashContent returns the SHA-256 digest of what remains to read in file.
func hashContent(file *os.File) ([]byte, error) {
	hash := sha256.New()

	_, err := io.Copy(hash, file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	return hash.Sum(nil), nil
}
