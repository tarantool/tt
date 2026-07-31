package storage

import (
	"fmt"
	"strings"
)

// ManifestsPrefix returns the relative key prefix for backup manifests.
func ManifestsPrefix() string {
	return "manifests/"
}

// DataPrefix returns the relative key prefix for backup archives.
func DataPrefix() string {
	return "data/"
}

// ManifestKey returns a relative key for a cluster manifest object.
func ManifestKey(backupID string) string {
	return fmt.Sprintf("%s%s.json", ManifestsPrefix(), backupID)
}

// ArchiveKey returns a relative key for a replicaset backup archive object.
func ArchiveKey(backupID, replicasetUUID string) string {
	return fmt.Sprintf("%s%s-%s.tar.zst", DataPrefix(), backupID, replicasetUUID)
}

// ManifestBackupID returns the backup id a manifest key belongs to. It is the
// inverse of ManifestKey, used to name an object whose content could not be
// read; false means the key is not a manifest key.
func ManifestBackupID(key string) (string, bool) {
	backupID, ok := strings.CutPrefix(key, ManifestsPrefix())
	if !ok {
		return "", false
	}

	backupID, ok = strings.CutSuffix(backupID, ".json")
	if !ok || backupID == "" {
		return "", false
	}

	return backupID, true
}

// uuidLength is the length of a canonical replicaset UUID (8-4-4-4-12).
const uuidLength = 36

// ArchiveBackupID splits an archive key into the backup id and the replicaset
// UUID it was produced for. It is the inverse of ArchiveKey: a backup id may
// itself contain dashes, so the key is parsed from the right, where the UUID has
// a fixed shape. False means the key does not name an archive of this layout.
func ArchiveBackupID(key string) (string, string, bool) {
	name, ok := strings.CutPrefix(key, DataPrefix())
	if !ok {
		return "", "", false
	}

	name, ok = strings.CutSuffix(name, ".tar.zst")
	if !ok {
		return "", "", false
	}

	// The separator sits right before the trailing UUID; anything before it is
	// the backup id, which must not be empty.
	separator := len(name) - uuidLength - 1
	if separator < 1 || name[separator] != '-' {
		return "", "", false
	}

	replicasetUUID := name[separator+1:]
	if !isUUID(replicasetUUID) {
		return "", "", false
	}

	return name[:separator], replicasetUUID, true
}

// isUUID reports whether s is a canonical lowercase-or-uppercase hex UUID.
func isUUID(s string) bool {
	if len(s) != uuidLength {
		return false
	}

	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexDigit(r) {
				return false
			}
		}
	}

	return true
}

// isHexDigit reports whether r is a hexadecimal digit.
func isHexDigit(r rune) bool {
	switch {
	case r >= '0' && r <= '9', r >= 'a' && r <= 'f', r >= 'A' && r <= 'F':
		return true
	default:
		return false
	}
}
