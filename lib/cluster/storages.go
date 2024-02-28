package cluster

import (
	"fmt"
	"strings"
)

// getConfigPrefix returns a full configuration prefix.
func getConfigPrefix(basePrefix string) string {
	prefix := strings.TrimRight(basePrefix, "/")
	return fmt.Sprintf("%s/%s/", prefix, "config")
}

// getConfigKey returns a full path to a configuration key.
func getConfigKey(basePrefix, key string) string {
	return getConfigPrefix(basePrefix) + key
}

// getHashesPrefix returns a full hashes prefix.
func getHashesPrefix(basePrefix string) string {
	prefix := strings.TrimRight(basePrefix, "/")
	return fmt.Sprintf("%s/%s/", prefix, "hashes")
}

// getHashesKey returns a full path to a hash of a key.
func getHashesKey(basePrefix, hash, key string) string {
	return getHashesPrefix(basePrefix) + fmt.Sprintf("%s/%s", hash, key)
}

// getSignPrefix returns a full sign prefix.
func getSignPrefix(basePrefix string) string {
	prefix := strings.TrimRight(basePrefix, "/")
	return fmt.Sprintf("%s/%s/", prefix, "sig")
}

// getSignKey returns a full path to a sign of a key.
func getSignKey(basePrefix, key string) string {
	return getSignPrefix(basePrefix) + key
}

// parseHashPath parses a full key path and returns true, hash, key on success.
func parseHashPath(path, prefix string) (bool, string, string) {
	hashesPrefix := getHashesPrefix(prefix)
	hashPath, found := strings.CutPrefix(path, hashesPrefix)
	if !found {
		return false, "", ""
	}

	split := strings.SplitN(hashPath, "/", 2)
	if len(split) != 2 {
		return false, "", ""
	}

	return true, split[0], split[1]
}

// GetStorageKey extracts the key from the source that contains the config prefix.
func GetStorageKey(prefix, source string) (string, error) {
	cfgPrefix := getConfigPrefix(prefix)
	key, ok := strings.CutPrefix(source, cfgPrefix)
	if !ok {
		return "", fmt.Errorf("source must begin with: %s", cfgPrefix)
	}
	return key, nil
}
