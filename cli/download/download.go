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
	// Version of SDK to download.
	Version string
	// Path where the sdk will be saved.
	DirectoryPrefix string
	// Download development build.
	DevBuild bool
}

// searchSDKVersionToDownload wrapper to call search package.
func searchSDKVersionToDownload(downloadCtx DownloadCtx, cliOpts *config.CliOpts) (
	search.BundleInfo, error,
) {
	log.Info("Search for the requested version...")
	searchCtx := search.NewSearchCtx(search.NewPlatformInformer(), search.NewTntIoDoer())
	searchCtx.Program = search.ProgramEe
	searchCtx.Filter = search.SearchAll
	searchCtx.Package = "enterprise"
	searchCtx.DevBuilds = downloadCtx.DevBuild

	bundles, err := search.FetchBundlesInfo(&searchCtx, cliOpts)
	if err != nil {
		return search.BundleInfo{}, fmt.Errorf("cannot get SDK bundles list: %s", err)
	}
	return search.SelectVersion(bundles, downloadCtx.Version)
}

// DownloadSDK Downloads and saves the SDK.
func DownloadSDK(cmdCtx *cmdcontext.CmdCtx, downloadCtx DownloadCtx,
	cliOpts *config.CliOpts,
) error {
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

	ver, err := searchSDKVersionToDownload(downloadCtx, cliOpts)
	if err != nil {
		return fmt.Errorf("no version for download: %s", err)
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
	searchCtx := search.NewSearchCtx(
		search.NewPlatformInformer(),
		install_ee.NewTntIoDownloader(ver.Token),
	)
	searchCtx.Program = search.ProgramEe
	searchCtx.DevBuilds = downloadCtx.DevBuild
	searchCtx.ReleaseVersion = ver.Release

	bundleSource, err := search.TntIoMakePkgURI(&searchCtx, bundleName)
	if err != nil {
		return fmt.Errorf("failed to make URI for downloading: %s", err)
	}

	err = install_ee.DownloadBundle(&searchCtx,
		bundleName, bundleSource, downloadCtx.DirectoryPrefix)
	if err != nil {
		return fmt.Errorf("download error: %s", err)
	}

	log.Infof("Downloaded to: %q", bundlePath)

	return err
}
