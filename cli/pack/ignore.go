package pack

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// readPackIgnore reads the .packignore file and returns a slice of ignore patterns.
func readPackIgnore(projectPath string) (map[string]struct{}, error) {
	ignoreFilePath := filepath.Join(projectPath, ".packignore")
	file, err := os.Open(ignoreFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]struct{}{}, nil
		}
		return nil, err
	}
	defer file.Close()

	patterns := make(map[string]struct{})
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		patterns[line] = struct{}{}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return patterns, nil
}

// shouldIgnore checks if the given file path matches any of the ignore patterns.
func shouldIgnore(path string, patterns map[string]struct{}) (bool, error) {
	for pattern := range patterns {
		pattern = filepath.ToSlash(pattern)
		filePath := filepath.ToSlash(path)

		if strings.HasPrefix(filePath, pattern) {
			return true, nil
		}

		match, err := filepath.Match(pattern, filePath)
		if err != nil {
			return false, err
		}
		if match {
			return true, nil
		}
	}
	return false, nil
}

// removeIgnoredFiles walks through the directory and removes files or directories
// that match the ignore patterns.
func removeIgnoredFiles(bundleEnvPath string, patterns map[string]struct{}) error {
	return filepath.Walk(bundleEnvPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		relPath, err := filepath.Rel(bundleEnvPath, path)
		if err != nil {
			return err
		}

		relPathUnix := filepath.ToSlash(relPath)

		ignore, err := shouldIgnore(relPathUnix, patterns)
		if err != nil {
			return err
		}

		if ignore {
			if info.IsDir() {
				err = os.RemoveAll(path)
				if err != nil {
					return fmt.Errorf("failed to remove directory %q: %s", path, err)
				}
				return filepath.SkipDir
			} else {
				err = os.Remove(path)
				if err != nil {
					return fmt.Errorf("failed to remove file %q: %s", path, err)
				}
			}
		}
		return nil
	})
}
