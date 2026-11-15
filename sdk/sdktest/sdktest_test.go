package sdktest_test

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/sdk/sdktest"
)

// fatalTB is a testing.TB whose Fatalf records the message and ends the
// goroutine, so a test can check that the fake fails the test it is given.
type fatalTB struct {
	testing.TB

	message string
}

// Fatalf records the message and stops the calling goroutine.
func (f *fatalTB) Fatalf(format string, args ...any) {
	f.message = fmt.Sprintf(format, args...)

	runtime.Goexit()
}

// fatal runs body with a fatalTB wrapping t and returns the message body
// failed with, or "" when it returned normally.
func fatal(t *testing.T, body func(tb testing.TB)) string {
	t.Helper()

	tb := &fatalTB{TB: t, message: ""}

	var wg sync.WaitGroup

	wg.Go(func() { body(tb) })
	wg.Wait()

	return tb.message
}

func TestServicesPanicBeforeStart(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithName("mymod"))

	calls := map[string]func(){
		"Tarantool": func() { _, _ = services.Tarantool() },
		"Integrity": func() { _ = services.Integrity() },
		"Project":   func() { _ = services.Project() },
		"Rocks":     func() { _ = services.Rocks() },
		"Confirm":   func() { _, _ = services.Confirm("sure?", true) },
		"Streams":   func() { _ = services.Streams() },
		"ClusterConfig": func() {
			_, _ = services.ClusterConfig(t.Context(), sdk.AppSource("app"))
		},
		"Exit": func() { services.Exit(nil) },
	}

	for method, call := range calls {
		assert.PanicsWithValue(t, `module "mymod" used Services.`+method+
			" before tt was configured; use it in a command's hooks, not in the constructor",
			call, method)
	}

	assert.NotPanics(t, func() { services.Log().Info("constructing") })

	services.Start()

	for method, call := range calls {
		if method == "Confirm" || method == "Exit" {
			continue // Confirm needs an answer, Exit ends the command; covered below.
		}

		assert.NotPanics(t, call, method)
	}
}

func TestServicesConfirm(t *testing.T) {
	t.Parallel()

	t.Run("answers are consumed in order", func(t *testing.T) {
		t.Parallel()

		services := sdktest.New(t, sdktest.WithAnswers(true, false, true))
		services.Start()

		for i, want := range []bool{true, false, true} {
			got, err := services.Confirm(fmt.Sprintf("question %d?", i), !want)
			require.NoError(t, err)
			assert.Equal(t, want, got, "answer %d", i)
		}

		assert.Equal(t, "question 0? [y/n]: question 1? [y/n]: question 2? [y/n]: ",
			services.Stderr())
	})

	t.Run("running out of answers fails the test", func(t *testing.T) {
		t.Parallel()

		message := fatal(t, func(tb testing.TB) {
			tb.Helper()

			services := sdktest.New(tb, sdktest.WithAnswers(true))
			services.Start()

			_, _ = services.Confirm("first?", false)
			_, _ = services.Confirm("second?", false)
		})

		assert.Contains(t, message, `Confirm("second?"): no answer left`)
	})

	t.Run("no answers given fails the test", func(t *testing.T) {
		t.Parallel()

		message := fatal(t, func(tb testing.TB) {
			tb.Helper()

			services := sdktest.New(tb)
			services.Start()

			_, _ = services.Confirm("sure?", false)
		})

		assert.Contains(t, message, "no answer left")
	})

	t.Run("no prompt returns the fallback", func(t *testing.T) {
		t.Parallel()

		services := sdktest.New(t, sdktest.WithNoPrompt())
		services.Start()

		for _, fallback := range []bool{true, false} {
			got, err := services.Confirm("sure?", fallback)
			require.NoError(t, err)
			assert.Equal(t, fallback, got)
		}

		assert.Empty(t, services.Stderr(), "nothing is asked")
	})

	t.Run("answers and no prompt exclude each other", func(t *testing.T) {
		t.Parallel()

		message := fatal(t, func(tb testing.TB) {
			tb.Helper()
			sdktest.New(tb, sdktest.WithAnswers(true), sdktest.WithNoPrompt())
		})

		assert.Contains(t, message, "exclude each other")
	})
}

func TestServicesTarantool(t *testing.T) {
	t.Parallel()

	none := sdktest.New(t)
	none.Start()

	_, err := none.Tarantool()
	require.ErrorIs(t, err, sdk.ErrNotFound)

	version := sdk.TarantoolVersion{Major: 3, Minor: 2, Edition: sdk.EditionEE}
	services := sdktest.New(t, sdktest.WithTarantool("/opt/tarantool/bin/tarantool", version))
	services.Start()

	tarantool, err := services.Tarantool()
	require.NoError(t, err)
	assert.Equal(t, "/opt/tarantool/bin/tarantool", tarantool.Path())

	got, err := tarantool.Version()
	require.NoError(t, err)
	assert.Equal(t, version, got)
}

func TestServicesIntegrity(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("content"), 0o600))

	services := sdktest.New(t)
	services.Start()

	file, err := services.Integrity().Open(path)
	require.NoError(t, err)

	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.NoError(t, file.Close())
	assert.Equal(t, "content", string(data))

	errTampered := errors.New("tampered")
	refusing := sdktest.New(t, sdktest.WithIntegrity(func(string) (io.ReadCloser, error) {
		return nil, errTampered
	}))
	refusing.Start()

	_, err = refusing.Integrity().Open(path)
	require.ErrorIs(t, err, errTampered)
}

func TestServicesProject(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	dir, err := services.Project().Dir()
	require.NoError(t, err)
	assert.True(t, filepath.IsAbs(dir), dir)
	assert.DirExists(t, dir)

	relative := sdktest.New(t, sdktest.WithProject("some/project"))
	relative.Start()

	dir, err = relative.Project().Dir()
	require.NoError(t, err)

	wd, err := os.Getwd()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(wd, "some", "project"), dir)
}

// TestServicesProjectAddDependency checks that AddDependency records its
// calls and reports a declaration already there as the core does.
func TestServicesProjectAddDependency(t *testing.T) {
	t.Parallel()

	moves := []sdk.DependencyMove{{Name: "checks", From: "", To: "3.3.0-1"}}

	services := sdktest.New(t,
		sdktest.WithDeclared(sdk.Dependency{Name: "luatest", Constraint: "1.0.1", Dev: true}),
		sdktest.WithMoves(moves...))
	services.Start()

	project := services.Project()

	// A dev declaration is not one in [dependencies].
	change, err := project.AddDependency(t.Context(), sdk.Dependency{Name: "luatest"})
	require.NoError(t, err)
	assert.Equal(t, sdk.DependencyChange{Existed: false, Previous: "", Moves: moves}, change)

	// Without a registry the constraint is rewritten.
	change, err = project.AddDependency(t.Context(),
		sdk.Dependency{Name: "luatest", Constraint: ">=1.1", Dev: true})
	require.NoError(t, err)
	assert.True(t, change.Existed)
	assert.Equal(t, "1.0.1", change.Previous)

	// With one the declaration is left as it is.
	registry := sdk.Dependency{
		Name: "emmylua", Constraint: "0.30.1", Registry: "https://rocks.example/",
		Dev: true, Registries: []string{"mirror"},
	}

	change, err = project.AddDependency(t.Context(), registry)
	require.NoError(t, err)
	assert.False(t, change.Existed)

	registry.Constraint = "0.31.0"

	change, err = project.AddDependency(t.Context(), registry)
	require.NoError(t, err)
	assert.True(t, change.Existed)
	assert.Equal(t, "0.30.1", change.Previous)

	change, err = project.AddDependency(t.Context(),
		sdk.Dependency{Name: "luatest", Dev: true})
	require.NoError(t, err)
	assert.Equal(t, ">=1.1", change.Previous)

	change, err = project.AddDependency(t.Context(),
		sdk.Dependency{Name: "luatest", Dev: true})
	require.NoError(t, err)
	assert.Equal(t, "*", change.Previous, "no constraint declares any version")

	added := services.AddedDependencies()
	require.Len(t, added, 6)
	assert.Equal(t, sdk.Dependency{Name: "luatest"}, added[0])
	assert.Equal(t, registry, added[3])

	added[3].Registries[0] = "changed"
	assert.Equal(t, []string{"mirror"}, services.AddedDependencies()[3].Registries,
		"the record is a copy")
}

// TestServicesProjectAddDependencyError checks that WithAddDependencyError
// fails every call, which is recorded and declares nothing.
func TestServicesProjectAddDependencyError(t *testing.T) {
	t.Parallel()

	failure := errors.New("no rock server answers")

	services := sdktest.New(t, sdktest.WithAddDependencyError(failure))
	services.Start()

	dep := sdk.Dependency{Name: "checks"}

	for range 2 {
		change, err := services.Project().AddDependency(t.Context(), dep)
		require.ErrorIs(t, err, failure)
		assert.Equal(t, sdk.DependencyChange{}, change)
	}

	assert.Equal(t, []sdk.Dependency{dep, dep}, services.AddedDependencies())
}

// TestServicesProjectInstall checks that Install records its calls and runs
// what WithInstall gives in the project directory.
func TestServicesProjectInstall(t *testing.T) {
	t.Parallel()

	plain := sdktest.New(t)
	plain.Start()

	require.NoError(t, plain.Project().Install(t.Context(), sdk.InstallOptions{}))
	assert.Equal(t, []sdk.InstallOptions{{}}, plain.Installs())
	assert.Empty(t, plain.AddedDependencies())

	failure := errors.New("rock server is down")
	calls := 0

	services := sdktest.New(t,
		sdktest.WithInstall(func(dir string, opts sdk.InstallOptions) error {
			calls++

			if len(opts.Registries) != 0 {
				return failure
			}

			bin := filepath.Join(dir, ".rocks", "bin")

			err := os.MkdirAll(bin, 0o755)
			if err != nil {
				return err
			}

			return os.WriteFile(filepath.Join(bin, "tool"), []byte("#!/bin/sh\n"), 0o755)
		}))
	services.Start()

	dir, err := services.Project().Dir()
	require.NoError(t, err)

	require.NoError(t, services.Project().Install(t.Context(), sdk.InstallOptions{}))
	assert.FileExists(t, filepath.Join(dir, ".rocks", "bin", "tool"))

	mirror := sdk.InstallOptions{Registries: []string{"mirror"}}

	err = services.Project().Install(t.Context(), mirror)
	require.ErrorIs(t, err, failure)

	assert.Equal(t, 2, calls)
	assert.Equal(t, []sdk.InstallOptions{{}, mirror}, services.Installs())
}

// TestServicesProjectRoot checks the root of the project, by default the
// directory of the command and below it with WithRoot, its StateDir, and
// that Install runs in it.
func TestServicesProjectRoot(t *testing.T) {
	t.Parallel()

	plain := sdktest.New(t)
	plain.Start()

	dir, err := plain.Project().Dir()
	require.NoError(t, err)

	root, err := plain.Project().Root()
	require.NoError(t, err)
	assert.Equal(t, dir, root)

	state, err := plain.Project().StateDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(dir, ".tarantool"), state)
	assert.NoDirExists(t, state, "nothing is created")

	top := t.TempDir()
	sub := filepath.Join(top, "sub")

	var installedIn string

	below := sdktest.New(t, sdktest.WithProject(sub), sdktest.WithRoot(top),
		sdktest.WithInstall(func(dir string, _ sdk.InstallOptions) error {
			installedIn = dir

			return nil
		}))
	below.Start()

	dir, err = below.Project().Dir()
	require.NoError(t, err)
	assert.Equal(t, sub, dir)

	root, err = below.Project().Root()
	require.NoError(t, err)
	assert.Equal(t, top, root)

	state, err = below.Project().StateDir()
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(top, ".tarantool"), state)

	require.NoError(t, below.Project().Install(t.Context(), sdk.InstallOptions{}))
	assert.Equal(t, top, installedIn)
}

// TestServicesProjectManifest checks that the manifest is what WithManifest
// and WithDeclared give, with what AddDependency and AddComponent declared
// since, a copy each time, and that a project WithNoManifest made has none.
func TestServicesProjectManifest(t *testing.T) {
	t.Parallel()

	empty := sdktest.New(t)
	empty.Start()

	manifest, err := empty.Project().Manifest()
	require.NoError(t, err)
	assert.Equal(t, sdk.Manifest{
		Dependencies:    map[string]sdk.ManifestDependency{},
		DevDependencies: map[string]sdk.ManifestDependency{},
		Components:      map[string]sdk.ManifestComponent{},
	}, manifest)

	given := sdk.Manifest{
		Dependencies: map[string]sdk.ManifestDependency{
			"wasm":  {Version: "0.1.0", Registry: ".tarantool/registry"},
			"local": {Path: "../local"},
		},
		DevDependencies: nil,
		Components: map[string]sdk.ManifestComponent{
			"lua":    {Path: "."},
			"router": {Path: "router", Build: &sdk.ManifestBuild{Backend: "shell", Cwd: "build"}},
		},
	}

	services := sdktest.New(t, sdktest.WithManifest(given),
		sdktest.WithDeclared(sdk.Dependency{Name: "luatest", Constraint: "1.0.1", Dev: true}))
	services.Start()

	project := services.Project()

	manifest, err = project.Manifest()
	require.NoError(t, err)
	assert.Equal(t, given.Dependencies, manifest.Dependencies)
	assert.Equal(t, given.Components, manifest.Components)
	assert.Equal(t, map[string]sdk.ManifestDependency{"luatest": {Version: "1.0.1"}},
		manifest.DevDependencies)

	// A copy: neither what was given nor what was returned changes it.
	manifest.Components["router"].Build.Cwd = "elsewhere"
	given.Components["lua"] = sdk.ManifestComponent{Path: "changed"}

	again, err := project.Manifest()
	require.NoError(t, err)
	assert.Equal(t, "build", again.Components["router"].Build.Cwd)
	assert.Equal(t, ".", again.Components["lua"].Path)

	// What WithManifest declares is declared for AddDependency.
	change, err := project.AddDependency(t.Context(), sdk.Dependency{
		Name: "wasm", Constraint: "0.2.0", Registry: "https://rocks.example/",
	})
	require.NoError(t, err)
	assert.True(t, change.Existed)
	assert.Equal(t, "0.1.0", change.Previous)

	assert.Equal(t, ".tarantool/registry", change.PreviousRegistry)

	_, err = project.AddDependency(t.Context(), sdk.Dependency{Name: "checks"})
	require.NoError(t, err)

	manifest, err = project.Manifest()
	require.NoError(t, err)
	assert.Equal(t, sdk.ManifestDependency{Version: "0.2.0", Registry: "https://rocks.example/"},
		manifest.Dependencies["wasm"], "a declaration takes the constraint and the registry")
	assert.Equal(t, sdk.ManifestDependency{Version: "*"}, manifest.Dependencies["checks"])

	// A registry the declaration names stays for a dependency of none.
	_, err = project.AddDependency(t.Context(), sdk.Dependency{Name: "wasm", Constraint: "0.3.0"})
	require.NoError(t, err)

	manifest, err = project.Manifest()
	require.NoError(t, err)
	assert.Equal(t, sdk.ManifestDependency{Version: "0.3.0", Registry: "https://rocks.example/"},
		manifest.Dependencies["wasm"])

	// RemoveDependency removes it from both tables, and refuses a name of
	// neither.
	change, err = project.RemoveDependency(t.Context(), "luatest", sdk.ResolveOptions{})
	require.NoError(t, err)
	assert.Equal(t, sdk.DependencyChange{Existed: true, Previous: "1.0.1", Moves: nil}, change)

	_, err = project.RemoveDependency(t.Context(), "luatest", sdk.ResolveOptions{})
	require.ErrorIs(t, err, sdk.ErrNotFound)
	assert.Equal(t, []string{"luatest", "luatest"}, services.RemovedDependencies())

	manifest, err = project.Manifest()
	require.NoError(t, err)
	assert.Empty(t, manifest.DevDependencies)

	none := sdktest.New(t, sdktest.WithNoManifest())
	none.Start()

	_, err = none.Project().Manifest()
	require.ErrorIs(t, err, sdk.ErrNotFound)

	message := fatal(t, func(tb testing.TB) {
		tb.Helper()
		sdktest.New(tb, sdktest.WithNoManifest(), sdktest.WithManifest(sdk.Manifest{}))
	})
	assert.Contains(t, message, "WithNoManifest excludes")
}

// routerComponent is a component of the tests of AddComponent.
func routerComponent() sdk.Component {
	return sdk.Component{
		Name: "router", Comment: "A component.", Path: "router",
		Include: []string{"dist/router.wasm"},
		Build: &sdk.ComponentBuild{Backend: "shell", Fields: []sdk.BuildField{
			{Key: "command", Value: "tarawasm"}, {Key: "cwd", Value: "router/src"},
			{Key: "output", Value: "dist/router.wasm"},
			{Key: "env", Value: map[string]string{"PROFILE": "release"}},
			{Key: "incremental", Value: true},
			{Key: "args", Value: map[string][]string{"build": {"-opt=2"}}},
		}},
	}
}

// TestServicesProjectAddComponent checks that AddComponent records its
// calls, declares the component in the manifest, the fields of its build
// block read back as the core reads them, refuses what the core refuses,
// and resolves nothing.
func TestServicesProjectAddComponent(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithManifest(sdk.Manifest{Package: "shop"}))
	services.Start()

	project := services.Project()
	router := routerComponent()

	require.NoError(t, project.AddComponent(t.Context(), router))

	manifest, err := project.Manifest()
	require.NoError(t, err)
	assert.Equal(t, sdk.ManifestComponent{
		Path: "router", Include: []string{"dist/router.wasm"}, Namespace: "shop",
		Build: &sdk.ManifestBuild{
			Backend: "shell", Cwd: "router/src", Env: map[string]string{"PROFILE": "release"},
			Output:      []sdk.ManifestOutput{{Path: "dist/router.wasm", To: ""}},
			Incremental: true,
			Fields: map[string]any{
				"command": "tarawasm", "args": map[string]any{"build": []any{"-opt=2"}},
			},
		},
	}, manifest.Components["router"])

	for _, refused := range []sdk.Component{
		router,
		{Name: "", Path: "x"},
		{Name: "x", Path: ""},
		{Name: "x", Path: "x", Build: &sdk.ComponentBuild{Backend: ""}},
	} {
		require.Error(t, project.AddComponent(t.Context(), refused), refused.Name)
	}

	added := services.AddedComponents()
	require.Len(t, added, 5)
	assert.Equal(t, router, added[0])

	added[0].Build.Fields[0].Value = "changed"
	assert.Equal(t, "tarawasm", services.AddedComponents()[0].Build.Fields[0].Value,
		"the record is a copy")
	assert.Empty(t, services.Resolves(), "nothing is resolved")

	none := sdktest.New(t, sdktest.WithNoManifest())
	none.Start()

	require.ErrorIs(t, none.Project().AddComponent(t.Context(), router), sdk.ErrNotFound)
}

// TestServicesProjectUpdateComponent checks that UpdateComponent and
// RemoveComponent change the manifest as the core does, and refuse a
// component it does not declare.
func TestServicesProjectUpdateComponent(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	project := services.Project()

	require.NoError(t, project.AddComponent(t.Context(), routerComponent()))
	require.NoError(t, project.AddComponent(t.Context(), sdk.Component{Name: "app", Path: "app"}))

	update := sdk.ComponentUpdate{
		Name:   "router",
		Fields: []sdk.BuildField{{Key: "path", Value: "wasm/router"}},
		Build:  []sdk.BuildField{{Key: "output", Value: []string{"dist/a.wasm", "dist/b.wasm"}}},
		Remove: []string{"include", "build.incremental", "build.args"},
	}

	require.NoError(t, project.UpdateComponent(t.Context(), update))
	require.NoError(t, project.UpdateComponent(t.Context(), sdk.ComponentUpdate{
		Name: "app", Build: []sdk.BuildField{{Key: "backend", Value: "make"}},
	}))

	manifest, err := project.Manifest()
	require.NoError(t, err)

	router := manifest.Components["router"]
	assert.Equal(t, "wasm/router", router.Path)
	assert.Empty(t, router.Include)
	assert.False(t, router.Build.Incremental)
	assert.Equal(t, map[string]any{"command": "tarawasm"}, router.Build.Fields)
	assert.Equal(t, []sdk.ManifestOutput{{Path: "dist/a.wasm"}, {Path: "dist/b.wasm"}},
		router.Build.Output)
	assert.Equal(t, "make", manifest.Components["app"].Build.Backend)

	require.ErrorIs(t, project.UpdateComponent(t.Context(), sdk.ComponentUpdate{Name: "x"}),
		sdk.ErrNotFound)
	require.Error(t, project.UpdateComponent(t.Context(), sdk.ComponentUpdate{
		Name: "router", Remove: []string{"build"},
		Build: []sdk.BuildField{{Key: "cwd", Value: "."}},
	}), "a build block made anew names its backend")
	assert.Equal(t, update, services.UpdatedComponents()[0])

	require.NoError(t, project.RemoveComponent(t.Context(), "app"))
	require.ErrorIs(t, project.RemoveComponent(t.Context(), "app"), sdk.ErrNotFound)
	assert.Equal(t, []string{"app", "app"}, services.RemovedComponents())

	manifest, err = project.Manifest()
	require.NoError(t, err)
	assert.NotContains(t, manifest.Components, "app")
}

// TestServicesProjectResolve checks that Resolve records its calls and
// answers as WithMoves and WithResolveError say, and ReadLock as WithLock.
func TestServicesProjectResolve(t *testing.T) {
	t.Parallel()

	moves := []sdk.DependencyMove{{Name: "wasm", From: "", To: "0.1.0-1"}}
	lock := sdk.LockFile{
		Fresh: true, Reason: "", DefaultProduct: "default",
		Products: map[string]map[string]sdk.LockedRock{
			"default": {"wasm": {Version: "0.1.0-1", Path: ""}},
		},
		DevDependencies: map[string]sdk.LockedRock{"luatest": {Version: "1.0.1-1", Path: ""}},
	}

	services := sdktest.New(t, sdktest.WithMoves(moves...), sdktest.WithLock(lock))
	services.Start()

	change, err := services.Project().Resolve(t.Context(),
		sdk.ResolveOptions{Registries: []string{"mirror"}})
	require.NoError(t, err)
	assert.Equal(t, sdk.ResolveChange{Moves: moves}, change)
	assert.Equal(t, []sdk.ResolveOptions{{Registries: []string{"mirror"}}}, services.Resolves())

	read, err := services.Project().ReadLock()
	require.NoError(t, err)
	assert.Equal(t, lock, read)

	rock, ok := read.Locked("wasm")
	assert.True(t, ok)
	assert.Equal(t, "0.1.0-1", rock.Version)

	rock, ok = read.Locked("luatest")
	assert.True(t, ok)
	assert.Equal(t, "1.0.1-1", rock.Version)

	_, ok = read.Locked("checks")
	assert.False(t, ok)

	failure := errors.New("rock not found on any server")

	failing := sdktest.New(t, sdktest.WithResolveError(failure))
	failing.Start()

	_, err = failing.Project().Resolve(t.Context(), sdk.ResolveOptions{})
	require.ErrorIs(t, err, failure)

	_, err = failing.Project().ReadLock()
	require.ErrorIs(t, err, sdk.ErrNotFound)
}

// TestServicesProjectBuild checks that Build records its calls and runs what
// WithBuild gives, and BuildContext what WithBuildContext gives.
func TestServicesProjectBuild(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	result, err := services.Project().Build(t.Context(), sdk.BuildOptions{
		Components: []string{"calc"}, Args: []string{"-opt=2"},
	})
	require.NoError(t, err)
	assert.Equal(t, sdk.BuildResult{Product: "default"}, result)
	assert.Equal(t, []sdk.BuildOptions{{
		Components: []string{"calc"}, Args: []string{"-opt=2"},
	}}, services.Builds())

	_, _, err = services.Project().BuildContext(t.Context(),
		sdk.BuildContextOptions{Component: "calc"})
	require.ErrorIs(t, err, sdk.ErrNotFound)

	built := sdk.BuiltComponent{Data: []string{"/p/.rocks/share/tarantool/shop/calc.wasm"}}
	facts := &build.Context{Args: []string{"-opt=2"}}

	building := sdktest.New(t,
		sdktest.WithBuild(func(opts sdk.BuildOptions) (sdk.BuildResult, error) {
			return sdk.BuildResult{
				Product: "default", Version: "1.0.0",
				Components: map[string]sdk.BuiltComponent{opts.Components[0]: built},
			}, nil
		}),
		sdktest.WithBuildContext(func(opts sdk.BuildContextOptions) (
			*build.Context, build.Build, error,
		) {
			return facts, nil, nil
		}))
	building.Start()

	result, err = building.Project().Build(t.Context(),
		sdk.BuildOptions{Components: []string{"calc"}})
	require.NoError(t, err)
	assert.Equal(t, built, result.Components["calc"])

	got, _, err := building.Project().BuildContext(t.Context(),
		sdk.BuildContextOptions{Component: "calc"})
	require.NoError(t, err)
	assert.Same(t, facts, got)
}

// TestServicesProjectPrepareEnvironment checks that PrepareEnvironment
// counts its calls and writes nothing.
func TestServicesProjectPrepareEnvironment(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t)
	services.Start()

	assert.Zero(t, services.PreparedEnvironments())

	for range 2 {
		require.NoError(t, services.Project().PrepareEnvironment())
	}

	assert.Equal(t, 2, services.PreparedEnvironments())

	root, err := services.Project().Root()
	require.NoError(t, err)
	assert.NoDirExists(t, filepath.Join(root, ".env"))
}

// TestServicesRocksIndex checks that Index records its calls and runs what
// WithIndex gives.
func TestServicesRocksIndex(t *testing.T) {
	t.Parallel()

	plain := sdktest.New(t)
	plain.Start()

	require.NoError(t, plain.Rocks().Index(t.Context(), "dist/rocks", sdk.IndexOptions{}))
	assert.Equal(t, []sdktest.Index{{Dir: "dist/rocks"}}, plain.Indexed())

	failure := errors.New("not a rock")
	registry := sdk.IndexOptions{IfIndexed: true}

	services := sdktest.New(t, sdktest.WithIndex(func(dir string, opts sdk.IndexOptions) error {
		if opts.IfIndexed {
			return failure
		}

		return os.WriteFile(filepath.Join(dir, "manifest"), []byte("repository = {}\n"), 0o600)
	}))
	services.Start()

	dir := t.TempDir()

	require.NoError(t, services.Rocks().Index(t.Context(), dir, sdk.IndexOptions{}))
	assert.FileExists(t, filepath.Join(dir, "manifest"))

	err := services.Rocks().Index(t.Context(), dir, registry)
	require.ErrorIs(t, err, failure)

	assert.Equal(t, []sdktest.Index{{Dir: dir}, {Dir: dir, Options: registry}},
		services.Indexed())
}

func TestServicesStreams(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithStdin(strings.NewReader("input")))
	services.Start()

	streams := services.Streams()

	input, err := io.ReadAll(streams.IO().In)
	require.NoError(t, err)
	assert.Equal(t, "input", string(input))

	_, err = io.WriteString(streams.IO().Err, "note\n")
	require.NoError(t, err)

	printer, err := streams.Printer(output.FormatJSON)
	require.NoError(t, err)
	require.NoError(t, printer.Emit(result{Name: "x"}))

	assert.JSONEq(t, `{"name": "x"}`, services.Stdout())
	assert.Equal(t, "note\n", services.Stderr())

	_, err = streams.Printer(output.FormatYAML)
	require.ErrorIs(t, err, output.ErrUnknownFormat)

	humanServices := sdktest.New(t)
	humanServices.Start()

	human, err := humanServices.Streams().Printer(output.FormatHuman)
	require.NoError(t, err)
	require.NoError(t, human.Emit(result{Name: "y"}))
	assert.Equal(t, "name y\n", humanServices.Stdout())
}

func TestServicesLog(t *testing.T) {
	t.Parallel()

	services := sdktest.New(t, sdktest.WithName("mymod"))
	services.Log().Info("hello", "key", "value")

	records := services.Records()
	require.Len(t, records, 1)
	assert.Equal(t, "hello", records[0].Message)
	assert.Equal(t, "mymod", records[0].Map()["module"])
	assert.Equal(t, "value", records[0].Map()["key"])

	defaultName := sdktest.New(t)
	defaultName.Log().Info("hello")
	assert.Equal(t, "test", defaultName.Records()[0].Map()["module"])
}

// result is a command result for the printer tests.
type result struct {
	Name string `json:"name"`
}

// Human writes the result for people.
func (r result) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "name %s\n", r.Name)

	return err
}
