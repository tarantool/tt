package pack

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/otiai10/copy"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/docker"
	"github.com/tarantool/tt/cli/templates"
	"github.com/tarantool/tt/cli/version"
)

//go:embed templates/Dockerfile.pack.build
var buildDockerfile []byte

// getTarantoolVersionForInstall generates tarantool version string to install in docker image.
func getVersionStringForInstall(tntVersion version.Version) string {
	versionStr := fmt.Sprintf("%d.%d.%d", tntVersion.Major, tntVersion.Minor, tntVersion.Patch)
	if tntVersion.Release.Type != version.TypeRelease {
		versionStr = fmt.Sprintf("%s-%s", versionStr, tntVersion.Release)
	}
	return versionStr
}

// getTarantoolVersionForInstall returns tarantool version to use for install in docker.
func getTarantoolVersionForInstall(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx) (
	tntVersion version.Version, err error) {
	if packCtx.TarantoolVersion != "" {
		tntVersion, err = version.Parse(packCtx.TarantoolVersion)
		if err != nil {
			return
		}
	} else {
		if tntVersion, err = cmdCtx.Cli.TarantoolCli.GetVersion(); err != nil {
			return
		}
	}
	return
}

// PackInDocker runs tt pack in docker container.
func PackInDocker(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	opts config.CliOpts, cmdArgs []string) error {
	tmpDir, err := os.MkdirTemp("", "docker_pack_ctx")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	envDir := filepath.Dir(cmdCtx.Cli.ConfigPath)

	tntVersion, err := getTarantoolVersionForInstall(cmdCtx, packCtx)
	if err != nil {
		return fmt.Errorf("failed to get tarantool version: %w", err)
	}
	tntVersionStr := getVersionStringForInstall(tntVersion)
	log.Infof("Using tarantool version %s for packing", tntVersionStr)

	goTextEngine := templates.NewDefaultEngine()
	dockerfileText, err := goTextEngine.RenderText(string(buildDockerfile),
		map[string]string{
			"tnt_version": tntVersionStr,
			"env_dir":     filepath.Base(envDir),
		})
	if err != nil {
		return err
	}

	// Write docker file (rw-rw-r-- permissions).
	if err = os.WriteFile(filepath.Join(tmpDir, "Dockerfile"),
		[]byte(dockerfileText),
		0664); err != nil {
		return err
	}

	// Remove --use-docker and --tarantool-version from args.
	for i := 0; i < len(cmdArgs); {
		arg := cmdArgs[i]
		if arg == "--use-docker" {
			cmdArgs = append(cmdArgs[:i], cmdArgs[i+1:]...)
			continue
		}
		if arg == "--tarantool-version" {
			cmdArgs = append(cmdArgs[:i], cmdArgs[i+2:]...)
			continue
		}
		i++
	}

	// Generate pack command line for tt in container.
	ttPackCommandLine := append([]string{"tt"}, cmdArgs[1:]...)

	// If bin_dir is not empty, we need to pack binaries built in container.
	relEnvBinPath := configure.BinPath
	ttPackCommandLine = append([]string{"/bin/bash", "-c",
		fmt.Sprintf("cp $(which tarantool) %s && cp $(which tt) %s && %s",
			relEnvBinPath, relEnvBinPath, strings.Join(ttPackCommandLine, " "))})

	// Get a pack context for preparing a bundle without binaries.
	// All binary files will be taken from the docker image.
	dockerPackCtx := *packCtx
	dockerPackCtx.WithoutBinaries = true
	dockerPackCtx.WithBinaries = false

	// Create a temporary directory with environment files for mapping it into container.
	// That is needed to avoid files mutation and binaries replacing in source directory.
	tempEnvDir, err := prepareBundle(cmdCtx, &dockerPackCtx, &opts, false)
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempEnvDir)

	dockerRunOptions := docker.RunOptions{
		BuildCtxDir: tmpDir,
		ImageTag:    "ubuntu:tt_pack",
		Command:     ttPackCommandLine,
		Binds: []string{
			fmt.Sprintf("%s:%s", tempEnvDir, filepath.Join("/", "usr", "src",
				filepath.Base(envDir))),
		},
		Verbose: true,
	}
	if err = docker.RunContainer(dockerRunOptions, os.Stdout); err != nil {
		return err
	}

	skipRegularFilesFunc := func(path string) (bool, error) {
		switch filepath.Ext(path) {
		case ".deb", ".rpm", ".gz":
			return false, nil
		default:
			return true, nil
		}
	}

	curDir, err := os.Getwd()
	if err != nil {
		return err
	}

	err = copy.Copy(tempEnvDir, curDir, copy.Options{Skip: skipRegularFilesFunc})
	if err != nil {
		return err
	}

	return nil
}
