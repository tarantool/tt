package cmd

import (
	"fmt"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/sdk/log"

	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/runtimepin"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/mirror"
)

// binaryRegistries is --registry of tt binary install: the rock servers whose
// mirrors of programs, binaries/ of a directory tt package download made, are
// asked before the sources of this build of tt.
var binaryRegistries []string

// binaryRegistryUsage is the help of --registry of tt binary install.
const binaryRegistryUsage = "Mirror tt package download made to install from first, " +
	"offline; repeatable, replaces " + manifestrocks.EnvRegistries

// addBinaryRegistryFlag declares --registry on the persistent flags of cmd.
func addBinaryRegistryFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().StringArrayVar(&binaryRegistries, "registry", nil,
		binaryRegistryUsage)
}

// binaryMirrors returns the mirrors of programs among the rock servers a
// command takes, as package commands settle them: --registry, else
// TT_REGISTRIES, else [platform].registries of the project in projectDir,
// when there is one. A server that is a directory with an index of binaries
// (mirror.IndexFile) is a mirror; any other is none.
func binaryMirrors(flag []string, projectDir string) ([]*mirror.Source, error) {
	sources, err := registrySources(flag)
	if err != nil {
		return nil, err
	}

	if projectDir != "" {
		sources.ProjectDir = projectDir

		sources.Manifest, err = manifestRegistries(projectDir)
		if err != nil {
			return nil, err
		}
	}

	if len(sources.Flag) == 0 && len(sources.Env) == 0 && len(sources.Manifest) == 0 {
		return nil, nil
	}

	registries, err := manifestrocks.EffectiveRegistries(sources)
	if err != nil {
		return nil, err
	}

	var mirrors []*mirror.Source

	for _, registry := range registries {
		// EffectiveRegistries makes the path of a directory absolute and
		// leaves a URL as it is.
		if !filepath.IsAbs(registry.URL) {
			continue
		}

		found, ok, err := mirror.Open(registry.URL)
		if err != nil {
			return nil, err
		}

		if ok {
			mirrors = append(mirrors, found)
		}
	}

	return mirrors, nil
}

// useBinaryMirrors makes the mirrors of programs of flag and of the project
// in projectDir (binaryMirrors) the first sources of prebuilt archives of the
// process, for the products they have, and returns them.
func useBinaryMirrors(flag []string, projectDir string) ([]*mirror.Source, error) {
	mirrors, err := binaryMirrors(flag, projectDir)
	if err != nil || len(mirrors) == 0 {
		return mirrors, err
	}

	sources := make([]bundle.Source, 0, len(mirrors)+len(binary.BundleSources()))
	for _, found := range mirrors {
		sources = append(sources, found)
	}

	binary.SetBundleSources(append(sources, binary.BundleSources()...))

	return mirrors, nil
}

// installFromMirrors makes the mirrors of programs that have a build of
// program answering requested (mirror.Source.Matches) the first sources of
// prebuilt archives of the process, so that tt binary install takes the
// build from them, and leaves the sources alone otherwise: a version a mirror
// does not have is installed as without one.
//
// The rock servers of TT_REGISTRIES and of the manifest were nothing to tt
// binary install before it had mirrors, so a list of them it cannot use is a
// warning, not a failure; one of --registry, asked for, is an error.
func installFromMirrors(program binary.Program, requested, projectDir string) error {
	mirrors, err := binaryMirrors(binaryRegistries, projectDir)
	if err != nil {
		if len(binaryRegistries) > 0 {
			return err
		}

		log.Warnf("No mirror of programs is looked for: %s", err)

		return nil
	}

	var matching []*mirror.Source

	for _, found := range mirrors {
		if found.Matches(program, requested, runtime.GOOS, runtime.GOARCH) {
			matching = append(matching, found)
		}
	}

	if len(matching) == 0 {
		if len(mirrors) > 0 || len(binaryRegistries) > 0 {
			log.Infof("No mirror among the rock servers has %s %s for %s/%s", program,
				orLatest(requested), runtime.GOOS, runtime.GOARCH)
		}

		return nil
	}

	sources := make([]bundle.Source, 0, len(matching)+len(binary.BundleSources()))
	for _, found := range matching {
		sources = append(sources, found)
	}

	binary.SetBundleSources(append(sources, binary.BundleSources()...))

	log.Infof("Installing %s from %s", program, matching[0].Name())

	return nil
}

// orLatest names a requested version in a message: the latest for none.
func orLatest(requested string) string {
	if requested == "" {
		return "of any version"
	}

	return requested
}

// pinnedInstallArgs returns the arguments tt binary install of the command
// commandName takes when it is given none in the project scope of the
// project in projectDir: the version app.manifest.lock pins of the product
// the command installs for the project - tarantool or, for an [ee] project,
// tarantool-ee of tt binary install tarantool, tt or tt-ee, and tcm - as the
// command takes it, 3.9.0, or 3.4.1ee of an Enterprise one. None for a
// project, a command or a lock that pins nothing.
func pinnedInstallArgs(commandName, projectDir string) []string {
	if projectDir == "" {
		return nil
	}

	man, lock, err := readManifestAndLock(projectDir)
	if err != nil {
		return nil
	}

	component := map[string]runtimepin.Component{
		binary.ProgramCe.String():  runtimepin.ComponentTarantool,
		binary.ProgramTt.String():  runtimepin.ComponentTt,
		binary.ProgramTcm.String(): runtimepin.ComponentTcm,
	}[commandName]

	pin := runtimepin.Get(lock, component)
	if component == "" || pin == "" || !runtimepin.Wanted(man.Platform, component) {
		return nil
	}

	program := runtimepin.Program(man.Platform, component)

	log.Infof("Installing %s %s, the version %s pins", program, pin, lockFileName)

	return []string{binary.VersionArgument(program, pin)}
}

// projectForInstall returns the project tt binary install of scope works on,
// empty outside every project and for another scope than the project one.
func projectForInstall(installScope binary.Scope) string {
	if installScope != binary.ScopeProject {
		return ""
	}

	dir, found, err := binary.FindProjectDir()
	if err != nil || !found {
		return ""
	}

	return dir
}

// requestOf returns the product and the version tt binary install of the
// command commandName installs for args, as install.FillCtx reads them.
func requestOf(commandName string, args []string) (binary.VersionRequest, error) {
	requested := ""
	if len(args) == 1 {
		requested = args[0]
	}

	request, err := binary.ParseVersionRequest(commandName, requested)
	if err != nil {
		return binary.VersionRequest{}, fmt.Errorf("failed to create context: %w", err)
	}

	return request, nil
}
