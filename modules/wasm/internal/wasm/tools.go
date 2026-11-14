package wasm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	// hostLinuxAMD64 and hostDarwinARM64 key the builds of a tool.
	hostLinuxAMD64  = "linux/amd64"
	hostDarwinARM64 = "darwin/arm64"
	// versionTimeout bounds a run of a tool that reports its version.
	versionTimeout = 15 * time.Second
	// versionFlag makes most tools print their version.
	versionFlag = "--version"
)

// Tool is a program the components of a language are built with: one tt
// installs into the project, or one the user installs and tt only checks.
type Tool struct {
	// Name is the program, as the builds find it on PATH.
	Name string
	// VersionArgs make it print its version.
	VersionArgs []string
	// Minimum and Below bound the version it must have: at least Minimum,
	// below Below; either may be empty.
	Minimum string
	Below   string
	// Hint says how to get it, for a tool the user installs.
	Hint string
	// Dir, when set, is an environment variable naming the directory whose
	// bin/ holds the tool, as WASI_SDK_PATH does for clang; PATH is the
	// fallback.
	Dir string
	// Release is the build tt installs into the project; nil for a tool the
	// user installs. tt installs nothing into the system.
	Release *ToolRelease
}

// ToolRelease is the build of a tool tt installs into the project.
type ToolRelease struct {
	// Version is the version tt installs.
	Version string
	// Assets are its downloads, by host: GOOS/GOARCH.
	Assets map[string]ToolAsset
	// Wheel is true for a tool whose assets are Python wheels, which pip
	// installs into the environment of tarawasm from the file tt downloaded.
	Wheel bool
	// Tree is the path of the program in an archive tt keeps whole: the
	// archive is unpacked into the version directory, its top directory
	// stripped. Empty for an asset that is, or holds, the program alone.
	Tree string
	// Overlays are more archives of each host unpacked over the tree, as
	// Binaryen is over TinyGo.
	Overlays map[string][]ToolAsset
	// Links are more programs of the tree linked into the project's bin.
	Links []string
	// Unlinked keeps the program out of the project's bin, where it would
	// stand in for a tool of the system in every build: clang. tarawasm is
	// told where it is instead, in each component (see Tool.ProjectSetting).
	Unlinked bool
	// Dir names the version directory; empty is the name of the tool.
	Dir string
}

// ToolAsset is one download of a tool: the program itself, or a .tar.gz
// holding it.
type ToolAsset struct {
	URL    string
	SHA256 string
}

//nolint:gochecknoglobals // Tables of constants: Go has no constant structs.
var (
	toolWasmTools = Tool{
		Name: "wasm-tools", VersionArgs: []string{versionFlag},
		Minimum: "1.245.1", Below: "", Hint: "", Dir: "",
		Release: &ToolRelease{
			Version: "1.261.0",
			Assets: map[string]ToolAsset{
				hostLinuxAMD64: {
					URL: "https://github.com/bytecodealliance/wasm-tools/releases/download/" +
						"v1.261.0/wasm-tools-1.261.0-x86_64-linux.tar.gz",
					SHA256: "ad62b2176037e93e1348cb65d6212d128ca9f097b63d155569f25215818ff7b1",
				},
				hostDarwinARM64: {
					URL: "https://github.com/bytecodealliance/wasm-tools/releases/download/" +
						"v1.261.0/wasm-tools-1.261.0-aarch64-macos.tar.gz",
					SHA256: "94c4fc9baeada6e793f0d4adeca537633b5d93e9dc6bf718be2f478f4afe0296",
				},
			},
			Wheel: false, Tree: "", Overlays: nil, Links: nil, Unlinked: false, Dir: "",
		},
	}
	toolWkg = Tool{
		Name: "wkg", VersionArgs: []string{versionFlag},
		Minimum: "0.16.1", Below: "", Hint: "", Dir: "",
		Release: &ToolRelease{
			Version: "0.16.1",
			Assets: map[string]ToolAsset{
				hostLinuxAMD64: {
					URL: "https://github.com/bytecodealliance/wasm-pkg-tools/releases/download/" +
						"v0.16.1/wkg-x86_64-unknown-linux-gnu",
					SHA256: "235ecf455e11b8b146e00f24f24c95b573cfe6a432f332358463b229311cec57",
				},
				hostDarwinARM64: {
					URL: "https://github.com/bytecodealliance/wasm-pkg-tools/releases/download/" +
						"v0.16.1/wkg-aarch64-apple-darwin",
					SHA256: "3ad0e1d2698607bffe4900fd2320f7d359ec672d8c8a359c77ae4c5e0c7fb3b7",
				},
			},
			Wheel: false, Tree: "", Overlays: nil, Links: nil, Unlinked: false, Dir: "",
		},
	}
	// toolComponentizePy is 0.25.1 exactly: the SDK patches the socket errno
	// mapper of that release. The wheels are on PyPI for both hosts; GitHub
	// has no macOS arm64 build of it. tt downloads the wheel itself, so pip
	// installs it from the file without asking an index.
	toolComponentizePy = Tool{
		Name: "componentize-py", VersionArgs: []string{versionFlag},
		Minimum: "0.25.1", Below: "0.25.2", Hint: "", Dir: "",
		Release: &ToolRelease{
			Version: "0.25.1",
			Assets: map[string]ToolAsset{
				hostLinuxAMD64: {
					URL: "https://files.pythonhosted.org/packages/ed/c6/" +
						"ad1431751ff1969a132323f6e2797ec38ccdf9d0a53aabb45d79221092a4/" +
						"componentize_py-0.25.1-cp39-abi3-manylinux_2_28_x86_64.whl",
					SHA256: "8623cd8cab8453cb0cd8bf03f3c7103420d06f2f3d360186a280a70a1ca5e89f",
				},
				hostDarwinARM64: {
					URL: "https://files.pythonhosted.org/packages/4b/9c/" +
						"f444240899a9b43c60b808d0ece5dff8f3dc6831aa119a1d8eb28d952bad/" +
						"componentize_py-0.25.1-cp39-abi3-macosx_11_0_arm64.whl",
					SHA256: "a556991534cc496207a9d2ca733c4b0f17ed38ef56a8d6b5f1847fdf1225dd1b",
				},
			},
			Wheel: true, Tree: "", Overlays: nil, Links: nil, Unlinked: false, Dir: "",
		},
	}

	// toolTinyGo is TinyGo 0.43, with the Binaryen 133 TinyGo 0.43 is built
	// for laid over it, as tarawasm does: its wasm-opt.
	toolTinyGo = Tool{
		Name: "tinygo", VersionArgs: []string{"version"},
		Minimum: "0.43.0", Below: "0.44.0", Hint: "", Dir: "",
		Release: &ToolRelease{
			Version: "0.43.0",
			Assets: map[string]ToolAsset{
				hostLinuxAMD64: {
					URL: "https://github.com/tinygo-org/tinygo/releases/download/" +
						"v0.43.0/tinygo0.43.0.linux-amd64.tar.gz",
					SHA256: "ca9fc6d055e3889462dea4450a2e42a92242d88b5c362a7ec39bdb563413d78a",
				},
				hostDarwinARM64: {
					URL: "https://github.com/tinygo-org/tinygo/releases/download/" +
						"v0.43.0/tinygo0.43.0.darwin-arm64.tar.gz",
					SHA256: "f5e0161f212fb69ad4650a4809e3ca66856e184892e80058f0d47abc64151812",
				},
			},
			Wheel: false,
			Tree:  "bin/tinygo",
			Overlays: map[string][]ToolAsset{
				hostLinuxAMD64: {{
					URL: "https://github.com/WebAssembly/binaryen/releases/download/" +
						"version_133/binaryen-version_133-x86_64-linux.tar.gz",
					SHA256: "2dc9c7813f5375db93d96ead4b78222fcc3e2677bbb832297af4797782a37489",
				}},
				hostDarwinARM64: {{
					URL: "https://github.com/WebAssembly/binaryen/releases/download/" +
						"version_133/binaryen-version_133-arm64-macos.tar.gz",
					SHA256: "ad66da82ac13f163e424b1643f16c6dfcccc98b5966296b43e52d3cab04f84a8",
				}},
			},
			Links: []string{"bin/wasm-opt"}, Unlinked: false, Dir: "",
		},
	}
	// toolClang is the clang of the wasi-sdk 34, which tarawasm needs with
	// the sysroot next to it. It is not linked into the project's bin.
	toolClang = Tool{
		Name: "clang", VersionArgs: []string{versionFlag},
		Minimum: "19.1.5", Below: "", Hint: "", Dir: "WASI_SDK_PATH",
		Release: &ToolRelease{
			Version: "34",
			Assets: map[string]ToolAsset{
				hostLinuxAMD64: {
					URL: "https://github.com/WebAssembly/wasi-sdk/releases/download/" +
						"wasi-sdk-34/wasi-sdk-34.0-x86_64-linux.tar.gz",
					SHA256: "b761e3a0721dbae9c09a0059e5fdb2bf917d1b4a8a7b430fb3b5aafb0984b2c4",
				},
				hostDarwinARM64: {
					URL: "https://github.com/WebAssembly/wasi-sdk/releases/download/" +
						"wasi-sdk-34/wasi-sdk-34.0-arm64-macos.tar.gz",
					SHA256: "9c59398106b417f8f14913380fdf0097a8cc0ff4af9eb3ce0065a859e88d49e9",
				},
			},
			Wheel: false, Tree: "bin/clang", Overlays: nil, Links: nil,
			Unlinked: true, Dir: "wasi-sdk",
		},
	}
	toolWitBindgen = Tool{
		Name: "wit-bindgen", VersionArgs: []string{versionFlag},
		Minimum: "0.53.1", Below: "", Hint: "", Dir: "",
		Release: &ToolRelease{
			Version: "0.62.0",
			Assets: map[string]ToolAsset{
				hostLinuxAMD64: {
					URL: "https://github.com/bytecodealliance/wit-bindgen/releases/download/" +
						"v0.62.0/wit-bindgen-0.62.0-x86_64-linux.tar.gz",
					SHA256: "3e81cc6523729f7532b4aa7968648a04abf0c711b7d1677150e9121f4e6458fe",
				},
				hostDarwinARM64: {
					URL: "https://github.com/bytecodealliance/wit-bindgen/releases/download/" +
						"v0.62.0/wit-bindgen-0.62.0-aarch64-macos.tar.gz",
					SHA256: "68a8898f8d139d24bd129c5206007dfb7636898edf7659348760d8159ecea9d6",
				},
			},
			Wheel: false, Tree: "", Overlays: nil, Links: nil, Unlinked: false, Dir: "",
		},
	}

	// The tools the user installs, with the versions tarawasm and the SDK
	// accept.
	toolPython = userTool("python3", "3.10.0", "", "the Python of the system, 3.10 or newer")
	toolGo     = Tool{
		Name: "go", VersionArgs: []string{"version"}, Minimum: "1.25.0", Below: "1.28.0",
		Hint: "https://go.dev/dl/ (1.25 to 1.27)", Dir: "", Release: nil,
	}
	toolRustc = userTool("rustc", "1.96.0", "",
		"rustup (https://rustup.rs), then rustup target add wasm32-wasip1")
	toolCargo          = userTool("cargo", "1.96.0", "", "rustup (https://rustup.rs)")
	toolCargoComponent = userTool("cargo-component", "0.21.1", "",
		"cargo install --locked cargo-component --version 0.21.1")
	toolNode = userTool("node", "24.0.0", "", "https://nodejs.org/ (24 or newer)")
	toolJco  = userTool("jco", "1.37.0", "",
		"npm install --global @bytecodealliance/jco@1.37.0 "+
			"@bytecodealliance/componentize-js@0.23.0 @bytecodealliance/preview2-shim@0.17.8")
	toolDotnet = userTool("dotnet", "10.0.402", "10.0.403",
		"the .NET SDK 10.0.402 (https://dotnet.microsoft.com/download/dotnet/10.0); "+
			"C# components build on Linux x86-64 only")

	// toolchains are the tools of each language tarawasm builds.
	toolchains = map[string][]Tool{
		"python": {toolPython, toolWasmTools, toolWkg, toolComponentizePy},
		"go":     {toolWasmTools, toolWkg, toolGo, toolTinyGo},
		"rust":   {toolWasmTools, toolWkg, toolRustc, toolCargo, toolCargoComponent},
		"js":     {toolWasmTools, toolWkg, toolNode, toolJco},
		"c":      {toolWasmTools, toolWkg, toolClang, toolWitBindgen},
		"csharp": {toolWasmTools, toolWkg, toolDotnet},
	}
)

// userTool is a tool the user installs.
func userTool(name, minimum, below, hint string) Tool {
	return Tool{
		Name: name, VersionArgs: []string{versionFlag}, Minimum: minimum, Below: below,
		Hint: hint, Dir: "", Release: nil,
	}
}

// Languages returns the languages tt knows the toolchains of.
func Languages() []string {
	return sortedKeys(toolchains)
}

// Toolchain returns the tools of language.
func Toolchain(language string) ([]Tool, error) {
	tools, ok := toolchains[language]
	if !ok {
		return nil, fmt.Errorf("%w: %q; known: %s", ErrUnknownLanguage, language,
			strings.Join(Languages(), ", "))
	}

	return tools, nil
}

// Requirement renders the versions the tool must have.
func (t Tool) Requirement() string {
	switch {
	case t.Release != nil:
		return t.Release.Version
	case t.Minimum != "" && t.Below != "":
		return ">=" + t.Minimum + ", <" + t.Below
	case t.Minimum != "":
		return ">=" + t.Minimum
	default:
		return "any"
	}
}

// ToolStatus is what a check found of a tool.
type ToolStatus struct {
	Tool Tool
	// Path is where it was found; empty when it was not.
	Path string
	// Version is the version it reports; empty when it was not found.
	Version string
	// OK is true for a tool found in a version it may have.
	OK bool
}

// Problem says what is wrong with the tool; empty when nothing is.
func (s ToolStatus) Problem() string {
	switch {
	case s.OK:
		return ""
	case s.Path == "":
		return "missing"
	case s.Version == "":
		return "reports no version"
	default:
		return "version " + s.Version + " does not fit"
	}
}

// CheckTool finds tool in the directories of path (a PATH value), runs it for
// its version and checks the version against the bounds of the tool.
func CheckTool(ctx context.Context, tool Tool, path string) ToolStatus {
	status := ToolStatus{Tool: tool, Path: findTool(tool, path), Version: "", OK: false}
	if status.Path == "" {
		return status
	}

	status.Version = toolVersion(ctx, status.Path, tool.VersionArgs)
	if status.Version == "" {
		return status
	}

	status.OK = versionFits(status.Version, tool.Minimum, tool.Below)

	return status
}

// checkProgram checks the program of tool at program, where tt installed it:
// the version it reports against the bounds of the tool.
func checkProgram(ctx context.Context, tool Tool, program string) ToolStatus {
	status := ToolStatus{Tool: tool, Path: "", Version: "", OK: false}
	if !isExecutable(program) {
		return status
	}

	status.Path = program
	status.Version = toolVersion(ctx, program, tool.VersionArgs)
	status.OK = status.Version != "" && versionFits(status.Version, tool.Minimum, tool.Below)

	return status
}

// findTool returns the program of tool: under $Dir/bin, when the tool names
// such a variable and it is set, or else in the directories of path.
func findTool(tool Tool, path string) string {
	if tool.Dir != "" {
		if dir := os.Getenv(tool.Dir); dir != "" {
			candidate := filepath.Join(dir, "bin", tool.Name)
			if isExecutable(candidate) {
				return candidate
			}
		}
	}

	for _, dir := range filepath.SplitList(path) {
		if !filepath.IsAbs(dir) {
			continue
		}

		candidate := filepath.Join(dir, tool.Name)
		if isExecutable(candidate) {
			return candidate
		}
	}

	return ""
}

// versionRe finds the first version number a tool prints.
var versionRe = regexp.MustCompile(`(\d+)\.(\d+)(?:\.(\d+))?`)

// toolVersion runs program with args and returns the first version it prints.
func toolVersion(ctx context.Context, program string, args []string) string {
	ctx, cancel := context.WithTimeout(ctx, versionTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, program, args...).CombinedOutput()
	if err != nil {
		return ""
	}

	return versionRe.FindString(string(out))
}

// versionFits reports whether version is at least minimum and below below;
// an empty bound does not bound.
func versionFits(version, minimum, below string) bool {
	if minimum != "" && compareVersions(version, minimum) < 0 {
		return false
	}

	return below == "" || compareVersions(version, below) < 0
}

// compareVersions compares two dotted versions number by number, a missing
// number counting as 0.
func compareVersions(left, right string) int {
	parse := func(version string) []int {
		var parts []int

		for part := range strings.SplitSeq(version, ".") {
			number, err := strconv.Atoi(part)
			if err != nil {
				break
			}

			parts = append(parts, number)
		}

		return parts
	}

	leftParts, rightParts := parse(left), parse(right)
	for len(leftParts) < len(rightParts) {
		leftParts = append(leftParts, 0)
	}

	for len(rightParts) < len(leftParts) {
		rightParts = append(rightParts, 0)
	}

	return slices.Compare(leftParts, rightParts)
}

// hostKey names the host tt runs on, as the assets of a tool are keyed.
func hostKey() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
