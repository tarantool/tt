package integrity

import (
	"bytes"
	"crypto/rsa"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

var (
	errHashMismatch         = errors.New("hash mismatch")
	errNotInRepository      = errors.New("file is not in repository")
	errUnsupportedAlgorithm = errors.New("unsupported hash algorithm")
)

// rsaRepository is a Repository that knows the files listed in the signed
// hashes files of an environment, each by its path with symbolic links
// resolved as they were when the repository was loaded.
type rsaRepository struct {
	// digests maps a resolved path to the SHA-256 digest of its content.
	digests map[string][]byte
	// paths are the keys of digests in the order they were listed.
	paths []string
}

var _ Repository = (*rsaRepository)(nil)

// Read resolves path, relative to the working directory, checks that the
// file it resolves to is known and unmodified, and returns it opened for
// reading.
func (repository *rsaRepository) Read(path string) (io.ReadCloser, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", path, err)
	}

	resolved, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read %q: %w", path, err)
	}

	digest, ok := repository.digests[resolved]
	if !ok {
		return nil, fmt.Errorf("%w: %q", errNotInRepository, resolved)
	}

	file, err := openRegularFile(resolved)
	if err != nil {
		return nil, err
	}

	err = checkOpenFile(file, resolved, digest)
	if err != nil {
		return nil, errors.Join(err, file.Close())
	}

	return file, nil
}

// ValidateAll checks every known file against its digest, in the order the
// files were listed, and returns the first failure.
func (repository *rsaRepository) ValidateAll() error {
	for _, path := range repository.paths {
		err := checkFile(path, path, repository.digests[path])
		if err != nil {
			return err
		}
	}

	return nil
}

// checkOpenFile hashes the content of file, the file at path, compares it
// with digest and rewinds the file.
func checkOpenFile(file *os.File, path string, digest []byte) error {
	actual, err := hashContent(file)
	if err != nil {
		return err
	}

	err = compareDigests(path, digest, actual)
	if err != nil {
		return err
	}

	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to rewind file: %w", err)
	}

	return nil
}

// loadRepository checks the environment whose hashes files are in
// configDir with publicKey and returns the repository of the files they
// list.
func loadRepository(publicKey *rsa.PublicKey, configDir string) (*rsaRepository, error) {
	dir, err := filepath.Abs(configDir)
	if err != nil {
		return nil, fmt.Errorf("failed to find hashes files: %w", err)
	}

	hashesPaths, err := checkedHashesFiles(dir)
	if err != nil {
		return nil, err
	}

	var records []hashRecord

	for _, hashesPath := range hashesPaths {
		fileRecords, err := loadHashesFile(publicKey, hashesPath)
		if err != nil {
			return nil, err
		}

		records = append(records, fileRecords...)
	}

	repository := &rsaRepository{
		digests: make(map[string][]byte, len(records)),
		paths:   make([]string, 0, len(records)),
	}

	for _, record := range records {
		err = repository.register(record)
		if err != nil {
			return nil, err
		}
	}

	return repository, nil
}

// checkedHashesFiles returns the hashes files that are checked in the
// directory dir: none when it has no environment hashes file, otherwise
// that file and, when it exists, the hashes file of the application. A
// hashes file is missing only when there is no directory entry of its
// name: a symbolic link that does not resolve is returned, and loading it
// fails.
func checkedHashesFiles(dir string) ([]string, error) {
	envPath := filepath.Join(dir, envHashesFileName)

	exists, err := entryExists(envPath)
	if err != nil || !exists {
		return nil, err
	}

	appPath := filepath.Join(dir, appHashesFileName)

	exists, err = entryExists(appPath)
	if err != nil {
		return nil, err
	}

	if !exists {
		return []string{envPath}, nil
	}

	return []string{envPath, appPath}, nil
}

// entryExists reports whether there is a directory entry at path, without
// following a symbolic link there. An error other than not-found is
// returned.
func entryExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("failed to find hashes file: %w", err)
	}

	return true, nil
}

// loadHashesFile verifies the signature of the hashes file at path with
// publicKey and parses it. The paths of the records are joined to the
// directory of the hashes file.
func loadHashesFile(publicKey *rsa.PublicKey, path string) ([]hashRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read hashes file: %w", err)
	}

	signature, err := os.ReadFile(path + signatureSuffix)
	if err != nil {
		return nil, fmt.Errorf("failed to read signature of %q: %w", path, err)
	}

	err = verifyData(publicKey, data, signature)
	if err != nil {
		return nil, fmt.Errorf("hashes file %q: %w", path, err)
	}

	records, err := decodeHashes(path, data)
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	for index := range records {
		records[index].path = filepath.Join(dir, filepath.FromSlash(records[index].path))
	}

	return records, nil
}

// register checks the file of record and adds it to the repository under
// its resolved path.
func (repository *rsaRepository) register(record hashRecord) error {
	if record.algorithm != sha256Algorithm {
		return fmt.Errorf("%w %q for %q", errUnsupportedAlgorithm, record.algorithm, record.path)
	}

	resolved, err := filepath.EvalSymlinks(record.path)
	if err != nil {
		return fmt.Errorf("failed to check %q: %w", record.path, err)
	}

	err = checkFile(record.path, resolved, record.digest)
	if err != nil {
		return err
	}

	_, known := repository.digests[resolved]
	if !known {
		repository.paths = append(repository.paths, resolved)
	}

	repository.digests[resolved] = record.digest

	return nil
}

// checkFile hashes the file at path and compares its digest with digest.
// A mismatch is reported for name.
func checkFile(name, path string, digest []byte) error {
	actual, err := hashFile(path)
	if err != nil {
		return err
	}

	return compareDigests(name, digest, actual)
}

// compareDigests reports a mismatch of the digest of the file name.
func compareDigests(name string, expected, actual []byte) error {
	if bytes.Equal(expected, actual) {
		return nil
	}

	return fmt.Errorf("%w for %q: expected %q, got %q", errHashMismatch, name,
		hex.EncodeToString(expected), hex.EncodeToString(actual))
}
