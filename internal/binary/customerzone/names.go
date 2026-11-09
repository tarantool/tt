package customerzone

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

// The platforms of the customer zone, as runtime.GOOS and runtime.GOARCH name them.
const (
	goosLinux    = "linux"
	goosDarwin   = "darwin"
	goarchAMD64  = "amd64"
	goarchARM64  = "arm64"
	apiOSLinux   = "linux"
	apiOSMacOS   = "macos"
	apiArchX8664 = "x86_64"
	apiArchARM64 = "aarch64"
)

var (
	errUnsupportedArchitecture = errors.New("unsupported architecture: ")
	errUnsupportedOS           = errors.New("unsupported OS: ")
	errUnknownProduct          = errors.New("not a product of the customer zone: ")
)

// products are the products the customer zone has archives of. The archives
// of tarantool-ee are the SDK ones.
var products = []binary.Program{
	binary.ProgramEe, binary.ProgramSDK, binary.ProgramTcm, binary.ProgramTtEe,
}

// productNames lists products as bundle.Query names them.
func productNames() []string {
	names := make([]string, 0, len(products))
	for _, program := range products {
		names = append(names, program.String())
	}

	return names
}

// parseProduct returns the product a query asks for, if the customer zone has it.
func parseProduct(product string) (binary.Program, error) {
	program, err := binary.ParseProgram(product)
	if err != nil || program.BundleSource() == binary.ProgramUnknown {
		return binary.ProgramUnknown, fmt.Errorf("%w%q", errUnknownProduct, product)
	}

	return program, nil
}

// archivePrefix is how the name of every archive of program starts.
func archivePrefix(program binary.Program) string {
	if program.BundleSource() == binary.ProgramSDK {
		return "tarantool-enterprise-sdk-"
	}

	return program.BundleSource().String() + "-"
}

// archivePattern extracts the version from the name of an archive of program.
func archivePattern(program binary.Program) *regexp.Regexp {
	source := program.BundleSource()
	if source == binary.ProgramSDK {
		return regexp.MustCompile(
			`^(?P<tarball>tarantool-enterprise-sdk-(?P<version>.*r[0-9]{1,3}).*\.tar\.gz)$`)
	}

	return regexp.MustCompile(`^(?P<tarball>` + regexp.QuoteMeta(source.String()) +
		`-(?P<version>\d+\.\d+\.\d+[^.]*?)` +
		`(?:[.-](?:linux|macos|darwin)[.-](?:amd64|arm64|x86_64|aarch64))?\.tar\.gz)$`)
}

// platformSuffix is how the names of the archives of program for the
// platform end. The archives of tt-ee for every platform share one catalog,
// and there are only Linux amd64 ones; the other products are listed by
// platform, so their suffix is empty.
func platformSuffix(program binary.Program, goos, goarch string) (string, error) {
	if program.BundleSource() != binary.ProgramTtEe {
		return "", nil
	}

	if goos != goosLinux {
		return "", fmt.Errorf("%w%s: tt-ee archives are available only for Linux amd64",
			errUnsupportedOS, goos)
	}

	if goarch != goarchAMD64 {
		return "", fmt.Errorf("%w%s: tt-ee archives are available only for Linux amd64",
			errUnsupportedArchitecture, goarch)
	}

	return "-linux-amd64.tar.gz", nil
}

// onReleaseLine reports whether available is of the release line --version
// names, empty for every line. tt-ee has no release directories to ask for a
// line, so its versions are matched one by one.
func onReleaseLine(line string, available version.Version) bool {
	if line == "" {
		return true
	}

	_, err := version.MatchVersion(line, []version.Version{available})

	return err == nil
}

// parseArchive describes the archive of program named name. ok is false for
// a name that is not one of an archive of program.
func parseArchive(program binary.Program, name string) (bundle.Artifact, bool, error) {
	var artifact bundle.Artifact

	matches := binary.FindNamedMatches(archivePattern(program), name)
	if len(matches) == 0 {
		return artifact, false, nil
	}

	parsed, err := version.Parse(matches["version"])
	if err != nil {
		return artifact, true, fmt.Errorf("parse the version of %s: %w", name, err)
	}

	artifact.Version, artifact.Name = parsed.Str, name

	if parsed.BuildName != "" {
		artifact.Flavor = strings.Split(parsed.BuildName, "-")
		artifact.Version = strings.TrimPrefix(parsed.Str, parsed.BuildName+"-")
	}

	return artifact, true, nil
}
