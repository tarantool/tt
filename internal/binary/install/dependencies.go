package install

import (
	"context"
	"os"
	"os/exec"
	"slices"
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

// isPackageInstalledWith reports whether the command that asks the package
// manager for a package succeeds.
func isPackageInstalledWith(name string, args ...string) bool {
	return exec.CommandContext(context.Background(), name, args...).Run() == nil
}

// isPackageInstalled checks if package is installed on a system of family.
func isPackageInstalled(family osFamily, packageName string) bool {
	switch family {
	case osFamilyDebian:
		return isPackageInstalledWith("dpkg", "-L", packageName)
	case osFamilyRedHat:
		return isPackageInstalledWith("rpm", "-q", packageName)
	case osFamilyDarwin:
		packageList, _ := binary.RunCommandAndGetOutput("brew", "list")

		return strings.Contains(packageList, packageName)
	case osFamilyUnknown:
	}

	return false
}

// redHatPackageManager is the package manager of a Red Hat system: dnf, or
// yum where there is no dnf, CentOS 7 say.
func redHatPackageManager() string {
	if isProgramInstalled("dnf") {
		return "dnf"
	}

	return "yum"
}

// programDependenciesInstalled checks if dependencies are installed.
func programDependenciesInstalled(program binary.Program) error {
	return dependenciesInstalledOn(program, detectOsFamily())
}

// dependenciesInstalledOn checks if the dependencies of a build of program
// from source are installed on a system of family.
func dependenciesInstalledOn(program binary.Program, family osFamily) error {
	programs := []Package{}
	packages := []string{}

	switch program {
	case binary.ProgramTt:
		programs = []Package{{mageExec, mageExec}, {gitExec, gitExec}}
	case binary.ProgramCe:
		switch family {
		case osFamilyDarwin:
			programs = []Package{
				{cmakeExec, cmakeExec},
				{gitExec, gitExec},
				{makeExec, makeExec},
				{"clang", "clang"},
				{"openssl", "openssl"},
			}
		case osFamilyDebian:
			programs = []Package{
				{cmakeExec, cmakeExec},
				{gitExec, gitExec},
				{makeExec, makeExec},
				{gccExec, "build-essential"},
			}
			packages = []string{"coreutils", "sed"} // spell-checker:disable-line
		case osFamilyRedHat:
			programs = []Package{
				{cmakeExec, cmakeExec},
				{gitExec, gitExec},
				{makeExec, makeExec},
				{gccExec, gccExec},
				{"g++", "gcc-c++"},
			}
			packages = []string{"libstdc++-static", "perl"}
		case osFamilyUnknown:
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
	case binary.ProgramUnknown, binary.ProgramEe, binary.ProgramTtEe,
		binary.ProgramTcm, binary.ProgramSDK:
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
		if !isPackageInstalled(family, packageName) {
			missingPack = append(missingPack, packageName)
		}
	}

	if len(missingPack) == 0 && len(missingPackSrc) == 0 {
		return nil
	}

	log.Error("The operation requires some dependencies.")

	return missingPackagesError(missingPackagesMessage(family, missingPack, missingPackSrc))
}

// missingPackagesMessage says what is missing on a system of family and the
// commands that install it: missing are the packages of the package manager,
// fromSource the programs installed from source.
func missingPackagesMessage(family osFamily, missing, fromSource []string) string {
	var message strings.Builder

	all := strings.Join(append(slices.Clone(missing), fromSource...), " ")

	message.WriteString("Missing packages: " + all + "\n")

	installer := ""

	switch family {
	case osFamilyDarwin:
		message.WriteString("You can install them by running commands:\nbrew install " +
			all + "\n")
	case osFamilyRedHat:
		installer = "sudo " + redHatPackageManager() + " install "
	case osFamilyDebian:
		installer = "sudo apt install "
	case osFamilyUnknown:
	}

	if installer != "" {
		message.WriteString("You can install them by running command:\n")

		if len(missing) != 0 {
			message.WriteString(" " + installer + strings.Join(missing, " ") + "\n")
		}

		if len(fromSource) != 0 {
			message.WriteString("install from sources: " + strings.Join(fromSource, " ") + "\n")
		}
	}

	message.WriteString("Use --force if you already have those packages installed")

	return message.String()
}
