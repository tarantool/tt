package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
)

// archivePacker is a structure that implements Packer interface
// with specific archive packing behavior.
type archivePacker struct {
}

// Run of ArchivePacker packs the bundle into tarball.
func (packer *archivePacker) Run(cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx,
	opts *config.CliOpts) error {
	bundlePath, err := prepareBundle(cmdCtx, packCtx, opts, true)
	if err != nil {
		return err
	}
	defer func() {
		err := os.RemoveAll(bundlePath)
		if err != nil {
			log.Warnf("Failed to remove a temporary directory %s: %s",
				bundlePath, err.Error())
		}
	}()

	log.Debugf("The package structure is created in: %s", bundlePath)

	tgzSuffix, err := getTgzSuffix()
	if err != nil {
		return err
	}
	tarName, err := getPackageName(packCtx, opts, tgzSuffix, true)
	if err != nil {
		return err
	}

	if packCtx.CartridgeCompat {
		// Generate VERSION file.
		if err := generateVersionFile(bundlePath, cmdCtx, packCtx); err != nil {
			log.Warnf("Failed to generate VERSION file: %s", err)
		}

		// Generate VERSION.lua file.
		if err := generateVersionLuaFile(bundlePath, packCtx); err != nil {
			log.Warnf("Failed to generate VERSION.lua file: %s", err)
		}
	}

	log.Infof("Creating tarball.")

	currentDir, err := os.Getwd()
	if err != nil {
		return err
	}
	tarName = filepath.Join(currentDir, tarName)

	err = WriteTgzArchive(bundlePath, tarName)
	if err != nil {
		if err := os.Remove(tarName); err != nil {
			log.Warnf("Failed to remove a tarball file %s: %s", tarName, err)
		}
		return err
	}
	log.Infof("Bundle is packed successfully to %s.", tarName)
	return nil
}

// generateVersionLuaFile generates VERSION.lua file (for cartridge-cli compatibility).
func generateVersionLuaFile(bundlePath string, packCtx *PackCtx) error {
	log.Infof("Generate %s file", versionLuaFileName)

	versionLuaFilePath := filepath.Join(bundlePath, packCtx.Name, versionLuaFileName)
	// Check if the file already exists.
	if _, err := os.Stat(versionLuaFilePath); err == nil {
		log.Warnf("File %s will be overwritten", versionLuaFileName)
	}

	err := os.WriteFile(versionLuaFilePath,
		[]byte(fmt.Sprintf("return '%s'", packCtx.Version)), 0644)
	if err != nil {
		return fmt.Errorf("failed to write VERSION.lua file %s: %s", versionLuaFilePath, err)
	}

	return nil
}

// generateVersionFile generates VERSION file (for cartridge-cli compatibility).
func generateVersionFile(bundlePath string, cmdCtx *cmdcontext.CmdCtx, packCtx *PackCtx) error {
	log.Infof("Generate %s file", versionFileName)

	var versionFileLines []string

	// Application version.
	appVersionLine := fmt.Sprintf("%s=%s", packCtx.Name, packCtx.Version)
	versionFileLines = append(versionFileLines, appVersionLine)

	// Tarantool version.
	tntVersion, err := cmdCtx.Cli.TarantoolCli.GetVersion()
	if err != nil {
		return err
	}
	tarantoolVersionLine := fmt.Sprintf("TARANTOOL=%s", tntVersion.Str)
	versionFileLines = append(versionFileLines, tarantoolVersionLine)

	// Rocks versions.
	rocksVersionsMap, err := LuaGetRocksVersions(filepath.Join(bundlePath, packCtx.Name))

	if err != nil {
		log.Warnf("Can't process rocks manifest file. Dependency information can't be "+
			"shipped to the resulting package: %s", err)
	} else {
		for rockName, versions := range rocksVersionsMap {
			if rockName != packCtx.Name {
				rockLine := fmt.Sprintf("%s=%s", rockName, versions[len(versions)-1])
				versionFileLines = append(versionFileLines, rockLine)
			}

			if len(versions) > 1 {
				log.Warnf("Found multiple versions of %s in rocks manifest: %s",
					rockName, strings.Join(versions, ", "))
			}
		}
	}

	versionFilePath := filepath.Join(bundlePath, packCtx.Name, versionFileName)
	err = os.WriteFile(versionFilePath, []byte(strings.Join(versionFileLines, "\n")+"\n"), 0644)
	if err != nil {
		return fmt.Errorf("failed to write VERSION file %s: %s", versionFilePath, err)
	}

	return nil
}

// getTgzSuffix returns suffix for a tarball.
func getTgzSuffix() (string, error) {
	arch, err := util.GetArch()
	if err != nil {
		return "", err
	}
	tgzSuffix := strings.Join([]string{"", arch, "tar", "gz"}, ".")
	return tgzSuffix, nil
}
