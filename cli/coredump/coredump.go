package coredump

import (
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/util"
)

//go:embed scripts/*
var corescripts embed.FS

// findFile makes absolute path and checks that file exists.
func findFile(fileName string) (string, error) {
	filePath, err := filepath.Abs(fileName)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(filePath); err != nil {
		return "", err
	}
	return filePath, err
}

// Pack packs coredump into a tar.gz archive.
func Pack(corePath string) error {
	script, err := corescripts.Open("scripts/tarabrt.sh")
	if err != nil {
		return fmt.Errorf("there was some problem packing archive. "+
			"Error: '%v'", err)
	}
	cmd := exec.Command("bash", "-s", "--", "-c", corePath)
	cmd.Stdin = script
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("there was some problem packing archive. "+
			"Error: '%v'", err)
	}
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("there was some problem packing archive. "+
			"Error: '%v'", err)
	}
	log.Infof("Core was successfully packed.")
	return nil
}

// Unpack unpacks a tar.gz archive.
func Unpack(tarName string) error {
	tarPath, err := findFile(tarName)
	if err != nil {
		return fmt.Errorf("there was some problem unpacking archive. "+
			"Error: '%v'", err)
	}
	err = util.ExtractTarGz(tarPath, ".")
	if err != nil {
		return fmt.Errorf("there was some problem unpacking archive. "+
			"Error: '%v'", err)
	}
	log.Infof("Archive was successfully unpacked. \n")
	return nil
}

// Inspect allows user to inspect unpacked coredump.
func Inspect(coreFolder string) error {
	corePath, err := findFile(coreFolder)
	if err != nil {
		return fmt.Errorf("there was some problem inspecting archive. "+
			"Error: '%v'", err)
	}
	script, err := corescripts.Open("scripts/gdb.sh")
	if err != nil {
		return fmt.Errorf("there was some problem inspecting coredump. "+
			"Error: '%v'", err)
	}
	cmd := exec.Command("bash", "-s")
	cmd.Env = append(cmd.Env, "COREFOLDER_ENV="+corePath)
	cmd.Stdin = script
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	if err != nil {
		return fmt.Errorf("there was some problem inspecting coredump. "+
			"Error: '%v'", err)
	}
	err = cmd.Wait()
	if err != nil {
		return fmt.Errorf("there was some problem inspecting coredump. "+
			"Error: '%v'", err)
	}
	return nil
}
