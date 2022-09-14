package pack

import (
	"github.com/otiai10/copy"
)

// copyBundleDir copies a prepared bundle content to the passed package directory.
func copyBundleDir(packagePath, bundlePath string) error {
	err := copy.Copy(bundlePath, packagePath)
	if err != nil {
		return err
	}

	return nil
}
