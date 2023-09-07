package pack

import (
	_ "embed"
	"fmt"
	"io/ioutil"
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

// PackInDocker runs tt pack in docker container.
func PackInDocker(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	opts config.CliOpts, cmdArgs []string) error {
	tmpDir, err := ioutil.TempDir("", "docker_pack_ctx")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	envDir := filepath.Dir(cmdCtx.Cli.ConfigPath)

	goTextEngine := templates.NewDefaultEngine()

	tntVersion := cmdCtx.Cli.TarantoolVersion

	if packCtx.TarantoolVersion != "" {
		tntVerParsed, err := version.Parse(packCtx.TarantoolVersion)
		if err != nil {
			return err
		}
		tntVersion = fmt.Sprintf("%d.%d.%d",
			tntVerParsed.Major, tntVerParsed.Minor, tntVerParsed.Patch)
	}

	log.Infof("Using tarantool version %s for packing", tntVersion)

	dockerfileText, err := goTextEngine.RenderText(string(buildDockerfile),
		map[string]string{
			"tnt_version": tntVersion,
			"env_dir":     filepath.Base(envDir),
		})
	if err != nil {
		return err
	}

	// Write docker file (rw-rw-r-- permissions).
	if err = ioutil.WriteFile(filepath.Join(tmpDir, "Dockerfile"),
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
