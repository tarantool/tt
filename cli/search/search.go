package search

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/configure"
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/version"
)

// isDeprecated checks if the program version is lower than 1.10.0.
func isDeprecated(version string) bool {
	splitedVersion := strings.Split(version, ".")
	if len(splitedVersion) < 2 {
		return false
	}
	if splitedVersion[0] == "1" && len(splitedVersion[1]) < 2 {
		return true
	}
	return false
}

// SearchVersions outputs available versions of program.
func SearchVersions(cmdCtx *cmdcontext.CmdCtx, program string) error {
	var cmd *exec.Cmd

	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("'git' is needed for 'tt search' to work")
	}

	if program == "tarantool" {
		cmd = exec.Command("git", "-c", "versionsort.suffix=-", "ls-remote", "--tags", "--sort="+
			"v:refname", "https://github.com/tarantool/tarantool.git")
	} else if program == "tt" {
		cmd = exec.Command("git", "-c", "versionsort.suffix=-", "ls-remote", "--tags", "--sort="+
			"v:refname", "https://github.com/tarantool/tt.git")
	} else if program == "tarantool-ee" {
		// Do nothing. Needs for bypass arguments check.
	} else {
		return fmt.Errorf("Search supports only tarantool/tt")
	}

	log.Warn("Available versions of " + program + ":")
	if program == "tarantool-ee" {
		cliOpts, err := configure.GetCliOpts(cmdCtx.Cli.ConfigPath)
		if err != nil {
			return err
		}

		versions, err := install_ee.FetchVersions(cliOpts)
		if err != nil {
			log.Fatalf(err.Error())
		}
		for _, ver := range versions {
			fmt.Printf("%s\n", ver.Str)
		}
		return nil
	}

	readPipe, writePipe, _ := os.Pipe()
	cmd.Stdout = writePipe
	cmd.Stderr = os.Stderr
	cmd.Start()
	err := cmd.Wait()
	writePipe.Close()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	io.Copy(&buf, readPipe)
	versions := buf.String()
	versionsArray := strings.Split(versions, "\n")
	for i := 0; i < len(versionsArray); i++ {
		trimPos := strings.LastIndex(versionsArray[i], "/") + 1
		versionsArray[i] = versionsArray[i][trimPos:]
		if strings.Contains(versionsArray[i], "^{}") ||
			(isDeprecated(versionsArray[i]) && program == "tarantool") {
			continue
		}
		os.Stdout.Write([]byte(versionsArray[i]))
		os.Stdout.Write([]byte("\n"))
	}
	os.Stdout.Write([]byte("master\n"))
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
			versions, err := RunCommandAndGetOutputInDir("git",
				localDir+"/tarantool",
				"-c", "versionsort.suffix=-",
				"tag", "--sort="+"v:refname")
			if err != nil {
				return err
			}
			log.Infof("Available versions of " + program + ":")
			versionsArray := strings.Split(versions, "\n")
			for _, version := range versionsArray {
				if isDeprecated(version) {
					continue
				}
				fmt.Println(version)
			}
			fmt.Println("master")
		}
	} else if program == "tt" {
		if _, err = os.Stat(localDir + "/tt"); !os.IsNotExist(err) {
			versions, err := RunCommandAndGetOutputInDir("git",
				localDir+"/tt", "-c",
				"versionsort.suffix=-",
				"tag", "--sort="+"v:refname")
			if err != nil {
				return err
			}
			log.Infof("Available versions of " + program + ":")
			fmt.Println(versions)
			fmt.Println("master")
		}
	} else if program == "tarantool-ee" {
		for _, v := range localFiles {
			var versions []version.Version
			if strings.Contains(v.Name(), "tarantool-enterprise-bundle") && !v.IsDir() {
				name := strings.TrimPrefix(v.Name(), "tarantool-enterprise-bundle-")
				name = strings.TrimSuffix(name, ".tar.gz")
				versionLocal, err := version.GetVersionDetails(name)
				if err != nil {
					return err
				}
				versionLocal.Str = name
				versions = append(versions, versionLocal)

			}
			log.Infof("Available versions of " + program + ":")
			version.SortVersions(versions)
			for _, version := range versions {
				fmt.Println("   " + version.Str)
			}
		}
	} else {
		return fmt.Errorf("Search supports only tarantool/tt")
	}

	return err
}
