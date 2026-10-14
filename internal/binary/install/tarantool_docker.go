package install

import (
	_ "embed"
	"fmt"
	"os"
	"os/user"
	"path/filepath"

	"github.com/otiai10/copy"
	"github.com/tarantool/tt/v3/cli/docker"
	"github.com/tarantool/tt/v3/cli/templates"
	"github.com/tarantool/tt/v3/internal/binary"
)

const (
	dockerfileMode     = 0o664
	dockerLocalRepoDir = "/tt_distfiles"
)

//go:embed templates/Dockerfile.tnt.build
var tarantoolBuildDockerfile []byte

func installTarantoolInDocker(tntVersion, artifactRoot string, installCtx InstallCtx) error {
	tmpDir, err := os.MkdirTemp("", "docker_build_ctx")
	if err != nil {
		return fmt.Errorf("failed to create a temporary directory: %w", err)
	}

	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %w", err)
	}

	goTextEngine := templates.NewDefaultEngine()

	dockerfileText, err := goTextEngine.RenderText(string(tarantoolBuildDockerfile),
		map[string]string{"uid": currentUser.Uid})
	if err != nil {
		return err
	}

	// Write docker file (rw-rw-r-- permissions).
	err = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"), []byte(dockerfileText),
		dockerfileMode)
	if err != nil {
		return fmt.Errorf("failed to write Dockerfile: %w", err)
	}

	// Copy tt executable.
	currentExecutable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("failed to get tt executable path: %w", err)
	}

	err = copy.Copy(currentExecutable, filepath.Join(tmpDir, "tt"))
	if err != nil {
		return fmt.Errorf("failed to copy tt executable: %w", err)
	}

	dockerRunOptions := tarantoolDockerRunOptions(tmpDir, tntVersion, artifactRoot,
		installCtx)

	return docker.RunContainer(dockerRunOptions, os.Stdout)
}

// tarantoolDockerRunOptions mounts the selected scope's versions without sharing active links.
func tarantoolDockerRunOptions(tmpDir, tntVersion, artifactRoot string,
	installCtx InstallCtx,
) docker.RunOptions {
	// Generate install command line for tt in container.
	// The container builds in its project scope. Only the versions directory is shared;
	// the host selects the active version using paths in the requested installation scope.
	tntInstallCommandLine := []string{"./tt", "binary", "--scope", binary.ScopeProject.String()}
	if installCtx.Verbose {
		tntInstallCommandLine = append(tntInstallCommandLine, "-V")
	}

	// The host already decided to build this version, including checking for master updates.
	tntInstallCommandLine = append(tntInstallCommandLine, "install", "-f",
		binary.ProgramCe.Exec(), tntVersion, "--reinstall")

	binds := []string{
		fmt.Sprintf("%s:%s", filepath.Join(artifactRoot, binary.VersionsDirName),
			filepath.Join("/work", ".tarantool", binary.VersionsDirName)),
	}

	if installCtx.LocalRepo != "" {
		tntInstallCommandLine = append(tntInstallCommandLine, "--local-repo", dockerLocalRepoDir)
		binds = append(binds, fmt.Sprintf("%s:%s", installCtx.LocalRepo, dockerLocalRepoDir))
	}

	if installCtx.Dynamic {
		tntInstallCommandLine = append(tntInstallCommandLine, "--dynamic")
	}

	return docker.RunOptions{
		BuildCtxDir: tmpDir,
		ImageTag:    "ubuntu:tt_tarantool_build",
		Command:     tntInstallCommandLine,
		Binds:       binds,
		Verbose:     installCtx.Verbose,
	}
}
