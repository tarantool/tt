package test_helpers

import (
	"os"
	"path/filepath"
)

func CreateFiles(destPath string, paths []string) error {
	var err error
	for _, item := range paths {
		_, err = os.Create(filepath.Join(destPath, item))
		if err != nil {
			return err
		}
	}
	return nil
}

func CreateDirs(destPath string, paths []string) error {
	var err error
	for _, item := range paths {
		err = os.MkdirAll(filepath.Join(destPath, item), 0750)
		if err != nil {
			return err
		}
	}
	return nil
}

func CreateSymlink(srcPath, destPath string) error {
	err := os.Chdir(filepath.Dir(srcPath))
	if err != nil {
		return err
	}
	return os.Symlink(filepath.Base(srcPath), destPath)
}
