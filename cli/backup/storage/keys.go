package storage

import "fmt"

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
