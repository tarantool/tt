package analyzer

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// onceDir is the directory of the markers of First, in the cache.
const onceDir = "once"

// First reports whether this is the first time key is asked about with
// cache, the cache of the module (DefaultCacheDir): it leaves a marker
// named after the sha256 of key in the cache, and a later call with the
// same key finds it and reports false. With no cache, or one it cannot
// write, every call is the first: a warning it guards is shown rather than
// lost.
func First(cache string, key ...string) bool {
	if cache == "" {
		return true
	}

	sum := sha256.Sum256([]byte(strings.Join(key, "\x00")))
	path := filepath.Join(cache, onceDir, hex.EncodeToString(sum[:]))

	err := os.MkdirAll(filepath.Dir(path), dirPerm)
	if err != nil {
		return true
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, filePerm)
	if errors.Is(err, fs.ErrExist) {
		return false
	}

	if err != nil {
		return true
	}

	_, _ = file.WriteString(strings.Join(key, "\n") + "\n")
	_ = file.Close()

	return true
}
