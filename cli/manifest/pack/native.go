package pack

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// checkNativeFiles fails on every native file that does not run on target,
// naming each with the platforms it does run on, so a tree with several is
// fixed in one pass. An archive is labeled with one platform: a native file of
// another one would fail only once the archive is installed and started, and
// pack cannot rebuild it for the archive's platform.
func checkNativeFiles(files []native.File, target native.Platform) error {
	var problems []error

	for _, file := range files {
		if file.RunsOn(target) {
			continue
		}

		problems = append(problems, fmt.Errorf(
			"%s: %w: it runs on %s, but the archive is packed for %s",
			file.Name, errForeignNative, native.Describe(file.Platforms),
			describeTarget(target)))
	}

	joined := errors.Join(problems...)
	if joined == nil {
		return nil
	}

	return stateErrorf("%w", joined)
}

// describeTarget renders the platform an archive is packed for, saying when
// it is the host's: the archive is never labeled anything else today, and a
// user reading the error would otherwise wonder where the platform came from.
func describeTarget(target native.Platform) string {
	if target == native.Host() {
		return target.String() + ", the host platform"
	}

	return target.String()
}
