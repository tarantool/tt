package install

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	mageExec = "mage"
	gccExec  = "gcc"
)

type missingPackagesError string

func (err missingPackagesError) Error() string {
	return string(err)
}

// Package is a struct containing sys and install name of the package.
type Package struct {
	// sysName is a string containing system name of package.
	sysName string
	// installName is a string containing install name of package.
	installName string
}

// isProgramInstalled checks if program is installed.
func isProgramInstalled(program string) bool {
	_, err := exec.LookPath(program)

	return err == nil
}

// isPackageInstalledDebian checks if package is installed on Debian/Ubuntu.
func isPackageInstalledDebian(packageName string) bool {
	cmd := exec.CommandContext(context.Background(), "dpkg", "-L", packageName)

	_ = cmd.Start()

	if cmd.Wait() == nil {
		return true
	} else {
		return false
	}
}

// isPackageInstalled checks if package is installed.
func isPackageInstalled(packageName string) bool {
	osName, _ := detectOsName()
	if strings.Contains(osName, "Ubuntu") || strings.Contains(osName, "Debian") {
		return isPackageInstalledDebian(packageName)
	}

	if strings.Contains(osName, osDarwin) {
		packageList, _ := binary.RunCommandAndGetOutput("brew", "list")
		return strings.Contains(packageList, packageName)
	}

	if strings.Contains(osName, "CentOS") {
		packageList, _ := binary.RunCommandAndGetOutput("yum", "list", "--installed")
		return strings.Contains(packageList, packageName)
	}

	return false
}

// programDependenciesInstalled checks if dependencies are installed.
func programDependenciesInstalled(program binary.Program) error {
	programs := []Package{}
	packages := []string{}
	osName, _ := detectOsName()

	switch program {
	case binary.ProgramTt:
		programs = []Package{{mageExec, mageExec}, {gitExec, gitExec}}
	case binary.ProgramCe:
		switch {
		case osName == osDarwin:
			programs = []Package{
				{cmakeExec, cmakeExec},
				{gitExec, gitExec},
				{makeExec, makeExec},
				{"clang", "clang"},
				{"openssl", "openssl"},
			}
		case strings.Contains(osName, "Ubuntu") || strings.Contains(osName, "Debian"):
			programs = []Package{
				{cmakeExec, cmakeExec},
				{gitExec, gitExec},
				{makeExec, makeExec},
				{gccExec, " build-essential"},
			}
			packages = []string{"coreutils", "sed"} // spell-checker:disable-line
		case strings.Contains(osName, "CentOs"):
			programs = []Package{
				{cmakeExec, cmakeExec},
				{gitExec, gitExec},
				{makeExec, makeExec},
				{gccExec, gccExec},
				{"g++", "gcc-c++ "},
			}
			packages = []string{"libstdc++-static", "perl"}
		default:
			answer, err := binary.AskConfirm(os.Stdin,
				"Unknown OS, can't check if dependencies"+
					" are installed.\n"+
					"Proceed without checking?")
			if err != nil {
				return err
			}

			if !answer {
				return binary.ErrCmdAbort
			}

			return nil
		}
	case binary.ProgramUnknown, binary.ProgramEe, binary.ProgramTcm, binary.ProgramSDK:
		// These programs have no source-build dependency checks.
	}

	missingPack := []string{}
	// Programs that are installed from source.
	missingPackSrc := []string{}

	for _, program := range programs {
		if !isProgramInstalled(program.sysName) {
			// Mage is installed from source instead of package manager.
			if program.sysName == mageExec {
				missingPackSrc = append(missingPackSrc, program.installName)
			} else {
				missingPack = append(missingPack, program.installName)
			}
		}
	}

	for _, packageName := range packages {
		if !isPackageInstalled(packageName) {
			missingPack = append(missingPack, packageName)
		}
	}

	if len(missingPack) != 0 || len(missingPackSrc) != 0 {
		log.Error("The operation requires some dependencies.")

		var errMsg strings.Builder

		errMsg.WriteString("Missing packages: " + strings.Join(missingPack, " ") + " " +
			strings.Join(missingPackSrc, " ") + "\n")

		switch {
		case osName == osDarwin:
			errMsg.WriteString(
				"You can install them by running commands:\nbrew install " + strings.Join(
					missingPack,
					" ",
				) +
					strings.Join(
						missingPackSrc,
						" ",
					) + "\n",
			)
		case strings.Contains(osName, "CentOs"):
			errMsg.WriteString("You can install them by running command:\n")

			if len(missingPack) != 0 {
				errMsg.WriteString(" sudo yum install " + strings.Join(missingPack, " ") + "\n")
			}

			if len(missingPackSrc) != 0 {
				errMsg.WriteString("install from sources: " +
					strings.Join(missingPackSrc, " ") + "\n")
			}
		case strings.Contains(osName, "Ubuntu") || strings.Contains(osName, "Debian"):
			errMsg.WriteString("You can install them by running command:\n")

			if len(missingPack) != 0 {
				errMsg.WriteString(" sudo apt install " + strings.Join(missingPack, " ") + "\n")
			}

			if len(missingPackSrc) != 0 {
				errMsg.WriteString("install from sources: " +
					strings.Join(missingPackSrc, " ") + "\n")
			}
		}

		errMsg.WriteString("Usage: tt binary install -f " +
			"if you already have those packages installed")

		return missingPackagesError(errMsg.String())
	}

	return nil
}
