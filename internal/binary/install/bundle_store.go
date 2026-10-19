package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type stagedBundleDirectory struct {
	destination string
	prepared    string
	previous    string
}

type bundlePlacement struct {
	destination string
	prepare     func(string) error
}

func checkBundleDestination(destination string, reinstall bool) error {
	_, err := os.Lstat(destination)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("failed to inspect version directory: %w", err)
	}

	if !reinstall {
		return fmt.Errorf("%w: %s; use --reinstall to replace an incomplete installation",
			errBundlePathExists, destination)
	}

	return nil
}

func stageBundleDirectory(destination string) (stagedBundleDirectory, error) {
	var staged stagedBundleDirectory

	staged.destination = destination

	err := os.MkdirAll(filepath.Dir(destination), defaultDirPermissions)
	if err != nil {
		return staged, fmt.Errorf("failed to create product directory: %w", err)
	}

	prepared, err := os.MkdirTemp(filepath.Dir(destination), ".install-*")
	if err != nil {
		return staged, fmt.Errorf("failed to stage installation: %w", err)
	}

	staged.prepared = prepared

	err = os.Chmod(prepared, defaultDirPermissions)
	if err != nil {
		_ = os.RemoveAll(prepared)

		return staged, fmt.Errorf("failed to set installation permissions: %w", err)
	}

	return staged, nil
}

// placeBundleArtifacts stages every product before replacing existing versions.
func placeBundleArtifacts(placements []bundlePlacement, reinstall bool) error {
	for _, placement := range placements {
		err := checkBundleDestination(placement.destination, reinstall)
		if err != nil {
			return err
		}
	}

	staged := make([]stagedBundleDirectory, 0, len(placements))
	defer func() {
		for _, directory := range staged {
			_ = os.RemoveAll(directory.prepared)
		}
	}()

	for _, placement := range placements {
		directory, err := stageBundleDirectory(placement.destination)
		if err != nil {
			return err
		}

		staged = append(staged, directory)

		err = placement.prepare(directory.prepared)
		if err != nil {
			return err
		}
	}

	err := commitBundleDirectories(staged)

	return err
}

// commitBundleDirectories keeps replaced directories until all products are placed.
func commitBundleDirectories(staged []stagedBundleDirectory) error {
	for index := range staged {
		directory := &staged[index]

		_, err := os.Lstat(directory.destination)
		if err == nil {
			directory.previous = directory.prepared + ".previous"
			err = os.Rename(directory.destination, directory.previous)
		} else if errors.Is(err, os.ErrNotExist) {
			err = nil
		}

		if err == nil {
			err = os.Rename(directory.prepared, directory.destination)
		}

		if err != nil {
			for undo := index; undo >= 0; undo-- {
				previous := staged[undo]
				if undo < index {
					removeErr := os.RemoveAll(previous.destination)

					err = errors.Join(err, removeErr)
				}

				if previous.previous != "" {
					_, inspectErr := os.Lstat(previous.previous)
					if inspectErr == nil {
						restoreErr := os.Rename(previous.previous, previous.destination)

						err = errors.Join(err, restoreErr)
					}
				}
			}

			return fmt.Errorf("failed to place bundle: %w", err)
		}
	}

	for _, directory := range staged {
		if directory.previous != "" {
			err := os.RemoveAll(directory.previous)
			if err != nil {
				return fmt.Errorf("failed to remove replaced installation: %w", err)
			}
		}
	}

	return nil
}
