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
func Pack(coreName string) error {
	corePath, err := findFile(coreName)
	if err != nil {
		return fmt.Errorf("There was some problem packing archive. "+
			"Error: '%v'.", err)
	}
	content, err := util.ReadEmbedFile(corescripts, "scripts/tarabart.sh")
	if err != nil {
		return fmt.Errorf("There was some problem packing archive. "+
			"Error: '%v'.", err)
	}
	cmd := exec.Command("bash", "-s")
	StdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("There was some problem packing archive. "+
			"Error: '%v'.", err)
	}
	cmd.Env = append(cmd.Env, "COREFILE_ENV="+corePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	StdinPipe.Write([]byte(content))
	StdinPipe.Close()
	err = cmd.Wait()
	if err == nil {
		log.Infof("Core was successfully packed.")
	} else {
		err = fmt.Errorf("There was some problem packing archive. "+
			"Error: '%v'.", err)
	}
	return err
}

// Unpack unpacks a tar.gz archive.
func Unpack(tarName string) error {
	tarPath, err := findFile(tarName)
	if err != nil {
		return fmt.Errorf("There was some problem unpacking archive. "+
			"Error: '%v'.", err)
	}
	err = util.ExtractTarGz(tarPath, ".")
	if err == nil {
		log.Infof("Archive was successfully unpacked. \n")
	} else {
		err = fmt.Errorf("There was some problem unpacking archive. "+
			"Error: '%v'.", err)
	}
	return err
}

// Inspect allows user to inspect unpacked coredump.
func Inspect(coreFolder string) error {
	corePath, err := findFile(coreFolder)
	if err != nil {
		return fmt.Errorf("There was some problem inspecting archive. "+
			"Error: '%v'.", err)
	}
	content, err := util.ReadEmbedFile(corescripts, "scripts/gdb.sh")
	if err != nil {
		return fmt.Errorf("There was some problem inspecting coredump. "+
			"Error: '%v'.", err)
	}
	cmd := exec.Command("bash", "-s")
	StdinPipe, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("There was some problem inspecting coredump. "+
			"Error: '%v'.", err)
	}
	cmd.Env = append(cmd.Env, "COREFOLDER_ENV="+corePath)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err = cmd.Start()
	StdinPipe.Write([]byte(content))
	StdinPipe.Close()
	err = cmd.Wait()
	if err != nil {
		err = fmt.Errorf("There was some problem inspecting coredump. "+
			"Error: '%v'.", err)
	}
	return err
}
