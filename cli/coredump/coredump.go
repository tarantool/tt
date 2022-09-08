package coredump

import (
	"archive/tar"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/util"
)

//go:embed scripts/*
var corescripts embed.FS

// MakeTarGzReader makes reader for tar.gz file.
func MakeTarGzReader(archive *os.File) (*tar.Reader, error) {
	uncompressedStream, err := gzip.NewReader(archive)
	if err != nil {
		return nil, err
	}

	tarReader := tar.NewReader(uncompressedStream)
	return tarReader, err
}

// ExtractTarGz extracts tar.gz archive.
func ExtractTarGz(tarName string) error {

	path, err := filepath.Abs(tarName)
	if err != nil {
		return err
	}
	archive, err := os.Open(path)
	defer archive.Close()
	if err != nil {
		return err
	}
	tarReader, err := MakeTarGzReader(archive)
	if err != nil {
		return err
	}
	for {
		header, err := tarReader.Next()

		if err == io.EOF {
			break
		} else if err != nil {
			return err
		}

		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg:
			var pos int
			// Some archives have strange order of objects,
			// so we check that all folders exist before
			// creating a file.
			pos = strings.LastIndex(header.Name, "/")
			if pos == -1 {
				pos = 0
			}
			if _, err := os.Stat(header.Name[0:pos]); os.IsNotExist(err) {
				// 0755:
				//    user:   read/write/execute
				//    group:  read/execute
				//    others: read/execute
				os.MkdirAll(header.Name[0:pos], 0755)
			}
			outFile, err := os.Create(header.Name)
			if err != nil {
				outFile.Close()
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return err
			}
			outFile.Close()

		default:
			return fmt.Errorf("unknown type: %b in %s", header.Typeflag, header.Name)
		}

	}
	return nil
}

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
	err = ExtractTarGz(tarPath)
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
