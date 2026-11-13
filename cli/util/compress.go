package util

import (
	"github.com/tarantool/tt/v3/internal/untar"
)

// ExtractTarGz extracts tar.gz archive into dstDir: its directories, its
// files with the permissions of their entries and its symbolic links. An
// entry outside dstDir, or a link that points outside it, is an error.
func ExtractTarGz(tarName, dstDir string) error {
	return untar.ExtractTarGz(tarName, dstDir, untar.Options{Symlinks: true, EntryModes: true})
}
