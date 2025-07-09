package pack

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// packCpio runs cpio command and packs the passed directory into the new package.
func packCpio(relPaths []string, resFileName, packageFilesDir string) error {
	cpioFile, err := os.Create(resFileName)
	if err != nil {
		return err
	}
	defer cpioFile.Close()

	cpioFileWriter := bufio.NewWriter(cpioFile)
	defer cpioFileWriter.Flush()

	var stderrBuf bytes.Buffer

	filesBuffer := bytes.Buffer{}
	filesBuffer.WriteString(strings.Join(relPaths, "\n"))

	cmd := exec.Command("cpio", "-o", "-H", "newc") // spell-checker:disable-line
	cmd.Stdin = &filesBuffer
	cmd.Stdout = cpioFileWriter
	cmd.Stderr = &stderrBuf
	cmd.Dir = packageFilesDir

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to run \n%s\n\nStderr: %s", cmd.String(), stderrBuf.String())
	}

	return nil
}
