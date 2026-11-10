package analyzer

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/fetch"
)

// maxProgramSize bounds a program unpacked from an asset: the programs are
// tens of megabytes.
const maxProgramSize = 512 << 20

// DefaultCacheDir is where assets are cached: <user cache>/tt/emmylua, or
// nowhere when the system names no cache directory.
func DefaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, "tt", "emmylua")
}

// fetchPrograms downloads the assets of platform through fetcher, each
// checked against the sha256 tt holds it to, and returns the programs they
// hold.
func fetchPrograms(
	ctx context.Context, fetcher fetch.Fetcher, release Release, platform Platform,
) (map[Program][]byte, error) {
	programs := make(map[Program][]byte, len(Programs))

	for _, program := range Programs {
		asset := platform.Asset(program)

		data, err := fetcher.Fetch(ctx, release.AssetURL(asset), platform.SHA256[program])
		if err != nil {
			return nil, err
		}

		programs[program], err = fetch.UnpackFile(data, string(program), maxProgramSize)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", asset, err)
		}
	}

	return programs, nil
}
