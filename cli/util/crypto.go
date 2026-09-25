package util

import (
	"crypto/md5"  //nolint:gosec // MD5 is a checksum here, not a security primitive.
	"crypto/sha1" //nolint:gosec // SHA1 is a checksum here, not a security primitive.
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

// FileSHA1Hex computes SHA1 for a given file.
// The result is returned in a hex form.
func FileSHA1Hex(path string) (string, error) {
	hasher := sha1.New() //nolint:gosec // SHA1 is a checksum here, not a security primitive.

	err := hashFile(path, hasher)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(hasher.Sum(nil)), nil
}

// FileMD5 computes MD5 for a given file.
// The result is returned in a binary form.
func FileMD5(path string) ([]byte, error) {
	hasher := md5.New() //nolint:gosec // MD5 is a checksum here, not a security primitive.

	err := hashFile(path, hasher)
	if err != nil {
		return nil, err
	}

	return hasher.Sum(nil), nil
}

// FileMD5Hex computes MD5 for a given file.
// The result is returned in a hex form.
func FileMD5Hex(path string) (string, error) {
	fileMD5, err := FileMD5(path)
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(fileMD5), nil
}

// StringSHA1Hex computes SHA1 for a given string
// The result is returned in a hex form.
func StringSHA1Hex(source string) string {
	hasher := sha1.New() //nolint:gosec // SHA1 is a checksum here, not a security primitive.
	hasher.Write([]byte(source))

	return hex.EncodeToString(hasher.Sum(nil))
}
