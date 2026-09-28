package integrity

import (
	"crypto/rsa"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	// hashesFileMode is the creation mode of hashes files and signatures:
	// anyone who checks the environment reads them.
	hashesFileMode fs.FileMode = 0o644
	// hashesDirMode is the creation mode of the directory that holds the
	// hashes file of a single-file application.
	hashesDirMode fs.FileMode = 0o700
	// executeBits are the execute permission bits of a file mode.
	executeBits fs.FileMode = 0o111

	binDirName          = "bin"
	modulesDirName      = "modules"
	ttConfigFileName    = "tt.yaml"
	instancesEnabledDir = "instances.enabled"
	luaSuffix           = ".lua"
)

var (
	errNotRegularFile = errors.New("not a regular file")
	errNotDirectory   = errors.New("not a directory")
)

// appConfigFileNames are the configuration files of a directory
// application, listed first and in this order.
func appConfigFileNames() []string {
	return []string{"config.yaml", "config.yml", "instances.yaml", "instances.yml"}
}

// executableSuffixes are the name suffixes that make a file of an
// application executable whatever its mode.
func executableSuffixes() []string {
	return []string{luaSuffix, ".so", ".dylib"}
}

// rsaSigner writes the hashes files of an environment and signs them with
// an RSA private key.
type rsaSigner struct {
	privateKey *rsa.PrivateKey
}

var _ Signer = rsaSigner{privateKey: nil}

// Sign writes the hashes files of the applications and then the one of the
// environment rooted at basePath, each with its signature. A nil appNames
// signs the environment root as one directory application; otherwise each
// name is a single-file application when <name>.lua is a regular file in
// basePath and a directory application otherwise.
func (signer rsaSigner) Sign(basePath string, appNames []string) error {
	basePath = filepath.Clean(basePath)

	if appNames == nil {
		err := signer.signDirApplication(basePath, basePath)
		if err != nil {
			return err
		}
	}

	for _, name := range appNames {
		err := signer.signApplication(basePath, name)
		if err != nil {
			return err
		}
	}

	digests, err := collectEnvironment(basePath)
	if err != nil {
		return err
	}

	return signer.writeHashes(filepath.Join(basePath, envHashesFileName), digests)
}

// signApplication signs the application name of the environment rooted at
// basePath.
func (signer rsaSigner) signApplication(basePath, name string) error {
	probe := filepath.Join(basePath, name+luaSuffix)

	info, err := os.Stat(probe)

	switch {
	case err == nil && info.Mode().IsRegular():
		return signer.signSingleFileApplication(basePath, name)
	case err == nil:
		return fmt.Errorf("application %q: %q is %w", name, probe, errNotRegularFile)
	case errors.Is(err, fs.ErrNotExist):
		return signer.signDirApplication(basePath, filepath.Join(basePath, name))
	default:
		return fmt.Errorf("failed to find application %q: %w", name, err)
	}
}

// signDirApplication signs the directory application in dir.
func (signer rsaSigner) signDirApplication(basePath, dir string) error {
	digests, err := collectDirApplication(basePath, dir)
	if err != nil {
		return err
	}

	return signer.writeHashes(filepath.Join(dir, appHashesFileName), digests)
}

// signSingleFileApplication signs the single-file application name: the
// file that instances.enabled holds for it is listed in the hashes file of
// the directory instances.enabled/<name>.
func (signer rsaSigner) signSingleFileApplication(basePath, name string) error {
	enabledDir := filepath.Join(basePath, instancesEnabledDir)
	appFile := filepath.Join(enabledDir, name+luaSuffix)
	hashesDir := filepath.Join(enabledDir, name)

	digest, err := hashFile(appFile)
	if err != nil {
		return fmt.Errorf("failed to sign application %q: %w", name, err)
	}

	relPath, err := filepath.Rel(hashesDir, appFile)
	if err != nil {
		return fmt.Errorf("failed to sign application %q: %w", name, err)
	}

	err = os.MkdirAll(hashesDir, hashesDirMode)
	if err != nil {
		return fmt.Errorf("failed to create hashes directory: %w", err)
	}

	digests := []fileDigest{{path: filepath.ToSlash(relPath), digest: digest}}

	return signer.writeHashes(filepath.Join(hashesDir, appHashesFileName), digests)
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

// collectDirApplication returns the files of the directory application in
// dir: its configuration files, then its executable files in walk order,
// leaving out hashes files and signatures. The directories bin and modules
// of the environment are not walked.
func collectDirApplication(basePath, dir string) ([]fileDigest, error) {
	// Looking up the configuration files or walking would fail as well on a
	// missing directory or on a file; checking it first reports it as what
	// it is, the directory of the application.
	info, err := os.Stat(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to find application directory %q: %w", dir, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("application directory %q is %w", dir, errNotDirectory)
	}

	digests, err := collectConfigFiles(dir)
	if err != nil {
		return nil, err
	}

	listed := make(map[string]bool, len(digests))
	for _, file := range digests {
		listed[file.path] = true
	}

	skipped := []string{
		filepath.Join(basePath, binDirName),
		filepath.Join(basePath, modulesDirName),
	}

	walker := treeWalker{
		skipDir: func(path string) bool {
			return path == skipped[0] || path == skipped[1]
		},
		visit: func(relPath, path string, info fs.FileInfo) error {
			if listed[relPath] || isSignerOutput(path) || !isExecutable(relPath, info.Mode()) {
				return nil
			}

			digest, err := hashFile(path)
			if err != nil {
				return err
			}

			digests = append(digests, fileDigest{path: relPath, digest: digest})

			return nil
		},
	}

	err = walker.walk(dir, "")
	if err != nil {
		return nil, err
	}

	return digests, nil
}

// collectConfigFiles returns the configuration files that exist in the
// application directory dir, in their order.
func collectConfigFiles(dir string) ([]fileDigest, error) {
	var digests []fileDigest

	for _, name := range appConfigFileNames() {
		path := filepath.Join(dir, name)

		_, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("failed to find configuration file: %w", err)
		}

		digest, err := hashFile(path)
		if err != nil {
			return nil, err
		}

		digests = append(digests, fileDigest{path: name, digest: digest})
	}

	return digests, nil
}

// collectEnvironment returns the files of the environment rooted at
// basePath: every file under bin, then every file under modules, then
// tt.yaml. A bin or a modules without a directory entry lists nothing.
func collectEnvironment(basePath string) ([]fileDigest, error) {
	var digests []fileDigest

	walker := treeWalker{
		skipDir: func(string) bool { return false },
		visit: func(relPath, path string, _ fs.FileInfo) error {
			digest, err := hashFile(path)
			if err != nil {
				return err
			}

			digests = append(digests, fileDigest{path: relPath, digest: digest})

			return nil
		},
	}

	for _, name := range []string{binDirName, modulesDirName} {
		root := filepath.Join(basePath, name)

		_, err := os.Lstat(root)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return nil, fmt.Errorf("failed to find environment directory: %w", err)
		}

		err = walker.walk(root, name)
		if err != nil {
			return nil, err
		}
	}

	digest, err := hashFile(filepath.Join(basePath, ttConfigFileName))
	if err != nil {
		return nil, fmt.Errorf("failed to sign environment: %w", err)
	}

	return append(digests, fileDigest{path: ttConfigFileName, digest: digest}), nil
}

// isSignerOutput reports whether the file at path has the name of a file
// that Sign writes: a hashes file or a signature. An application never
// lists one, whatever its mode: writing it would change the digest just
// listed, and the signed application would fail every check.
func isSignerOutput(path string) bool {
	switch filepath.Base(path) {
	case appHashesFileName, appHashesFileName + signatureSuffix,
		envHashesFileName, envHashesFileName + signatureSuffix:
		return true
	default:
		return false
	}
}

// isExecutable reports whether an application file is listed in its
// hashes file: by its name, or by the execute bits of its mode.
func isExecutable(name string, mode fs.FileMode) bool {
	for _, suffix := range executableSuffixes() {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}

	return mode&executeBits != 0
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
