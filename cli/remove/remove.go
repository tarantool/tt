package remove

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/apex/log"
)

func Remove(programm string, dst string) error {
	if dst == "" {
		dst, _ = os.Getwd()
		dst += "/bin"
	}
	path := dst + "/" + programm
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return fmt.Errorf("There is no %s installed.", programm)
	}
	log.Warnf("%s found, removing...", programm)
	var linkPath string
	var err error
	if strings.Contains("tt", programm) {
		linkPath, err = filepath.Abs(dst + "/tt")
		if err != nil {
			return err
		}
	} else if strings.Contains("tarantool", programm) {
		linkPath, err = filepath.Abs(dst + "/tarantool")
	} else {
		return fmt.Errorf("Unknown programm: %s", programm)
	}
	reader, writer, _ := os.Pipe()
	cmd := exec.Command("ls", "-l", linkPath)
	cmd.Stdout = writer
	cmd.Start()
	err = cmd.Wait()
	writer.Close()
	var buf bytes.Buffer
	io.Copy(&buf, reader)
	if strings.Index(buf.String(), programm) == -1 {
		err = os.Remove(path)
	} else {
		err = os.Remove(linkPath)

		err = os.Remove(path)
	}
	log.Warnf("%s was removed!", programm)
	return err
}
