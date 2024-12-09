package coredump

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/util"
)

//go:embed scripts/*
var corescripts embed.FS

//go:embed extensions
var extensions embed.FS

const packEmbedPath = "scripts/tarabrt.sh"
const inspectEmbedPath = "scripts/gdb.sh"

// Pack packs coredump into a tar.gz archive.
func Pack(corePath string, executable string, outputDir string, pid uint, time string) error {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "tt-coredump-*")
	if err != nil {
		return fmt.Errorf("cannot create a temporary directory for archiving: %v", err)
	}
	defer os.RemoveAll(tmpDir) // Clean up on function return.

	scriptArgs := []string{"-c", corePath}
	if executable != "" {
		scriptArgs = append(scriptArgs, "-e", executable)
	}
	if outputDir != "" {
		scriptArgs = append(scriptArgs, "-d", outputDir)
	}
	if pid != 0 {
		scriptArgs = append(scriptArgs, "-p", strconv.FormatUint(uint64(pid), 10))
	}

	// Prepare gdb wrapper for packing.
	inspectPath := filepath.Join(tmpDir, filepath.Base(inspectEmbedPath))
	err = util.FsCopyFileChangePerms(corescripts, inspectEmbedPath, inspectPath, 0755)
	if err != nil {
		return fmt.Errorf("failed to put the inspecting script into the archive: %v", err)
	}
	scriptArgs = append(scriptArgs, "-g", inspectPath)

	// Prepare gdb extensions for packing.
	const extDirName = "extensions"
	extEntries, err := extensions.ReadDir(extDirName)
	if err != nil {
		return fmt.Errorf("failed to find embedded GDB-extensions: %v", err)
	}
	for _, extEntry := range extEntries {
		extSrc := filepath.Join(extDirName, extEntry.Name())
		extDst := filepath.Join(tmpDir, extEntry.Name())
		err = util.FsCopyFileChangePerms(extensions, extSrc, extDst, 0644)
		if err != nil {
			return fmt.Errorf("failed to put GDB-extension into the archive: %v", err)
		}
		scriptArgs = append(scriptArgs, "-x", extDst)
	}

	script, err := corescripts.Open(packEmbedPath)
	if err != nil {
		return fmt.Errorf("failed to open pack script: %v", err)
	}
	cmdArgs := []string{"-s", "--"}
	cmd := exec.Command("bash", append(cmdArgs, scriptArgs...)...)
	cmd.Stdin = script
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("pack script execution failed: %v", err)
	}
	log.Info("Core was successfully packed.")
	return nil
}

// Unpack unpacks a tar.gz archive.
func Unpack(archivePath string) error {
	err := util.ExtractTarGz(archivePath, ".")
	if err != nil {
		return fmt.Errorf("failed to unpack: %v", err)
	}
	log.Info("Archive was successfully unpacked.")
	return nil
}

// Inspect allows user to inspect coredump.
func Inspect(archiveOrDir string, sourceDir string) error {
	stat, err := os.Stat(archiveOrDir)
	if err != nil {
		return fmt.Errorf("failed to inspect: %v", err)
	}

	var dir string
	if stat.IsDir() {
		dir = archiveOrDir

	} else {
		// It seems archive was specified, so try to unpack into
		// temporary directory.
		tmpDir, err := os.MkdirTemp(os.TempDir(), "tt-coredump-*")
		if err != nil {
			return fmt.Errorf("cannot create a temporary directory for unpacking: %v", err)
		}
		defer os.RemoveAll(tmpDir) // Clean up on function return.

		err = util.ExtractTarGz(archiveOrDir, tmpDir)
		if err != nil {
			return fmt.Errorf("failed to unpack: %v", err)
		}

		// Directory name is archive basename w/o extensions.
		dir = strings.Split(filepath.Base(archiveOrDir), ".")[0]
		// Compose full path to unpacked directory.
		dir = filepath.Join(tmpDir, dir)
	}

	// First, try to find gdb wrapper within the unpacked directory.
	scriptPath := filepath.Join(dir, filepath.Base(inspectEmbedPath))
	_, err = os.Stat(scriptPath)
	if errors.Is(err, fs.ErrNotExist) {
		// If the wrapper is missing in archive, then use the embedded one.
		err = util.FsCopyFileChangePerms(corescripts, inspectEmbedPath, scriptPath, 0755)
	}

	if err != nil {
		return fmt.Errorf("failed to find inspect script: %v", err)
	}

	scriptArgs := []string{}
	if len(sourceDir) > 0 {
		scriptArgs = append(scriptArgs, "-s", sourceDir)
	}

	// GDB-wrapper use standard input, so we need to launch it directly
	// rather than pass it over standard input to bash -s.
	cmd := exec.Command(scriptPath, scriptArgs...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Run()
	if err != nil {
		return fmt.Errorf("inspect script execution failed: %v", err)
	}
	return nil
}
