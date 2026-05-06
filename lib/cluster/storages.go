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

// getConfigPrefix returns a full configuration prefix.
func getConfigPrefixNew(basePrefix string) string {
	prefix := strings.TrimRight(basePrefix, "/")
	return fmt.Sprintf("%s/%s", prefix, "config")
}

// getConfigKey returns a full path to a configuration key.
func getConfigKey(basePrefix, key string) string {
	return getConfigPrefix(basePrefix) + key
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
