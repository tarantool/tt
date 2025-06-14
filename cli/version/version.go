package version

import (
	"fmt"
	"runtime"
	"strconv"
	"strings"

	goVersion "github.com/hashicorp/go-version"
)

const (
	unknownVersion  = "<unknown>"
	cliVersionTitle = "Tarantool CLI"
)

// Get the value of this variables at build time.
// See magefile for more details.
var (
	gitTag            string
	gitCommit         string
	gitCommitSinceTag string
	versionLabel      string
)

// GetVersion return string with Tarantool CLI version info.
func GetVersion(showShort, needCommit bool) string {
	var version string

	if gitTag == "" {
		version = unknownVersion
	} else {
		if normalizedVersion, err := goVersion.NewVersion(gitTag); err != nil {
			version = gitTag
		} else {
			var versionStrNumbers []string
			for _, num := range normalizedVersion.Segments() {
				versionStrNumbers = append(versionStrNumbers, strconv.Itoa(num))
			}

			version = strings.Join(versionStrNumbers, ".")
		}

		if versionLabel != "" {
			version = fmt.Sprintf("%s/%s", version, versionLabel)
		}
	}

	if showShort || needCommit {
		if needCommit {
			return fmt.Sprintf("%s.%s", version, gitCommit)
		}

		return version
	}

	if gitCommitSinceTag != "" && gitCommitSinceTag != "0" && gitTag != "" {
		return fmt.Sprintf(
			"%s version %s, %s/%s. commit: %s (%s)",
			cliVersionTitle, version, runtime.GOOS, runtime.GOARCH, gitCommit, gitTag,
		)
	}

	return fmt.Sprintf(
		"%s version %s, %s/%s. commit: %s",
		cliVersionTitle, version, runtime.GOOS, runtime.GOARCH, gitCommit,
	)
}
