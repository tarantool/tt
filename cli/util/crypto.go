package util

import (
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
)

// FileSHA256Hex computes SHA256 for a given file.
// The result is returned in a hex form.
func FileSHA256Hex(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// FileSHA1Hex computes SHA1 for a given file.
// The result is returned in a hex form.
func FileSHA1Hex(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	hasher := sha1.New()
	if _, err := io.Copy(hasher, file); err != nil {
		return "", err
	}

	return fmt.Sprintf("%x", hasher.Sum(nil)), nil
}

// FileMD5 computes MD5 for a given file.
// The result is returned in a binary form.
func FileMD5(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	hasher := md5.New()
	if _, err := io.Copy(hasher, file); err != nil {
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

	return fmt.Sprintf("%x", fileMD5), nil
}

// StringSHA1Hex computes SHA1 for a given string
// The result is returned in a hex form.
func StringSHA1Hex(source string) string {
	hasher := sha1.New()
	hasher.Write([]byte(source))

	return fmt.Sprintf("%x", hasher.Sum(nil))
}
