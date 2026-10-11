package install

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"strings"

	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errUnknownOS = errors.New("unknown OS")
)

const (
	// osDarwin is the name of macOS as runtime.GOOS reports it.
	osDarwin = "darwin"
)

// DistroInfo is a struct containing info about linux distro.
type DistroInfo struct {
	Name         string
	Vendor       string
	Version      string
	Architecture string
}

var (
	// PrettyNameRe is a regexp for PrettyName in os-release file.
	PrettyNameRe = regexp.MustCompile(`^PRETTY_NAME=(.*)$`)
	// IDRe is a regexp for ID in os-release file.
	IDRe = regexp.MustCompile(`^ID=(.*)$`)
	// VersionIDRe is a regexp for VersionID in os-release file.
	VersionIDRe = regexp.MustCompile(`^VERSION_ID=(.*)$`)
)

// getDistroInfo collects info about linux distro.
func getDistroInfo() (DistroInfo, error) {
	var (
		distroInfo DistroInfo
		err        error
	)

	// Get architecture.
	distroInfo.Architecture, err = binary.GetArch()
	if err != nil {
		return distroInfo, err
	}

	// Get distribution info.
	releaseFile, err := os.Open("/etc/os-release")
	if err != nil {
		return distroInfo, fmt.Errorf("failed to read OS release info: %w", err)
	}

	defer func() {
		_ = releaseFile.Close()
	}()

	scanner := bufio.NewScanner(releaseFile)
	for scanner.Scan() {
		if m := PrettyNameRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Name = strings.Trim(m[1], `"`)
		} else if m := IDRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Vendor = strings.Trim(m[1], `"`)
		} else if m := VersionIDRe.FindStringSubmatch(scanner.Text()); m != nil {
			distroInfo.Version = strings.Trim(m[1], `"`)
		}
	}

	return distroInfo, nil
}

// detectOsName returns name of the OS.
func detectOsName() (string, error) {
	if runtime.GOOS == osDarwin {
		return osDarwin, nil
	}

	if runtime.GOOS == "windows" {
		return "windows", nil
	}

	if runtime.GOOS == "linux" {
		distroInfo, err := getDistroInfo()
		return distroInfo.Name, err
	}

	return "", errUnknownOS
}
