package binary

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/config"
	"golang.org/x/sys/unix"
)

var (
	errCurrentDirectoryUnavailable = errors.New("can't get current dir: ")
)

// searchSDKVersionToDownload wrapper to search for SDK bundles.
func searchSDKVersionToDownload(installCtx InstallCtx, cliOpts *config.CliOpts) (
	BundleInfo, error,
) {
	log.Info("Search for the requested version...")

	searchCtx := NewSearchCtx(NewPlatformInformer(), NewTntIoDoer())

	searchCtx.BinaryContext = installCtx.BinaryContext
	searchCtx.Filter = SearchAll
	searchCtx.DevBuilds = installCtx.DevBuild

	bundles, err := FetchBundlesInfo(&searchCtx, cliOpts)
	if err != nil {
		return BundleInfo{}, fmt.Errorf("cannot get SDK bundles list: %w", err)
	}

	return SelectVersion(bundles, installCtx.version)
}

// DownloadSDK downloads and saves a Tarantool SDK bundle.
func DownloadSDK(installCtx InstallCtx, cliOpts *config.CliOpts) error {
	var err error

	if len(installCtx.Destination) == 0 {
		installCtx.Destination, err = os.Getwd()
		if err != nil {
			return fmt.Errorf("%w%s", errCurrentDirectoryUnavailable, err.Error())
		}
	}

	err = unix.Access(installCtx.Destination, unix.W_OK)
	if err != nil {
		return fmt.Errorf("invalid destination directory %q: %w", installCtx.Destination, err)
	}

	ver, err := searchSDKVersionToDownload(installCtx, cliOpts)
	if err != nil {
		return fmt.Errorf("no version for download: %w", err)
	}

	bundleName := ver.Version.Tarball
	bundlePath := filepath.Join(installCtx.Destination, bundleName)

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

	searchCtx.BinaryContext = installCtx.BinaryContext
	searchCtx.DevBuilds = installCtx.DevBuild
	searchCtx.ReleaseVersion = ver.Release

	bundleSource, err := TntIoMakePkgURI(&searchCtx, bundleName)
	if err != nil {
		return fmt.Errorf("failed to make URI for downloading: %w", err)
	}

	err = DownloadBundle(searchCtx.TntIoDoer,
		bundleName, bundleSource, installCtx.Destination)
	if err != nil {
		return fmt.Errorf("download error: %w", err)
	}

	log.Infof("Downloaded to: %q", bundlePath)

	return err
}
