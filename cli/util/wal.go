package util

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/apex/log"
)

const (
	snapSuffix = ".snap"
	xlogSuffix = ".xlog"
)

// hasExt checks if the file name ends with the given suffix and is longer than the suffix itself.
func hasExt(f, s string) bool {
	return strings.HasSuffix(f, s) && len(f) > len(s)
}

// isWAL checks if a file name has a .snap or .xlog extension.
func isWal(f string) bool {
	return hasExt(f, snapSuffix) || hasExt(f, xlogSuffix)
}

// sortWalFiles sorts a slice of file paths, ensuring .snap files come before .xlog files.
// Files of the same type are sorted lexicographically.
// The sorting happens in place.
func sortWalFiles(files []string) {
	slices.SortFunc(files, func(left, right string) int {
		if hasExt(left, snapSuffix) && hasExt(right, xlogSuffix) {
			return -1
		}
		if hasExt(left, xlogSuffix) && hasExt(right, snapSuffix) {
			return 1
		}
		lDir, fName := filepath.Split(left)
		rDir, rName := filepath.Split(right)
		if lDir != rDir {
			return strings.Compare(lDir, rDir)
		}
		return strings.Compare(fName, rName)
	})
}

// collectWALsFromSinglePath is an internal helper to find WAL files starting from a single path.
// It handles both files and directories, respecting the isRecursive flag for directories.
// It returns absolute paths of found WAL files.
func collectWALsFromSinglePath(path string, isRecursive bool) ([]string, error) {
	collected := []string{}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat path %q: %w", path, err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute path for %q: %w", path, err)
	}

	if !info.IsDir() {
		if isWal(info.Name()) {
			collected = append(collected, path)
		}
		return collected, nil
	}

	// Handle the case where path is a directory.
	if isRecursive {
		err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				log.Warnf("Skipping %q due to error during walk: %s", p, err)
				if d != nil && d.IsDir() && errors.Is(err, fs.ErrPermission) {
					// Skip directory if permission denied, but continue walking other parts.
					return fs.SkipDir
				}
				return nil
			}

			if !d.IsDir() && isWal(d.Name()) {
				collected = append(collected, p)
			}
			return nil
		})
		if err != nil {
			log.Warnf("Error encountered during recursive walk of %q: %s", path, err)
		}

	} else {
		dirEntries, readErr := os.ReadDir(path)
		if readErr != nil {
			log.Warnf("Failed to read directory %q: %s", path, readErr)
			return collected, nil
		}
		for _, entry := range dirEntries {
			if !entry.IsDir() && isWal(entry.Name()) {
				collected = append(collected, filepath.Join(path, entry.Name()))
			}
		}
	}

	return collected, nil
}

// CollectWalFiles collects WAL (Write-Ahead Log) files based on the given
// set of paths. It identifies files with ".snap" or ".xlog" extensions as WAL files.
// For directory paths, it traverses them based on the isRecursive flag.
// It skips directories or files it cannot access due to permissions, logging warnings.
//
// The function ensures that ".snap" files are sorted before ".xlog" files in the result,
// and that the returned list contains unique absolute paths.
//
// Returns: A sorted, unique slice of strings containing the absolute paths of collected WAL files.
func CollectWalFiles(paths []string, isRecursive bool) ([]string, error) {
	allCollectedFiles := make([]string, 0)

	for _, p := range paths {
		filesFromPath, err := collectWALsFromSinglePath(p, isRecursive)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("required %q not found: %w", p, err)
			}
			log.Warnf("Error processing path %q: %v. Skipping this path.", p, err)
			continue
		}
		if len(filesFromPath) == 0 {
			log.Warnf("No WAL files found at %q", p)
		} else {
			allCollectedFiles = append(allCollectedFiles, filesFromPath...)
		}
	}

	// Sort the aggregated list.
	sortWalFiles(allCollectedFiles)

	// For the case a file is included both as an individual file and as part of a directory.
	return slices.Compact(allCollectedFiles), nil
}
