package install

import (
	"fmt"

	"github.com/tarantool/tt/cli/manifest/state"
)

// writeMetadata records a freshly installed guest's metadata under
// manifests/<pkg>/. The format and the directory belong to cli/manifest/state,
// which list and uninstall read back; install only supplies the archive's
// manifest, its lock and the version it carried.
func writeMetadata(lay state.Layout, header *Header) error {
	lockBytes, err := header.Lock.Marshal()
	if err != nil {
		return fmt.Errorf("serializing lock metadata: %w", err)
	}

	err = state.WriteMetadata(
		lay, header.Manifest.Package.Name, header.ManifestBytes, lockBytes, header.Version)
	if err != nil {
		return fmt.Errorf("recording install metadata: %w", err)
	}

	return nil
}
