package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/config"
	"golang.org/x/sys/unix"
)

var (
	errCurrentDirectoryUnavailable = errors.New("can't get current dir: ")
)

type DownloadCtx struct {
	// Version of SDK to download.
	Version string
	// Path where the sdk will be saved.
	DirectoryPrefix string
	// Download development build.
	DevBuild bool
}

// searchSDKVersionToDownload wrapper to search for SDK bundles.
func searchSDKVersionToDownload(downloadCtx DownloadCtx, cliOpts *config.CliOpts) (
	BundleInfo, error,
) {
	log.Info("Search for the requested version...")

	searchCtx := NewSearchCtx(NewPlatformInformer(), NewTntIoDoer())

	searchCtx.Program = ProgramEe
	searchCtx.Filter = SearchAll
	searchCtx.Package = "enterprise"
	searchCtx.DevBuilds = downloadCtx.DevBuild

	bundles, err := FetchBundlesInfo(&searchCtx, cliOpts)
	if err != nil {
		return BundleInfo{}, fmt.Errorf("cannot get SDK bundles list: %w", err)
	}

	return SelectVersion(bundles, downloadCtx.Version)
}

// DownloadSDK Downloads and saves the SDK.
func DownloadSDK(cmdCtx *cmdcontext.CmdCtx, downloadCtx DownloadCtx,
	cliOpts *config.CliOpts,
) error {
	var err error

	if len(downloadCtx.DirectoryPrefix) == 0 {
		downloadCtx.DirectoryPrefix, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("%w%s", errCurrentDirectoryUnavailable, err.Error())
		}
	}

	err = unix.Access(downloadCtx.DirectoryPrefix, unix.W_OK)
	if err != nil {
		return fmt.Errorf("bad directory prefix: %w", err)
	}

	ver, err := searchSDKVersionToDownload(downloadCtx, cliOpts)
	if err != nil {
		return fmt.Errorf("no version for download: %w", err)
	}

	bundleName := ver.Version.Tarball
	bundlePath := filepath.Join(downloadCtx.DirectoryPrefix, bundleName)

	_, err = os.Stat(bundlePath)
	if err == nil {
		confirmed, err := AskConfirm(os.Stdin, "Confirm overwrite "+bundlePath)
		if err != nil {
			return err
		}

		if !confirmed {
			log.Info("Download is cancelled.")

			return nil
		}
	}

	log.Infof("Downloading %s...", bundleName)

	searchCtx := NewSearchCtx(
		NewPlatformInformer(),
		NewTntIoDownloader(ver.Token),
	)

	searchCtx.Program = ProgramEe
	searchCtx.DevBuilds = downloadCtx.DevBuild
	searchCtx.ReleaseVersion = ver.Release

	bundleSource, err := TntIoMakePkgURI(&searchCtx, bundleName)
	if err != nil {
		return fmt.Errorf("failed to make URI for downloading: %w", err)
	}

	err = DownloadBundle(searchCtx.TntIoDoer,
		bundleName, bundleSource, downloadCtx.DirectoryPrefix)
	if err != nil {
		return fmt.Errorf("download error: %w", err)
	}

	log.Infof("Downloaded to: %q", bundlePath)

	return err
}
