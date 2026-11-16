package extmod

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

// cacheDirPerm and cacheFilePerm are the modes of what the cache writes.
const (
	cacheDirPerm  = 0o755
	cacheFilePerm = 0o644
)

// cacheKeyLen is the number of bytes of the hash of a path a cache file is
// named by.
const cacheKeyLen = 16

// DefaultCacheDir is where the descriptions of external modules are kept:
// <user cache>/tt/modules, or nowhere when the system names no cache
// directory.
func DefaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, "tt", "modules")
}

// cachedReply is the answer of an executable to --description --version, as
// the cache keeps it: with the executable it came from, by its absolute
// path, size and modification time, which must all be the same for the
// answer to hold.
type cachedReply struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime_ns"`
	Version string `json:"version"`
	Help    string `json:"help"`
}

// replyKey returns what the reply of the executable at path is kept by, and
// false when the executable cannot be looked at.
func replyKey(path string) (cachedReply, bool) {
	var none cachedReply

	abs, err := filepath.Abs(path)
	if err != nil {
		return none, false
	}

	info, err := os.Stat(abs)
	if err != nil {
		return none, false
	}

	return cachedReply{
		Path: abs, Size: info.Size(), ModTime: info.ModTime().UnixNano(),
		Version: "", Help: "",
	}, true
}

// cacheFile returns the file of dir the reply of key is kept in.
func cacheFile(dir string, key cachedReply) string {
	sum := sha256.Sum256([]byte(key.Path))

	return filepath.Join(dir, hex.EncodeToString(sum[:cacheKeyLen])+".json")
}

// loadReply returns the reply dir keeps for key, and false when it keeps
// none for the executable as it is now.
func loadReply(dir string, key cachedReply) (cachedReply, bool) {
	var none, cached cachedReply

	if dir == "" {
		return none, false
	}

	data, err := os.ReadFile(cacheFile(dir, key))
	if err != nil {
		return none, false
	}

	err = json.Unmarshal(data, &cached)
	if err != nil || cached.Path != key.Path || cached.Size != key.Size ||
		cached.ModTime != key.ModTime {
		return none, false
	}

	return cached, true
}

// storeReply keeps reply in dir. A cache that cannot be written is not one:
// the module is asked again next time.
func storeReply(dir string, reply cachedReply) {
	if dir == "" {
		return
	}

	data, err := json.Marshal(reply)
	if err != nil {
		return
	}

	err = os.MkdirAll(dir, cacheDirPerm)
	if err != nil {
		return
	}

	// Written aside and renamed over, so that a tt reading it at the same
	// time sees the old file or the new one.
	tmp, err := os.CreateTemp(dir, ".reply-*")
	if err != nil {
		return
	}

	_, err = tmp.Write(data)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Chmod(tmp.Name(), cacheFilePerm)
	}

	if err == nil {
		err = os.Rename(tmp.Name(), cacheFile(dir, reply))
	}

	if err != nil {
		_ = os.Remove(tmp.Name())
	}
}
