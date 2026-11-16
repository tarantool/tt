package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/binary"
)

// printLog prints logfile to stdout.
func printLog(logName string) error {
	logs, err := os.ReadFile(logName)
	if err != nil {
		return fmt.Errorf("failed to read log: %w", err)
	}

	_, _ = os.Stdout.Write(logs)

	return nil
}

// installationActivation changes active links after all installation files are prepared.
type installationActivation func() error

// Install prepares a program and its environment, then activates it as the final step.
func Install(installCtx InstallCtx) error {
	if installCtx.LocalRepo != "" {
		localRepo, err := filepath.Abs(installCtx.LocalRepo)
		if err != nil {
			return fmt.Errorf("failed to resolve local repository path: %w", err)
		}

		installCtx.LocalRepo = localRepo
	}

	noteProjectScope(installCtx.Scope)

	// SIGINT and SIGTERM stop the build and remove the temporary files:
	// the deferred cleanups below do not run when a signal ends tt.
	stop := binary.WatchInterrupts(installCtx.KeepTemp)
	defer stop()

	var (
		activate installationActivation
		err      error
	)

	switch {
	case installCtx.Program == binary.ProgramTt:
		activate, err = prepareTtInstallation(installCtx)
	case installCtx.Program == binary.ProgramCe:
		activate, err = prepareTarantoolInstallation(installCtx)
	case installCtx.Program.BundleSource() != binary.ProgramUnknown:
		activate, err = prepareBundleProgram(installCtx)
	default:
		return fmt.Errorf("%w%s", binary.ErrUnknownApplication, installCtx.Program)
	}

	if err != nil {
		return err
	}

	return finishInstallation(installCtx, activate)
}

// noteProjectScope says where a project-scope installation outside every
// project goes, when it is the first: the working directory gets .tarantool
// and .env, which is easy to miss in $HOME.
func noteProjectScope(installScope binary.Scope) {
	if installScope != binary.ScopeProject {
		return
	}

	directory, found, err := binary.FindProjectDir()
	if err != nil || found || binary.IsDir(filepath.Join(directory, ".tarantool")) {
		return
	}

	log.Infof("Not inside a project: the project scope is %s, in the current directory "+
		"(see tt binary --help)", filepath.Join(directory, ".tarantool"))
}

// finishInstallation completes every filesystem preparation before changing active links.
func finishInstallation(installCtx InstallCtx, activate installationActivation) error {
	artifactRoot, err := installCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	directories := []string{binary.BinDirName}
	if installCtx.Program.IsTarantool() || installCtx.Program == binary.ProgramSDK {
		directories = append(directories, binary.IncDirName)
	}

	for _, directory := range directories {
		err = binary.CreateDirectory(filepath.Join(artifactRoot, directory), defaultDirPermissions)
		if err != nil {
			return err
		}
	}

	err = prepareProjectEnvironment(installCtx.Scope)
	if err != nil {
		return err
	}

	err = activate()
	if err != nil {
		return err
	}

	if installCtx.Scope == binary.ScopeProject {
		_, _ = fmt.Fprintf(os.Stdout, "\nTo activate the installed binaries, run:\n\n  %s\n\n",
			color.GreenString(projectActivateCommand()))
	} else {
		_, _ = fmt.Fprintf(os.Stdout,
			"\nTo use the installed binaries, add this directory to your PATH:\n\n  %s\n\n",
			color.GreenString(filepath.Join(artifactRoot, binary.BinDirName)))
	}

	return nil
}

// prepareInstallDirectory owns downloaded and built artifacts until they have been staged.
func prepareInstallDirectory(installCtx InstallCtx) (string, func(), error) {
	directory, err := os.MkdirTemp("", installCtx.Program.String()+"_install")
	if err != nil {
		return "", nil, fmt.Errorf("failed to create a temporary directory: %w", err)
	}

	forget := binary.RemoveOnInterrupt(directory)
	cleanup := func() {
		if installCtx.KeepTemp {
			log.Infof("Artifacts can be found at: %s", directory)
		} else {
			_ = os.RemoveAll(directory)
		}

		forget()
	}

	err = os.Chmod(directory, defaultDirPermissions)
	if err != nil {
		cleanup()

		return "", nil, fmt.Errorf("failed to set temporary directory permissions: %w", err)
	}

	return directory, cleanup, nil
}
