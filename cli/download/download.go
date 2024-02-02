package download

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/install_ee"
	"github.com/tarantool/tt/cli/search"
	"github.com/tarantool/tt/cli/util"
	"golang.org/x/sys/unix"
)

type DownloadCtx struct {
	// SDK version to download.
	version string
	// Path where the sdk will be saved.
	DirectoryPrefix string
	// Download development build.
	DevBuild bool
}

// DownloadSDK Downloads and saves the SDK.
func DownloadSDK(cmdCtx *cmdcontext.CmdCtx, downloadCtx DownloadCtx,
	cliOpts *config.CliOpts) error {
	var err error

	if len(downloadCtx.DirectoryPrefix) == 0 {
		downloadCtx.DirectoryPrefix, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("can't get current dir: %s", err.Error())
		}
	}

	if err = unix.Access(downloadCtx.DirectoryPrefix, unix.W_OK); err != nil {
		return fmt.Errorf("bad directory prefix: %s", err)
	}

	log.Info("Search for the requested version...")
	ver, err := search.GetTarantoolBundleInfo(cliOpts, false,
		downloadCtx.DevBuild, nil, downloadCtx.version)
	if err != nil {
		return fmt.Errorf("cannot get SDK bundle info: %s", err)
	}

	bundleName := ver.Version.Tarball
	bundlePath := filepath.Join(downloadCtx.DirectoryPrefix, bundleName)
	if _, err := os.Stat(bundlePath); err == nil {
		confirmed, err := util.AskConfirm(os.Stdin, fmt.Sprintf("Confirm overwrite %s",
			bundlePath))
		if err != nil {
			return err
		}
		if !confirmed {
			log.Info("Download is cancelled.")
			return nil
		}
	}

	log.Infof("Downloading %s...", bundleName)
	bundleSource, err := search.TntIoMakePkgURI(ver.Package, ver.Release,
		bundleName, downloadCtx.DevBuild)
	if err != nil {
		return fmt.Errorf("failed to make URI for downloading: %s", err)
	}

	err = install_ee.GetTarantoolEE(cliOpts, bundleName, bundleSource,
		ver.Token, downloadCtx.DirectoryPrefix)
	if err != nil {
		return fmt.Errorf("download error: %s", err)
	}

	log.Infof("Downloaded to: %q", bundlePath)

	return err
}

func FillCtx(cmdCtx *cmdcontext.CmdCtx, downloadCtx *DownloadCtx, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("to download Tarantool SDK, you need to specify the version")
	} else if len(args) == 1 {
		downloadCtx.version = args[0]
	} else if len(args) > 1 {
		return fmt.Errorf("invalid number of parameters")
	}

	return nil
}
