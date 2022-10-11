package search

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

const (
	GitRepoTarantool = "https://github.com/tarantool/tarantool.git"
	GitRepoTT        = "https://github.com/tarantool/tt.git"
)

const (
	// VersionCliSeparator is used in commands to specify version. E.g: program=version.
	VersionCliSeparator = "="
	// VersionFsSeparator is used in file names to specify version. E.g: program_version.
	VersionFsSeparator = "_"
)

// isMasked function checks that the given version of tarantool is masked.
func isMasked(version version.Version) bool {
	// Mask all versions below 1.10: deprecated.
	if version.Major == 1 && version.Minor < 10 {
		return true
	}

	// Mask all versions below 1.10.11: static build is not supported.
	if version.Major == 1 && version.Minor == 10 && version.Patch < 11 {
		return true
	}

	// Mask all versions below 2.7: static build is not supported.
	if version.Major == 2 && version.Minor < 7 {
		return true
	}

	// Mask 2.10.1 version: https://github.com/orgs/tarantool/discussions/7646.
	if version.Major == 2 && version.Minor == 10 && version.Patch == 1 {
		return true
	}

	// Mask all 2.X.0 below 2.10.0: technical tags.
	if version.Major == 2 && version.Minor < 10 && version.Patch == 0 {
		return true
	}

	return false
}

// GetVersionsFromGitRemote returns sorted versions list from specified remote git repo.
func GetVersionsFromGitRemote(repo string) ([]version.Version, error) {
	versions := []version.Version{}

	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("'git' is required for 'tt search' to work")
	}

	output, err := exec.Command("git", "ls-remote", "--tags", "--refs", repo).Output()
	if err != nil {
		return nil, fmt.Errorf("Failed to get versions from %s: %s", repo, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	// No tags found.
	if len(lines) == 1 && lines[0] == "" {
		return versions, nil
	}

	for _, line := range lines {
		slashIdx := strings.LastIndex(line, "/")
		if slashIdx == -1 {
			return nil, fmt.Errorf("Unexpected Data from %s", repo)
		} else {
			slashIdx += 1
		}
		ver := line[slashIdx:]
		version, err := version.GetVersionDetails(ver)
		if err != nil {
			continue
		}
		if isMasked(version) && repo == GitRepoTarantool {
			continue
		}
		versions = append(versions, version)
	}

	version.SortVersions(versions)

	return versions, nil
}

// GetVersionsFromGitLocal returns sorted versions list from specified local git repo.
func GetVersionsFromGitLocal(repo string) ([]version.Version, error) {
	versions := []version.Version{}

	if _, err := exec.LookPath("git"); err != nil {
		return nil, fmt.Errorf("'git' is required for 'tt search' to work")
	}

	output, err := exec.Command("git", "-C", repo, "tag").Output()
	if err != nil {
		return nil, fmt.Errorf("Failed to get versions from %s: %s", repo, err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")

	// No tags found.
	if len(lines) == 1 && lines[0] == "" {
		return versions, nil
	}

	for _, line := range lines {
		version, err := version.GetVersionDetails(line)
		if err != nil {
			continue
		}
		if isMasked(version) && strings.Contains(repo, "tarantool") {
			continue
		}
		versions = append(versions, version)
	}

	version.SortVersions(versions)

	return versions, nil
}

// printVersion prints the version and labels:
// * if the package is installed: [installed]
// * if the package is installed and in use: [active]
func printVersion(bindir string, program string, version string) {
	if _, err := os.Stat(filepath.Join(bindir, program+VersionFsSeparator+version)); err == nil {
		target := ""
		if program == "tarantool-ee" {
			target, _ = util.ResolveSymlink(filepath.Join(bindir, "tarantool"))
		} else {
			target, _ = util.ResolveSymlink(filepath.Join(bindir, program))
		}

		if path.Base(target) == program+VersionFsSeparator+version {
			fmt.Printf("%s [active]\n", version)
		} else {
			fmt.Printf("%s [installed]\n", version)
		}
	} else {
		fmt.Println(version)
	}
}

// SearchVersions outputs available versions of program.
func SearchVersions(cmdCtx *cmdcontext.CmdCtx, program string) error {
	var repo string
	versions := []version.Version{}

	if program == "tarantool" {
		repo = GitRepoTarantool
	} else if program == "tt" {
		repo = GitRepoTT
	} else if program == "tarantool-ee" {
		// Do nothing. Needs for bypass arguments check.
	} else {
		return fmt.Errorf("Search supports only tarantool/tarantool-ee/tt")
	}

	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}

	log.Infof("Available versions of " + program + ":")
	if program == "tarantool-ee" {
		versions, err = install_ee.FetchVersions(cliOpts)
		if err != nil {
			log.Fatalf(err.Error())
		}
		for _, version := range versions {
			printVersion(cliOpts.App.BinDir, program, version.Str)
		}
		return nil
	}

	versions, err = GetVersionsFromGitRemote(repo)
	if err != nil {
		log.Fatalf(err.Error())
	}

	for _, version := range versions {
		printVersion(cliOpts.App.BinDir, program, version.Str)
	}

	printVersion(cliOpts.App.BinDir, program, "master")

	return err
}

// RunCommandAndGetOutputInDir returns output of command.
func RunCommandAndGetOutputInDir(program string, dir string, args ...string) (string, error) {
	cmd := exec.Command(program, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(string(out)), nil
}

// SearchVersionsLocal outputs available versions of program from distfiles directory.
func SearchVersionsLocal(cmdCtx *cmdcontext.CmdCtx, program string) error {
	var err error
	cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
	if err != nil {
		return err
	}
	if cliOpts.Repo == nil {
		cliOpts.Repo = &config.RepoOpts{Install: "", Rocks: ""}
	}
	localDir := cliOpts.Repo.Install
	if localDir == "" {
		configDir := filepath.Dir(cmdCtx.Cli.ConfigPath)
		localDir = filepath.Join(configDir, "distfiles")
	}

	localFiles, err := os.ReadDir(localDir)
	if err != nil {
		return err
	}

	if program == "tarantool" {
		if _, err = os.Stat(localDir + "/tarantool"); !os.IsNotExist(err) {
			log.Infof("Available versions of " + program + ":")
			versions, err := GetVersionsFromGitLocal(localDir + "/tarantool")
			if err != nil {
				log.Fatalf(err.Error())
			}

			for _, version := range versions {
				printVersion(cliOpts.App.BinDir, program, version.Str)
			}
			printVersion(cliOpts.App.BinDir, program, "master")
		}
	} else if program == "tt" {
		if _, err = os.Stat(localDir + "/tt"); !os.IsNotExist(err) {
			log.Infof("Available versions of " + program + ":")
			versions, err := GetVersionsFromGitLocal(localDir + "/tt")
			if err != nil {
				log.Fatalf(err.Error())
			}

			for _, version := range versions {
				printVersion(cliOpts.App.BinDir, program, version.Str)
			}
			printVersion(cliOpts.App.BinDir, program, "master")
		}
	} else if program == "tarantool-ee" {
		files := []string{}
		for _, v := range localFiles {
			if strings.Contains(v.Name(), "tarantool-enterprise-bundle") && !v.IsDir() {
				files = append(files, v.Name())
			}
		}

		log.Infof("Available versions of " + program + ":")
		versions, err := install_ee.FetchVersionsLocal(files)
		if err != nil {
			log.Fatalf(err.Error())
		}

		for _, version := range versions {
			printVersion(cliOpts.App.BinDir, program, version.Str)
		}
	} else {
		return fmt.Errorf("Search supports only tarantool/tarantool-ee/tt")
	}

	return err
}
