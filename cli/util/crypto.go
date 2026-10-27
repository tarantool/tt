package util

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
)

// hashFile feeds the content of the file to the hasher.
func hashFile(path string, hasher hash.Hash) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot open the file to hash: %w", err)
	}

	defer func() {
		_ = file.Close()
	}()

	_, err = io.Copy(hasher, file)
	if err != nil {
		return fmt.Errorf("cannot read %q to hash: %w", path, err)
	}

	return nil
}

// FileSHA256Hex computes SHA256 for a given file.
// The result is returned in a hex form.
func FileSHA256Hex(path string) (string, error) {
	hasher := sha256.New()

	err := hashFile(path, hasher)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}
