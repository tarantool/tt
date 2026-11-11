package wasm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/v3/internal/scope"
)

// ErrNotReady reports a project tt wasm status found something missing in.
var ErrNotReady = errors.New("the project is not ready")

// ComponentStatus is what tt wasm status found of a component.
type ComponentStatus struct {
	Component

	// SDKRestored is true when the SDK the component pins is in its
	// .tarawasm, as tt wasm prepare puts it.
	SDKRestored bool
}

// StatusReport is what tt wasm status found, changing nothing.
type StatusReport struct {
	// Declared and Registry are the wasm rock the manifest declares; empty
	// when it declares none, or no exact version.
	Declared string
	Registry string
	// Rock is the rock of this host in the registry; empty when it is not
	// there, or when the registry is a rock server (FromServer).
	Rock string
	// FromServer is true when the registry is a rock server, which status
	// does not look into: a URL, or a directory with a rock index. A build
	// fetches the rock from it.
	FromServer bool
	// Tarawasm is the tarawasm of the project; empty when it is not there.
	Tarawasm string
	// Components are the components tarawasm builds.
	Components []ComponentStatus
	// Languages and Toolchains are their languages and what was found of the
	// toolchain of each.
	Languages  []string
	Toolchains map[string][]ToolStatus
	// Problems are what keeps the project from building, one a line.
	Problems []string
}

// Status reports how far the project in projectDir is prepared, as tt wasm
// prepare leaves it and tt package build needs it, and changes nothing.
func Status(ctx context.Context, projectDir string) (*StatusReport, error) {
	man, err := readManifest(projectDir)
	if err != nil {
		return nil, err
	}

	root := filepath.Join(projectDir, scope.ProjectDirName)
	layout := tarawasmLayout(root, "")
	report := &StatusReport{
		Declared: "", Registry: "", Rock: "", FromServer: false, Tarawasm: "",
		Components: nil, Languages: nil, Toolchains: map[string][]ToolStatus{}, Problems: nil,
	}

	report.Declared, err = DeclaredVersion(projectDir)
	if err != nil {
		return nil, err
	}

	report.Registry = man.Dependencies[RockName].Registry
	report.statusRock(projectDir)

	if isExecutable(layout.Link) {
		report.Tarawasm = layout.Link
	}

	components, err := FindComponents(projectDir)
	if err != nil {
		return nil, err
	}

	if len(components) > 0 && report.Tarawasm == "" {
		report.problemf("tarawasm is not in %s: run tt wasm prepare", layout.Link)
	}

	report.statusComponents(components)

	report.Languages = componentLanguages(components, "")

	for _, language := range report.Languages {
		tools, err := Toolchain(language)
		if err != nil {
			report.problemf("%v", err)

			continue
		}

		path := filepath.Join(root, "bin") + string(os.PathListSeparator) + os.Getenv("PATH")

		statuses, err := checkTools(ctx, tools, path, installedPrograms(root, layout, tools),
			language)

		report.Toolchains[language] = statuses

		if err != nil {
			report.problemf("the %s toolchain is not ready: run tt wasm prepare, "+
				"and install what it names", language)
		}
	}

	return report, nil
}

// statusRock finds the rock of this host for the declared release.
//
// A rock server is not looked into - a URL, or a directory with a rock
// index, which may name rocks anywhere: a build fetches the rock from it and
// checks it against the lock. A directory with rocks of its own, such as the
// registry tt wasm prepare --from packs, must hold one of the declared
// version, of any revision, for this host. Neither needs the release to be
// one tt knows.
func (r *StatusReport) statusRock(projectDir string) {
	if r.Declared == "" {
		r.problemf("the package declares no exact %s version: run tt wasm prepare", RockName)

		return
	}

	if strings.Contains(r.Registry, "://") {
		r.FromServer = true

		return
	}

	registry := r.Registry
	if registry == "" || !filepath.IsAbs(registry) {
		registry = filepath.Join(projectDir, filepath.FromSlash(registry))
	}

	_, err := os.Stat(filepath.Join(registry, remote.RocksIndexFile))
	if err == nil {
		r.FromServer = true

		return
	}

	pattern := RockName + "-" + r.Declared + "-*." + hostRockArch() + rockSuffix

	rocks, err := filepath.Glob(filepath.Join(registry, pattern))
	if err != nil || len(rocks) == 0 {
		r.problemf("no %s in %s: run tt wasm prepare", pattern, r.Registry)

		return
	}

	// Any revision shows the rock is there; the lock says which one installs.
	r.Rock = rocks[len(rocks)-1]
}

// statusComponents checks the SDK of every component.
func (r *StatusReport) statusComponents(components []Component) {
	for _, component := range components {
		_, err := os.Stat(filepath.Join(component.Dir, ".tarawasm", "tarantool"))
		restored := err == nil

		r.Components = append(r.Components,
			ComponentStatus{Component: component, SDKRestored: restored})

		if component.SDKVersion != "" && !restored {
			r.problemf("the SDK of component %s is not restored: run tt wasm prepare",
				component.Name)
		}
	}

	if r.Declared != "" {
		r.Problems = append(r.Problems, SDKMismatches(components, r.Declared)...)
	}
}

// problem records a problem.
func (r *StatusReport) problemf(format string, args ...any) {
	r.Problems = append(r.Problems, fmt.Sprintf(format, args...))
}

// installedPrograms returns where tt installs the tools of the toolchain it
// installs, by tool, for the check to find them there.
func installedPrograms(root string, layout TarawasmLayout, tools []Tool) map[string]string {
	programs := map[string]string{}

	for _, tool := range tools {
		switch {
		case tool.Release == nil:
		case tool.Release.Wheel:
			programs[tool.Name] = filepath.Join(layout.Env, "bin", tool.Name)
		case tool.Release.Tree != "":
			programs[tool.Name] = filepath.Join(toolDir(root, tool),
				filepath.FromSlash(tool.Release.Tree))
		default:
			programs[tool.Name] = filepath.Join(toolDir(root, tool), tool.Name)
		}
	}

	return programs
}
