package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/binary/search"
	"golang.org/x/sys/unix"
)

var (
	errCurrentDirectoryUnavailable = errors.New("can't get current dir: ")
)

// searchSDKVersionToDownload wrapper to search for SDK bundles.
func searchSDKVersionToDownload(installCtx InstallCtx, cliOpts *config.CliOpts) (
	search.BundleInfo, error,
) {
	log.Info("Search for the requested version...")

	searchCtx := search.NewSearchCtx(search.NewPlatformInformer(), search.NewTntIoDoer())

	searchCtx.BinaryContext = installCtx.BinaryContext
	searchCtx.Filter = search.SearchAll
	searchCtx.DevBuilds = installCtx.DevBuild

	bundles, err := search.FetchBundlesInfo(&searchCtx, cliOpts)
	if err != nil {
		return search.BundleInfo{}, fmt.Errorf("cannot get SDK bundles list: %w", err)
	}

	return search.SelectVersion(bundles, installCtx.requestedVersion)
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
		confirmed, err := binary.AskConfirm(os.Stdin, "Confirm overwrite "+bundlePath)
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
		NewTntIoDownloader(ver.Token),
	)

	searchCtx.BinaryContext = installCtx.BinaryContext
	searchCtx.DevBuilds = installCtx.DevBuild
	searchCtx.ReleaseVersion = ver.Release

	bundleSource, err := search.TntIoMakePkgURI(&searchCtx, bundleName)
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
