// Package rockspec implements the sandboxed gopher-lua evaluator for
// .rockspec source files (this is the ONLY package permitted to import
// gopher-lua). The output of Eval is a plain-data *rocks.Rockspec; downstream
// packages (fetch, build, deps, manif) consume that struct without touching
// Lua at all.
//
// Sandbox:
//
//   - The evaluator opens base/string/table/math only. io, package, debug,
//     channel, coroutine, and the full os library are NOT opened.
//   - A custom os table is installed with just three functions: getenv,
//     time, date. getenv routes through buildGetenv (configurable per
//     RockspecConfig.Env).
//   - Base-library doors to the host — require, load, loadfile, dofile,
//     loadstring, module — are removed, together with the metatable/raw/
//     environment primitives (setmetatable, getmetatable, rawset, rawget,
//     rawequal, rawlen, setfenv, getfenv, newproxy, collectgarbage) and the
//     package/io/debug/coroutine globals. (print is intentionally left
//     reachable — it is harmless and aids debugging rockspecs.)
//
// Tests in eval_test.go assert that each forbidden global is unreachable
// from rockspec code.
package rockspec

import (
	"fmt"
	"os"
	"strings"
	"time"

	lua "github.com/yuin/gopher-lua"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// Eval reads `path` as a `.rockspec` file, executes it inside a sandboxed
// gopher-lua VM, and harvests known globals into a *rocks.Rockspec.
//
// The returned spec contains Build.Platforms populated as-declared; the
// caller folds them into Build via MergePlatforms (3.4) when running for a
// specific OS. Eval does NOT call MergePlatforms itself — keeping the two
// stages distinct lets the same parsed spec drive both the host build and
// e.g. `download` flows that want to inspect platform overlays.
//
// Returns ErrUnsupportedRockspecFeature if build.type is outside the
// {builtin, cmake, make, command, none} set.
func Eval(path string, cfg rocks.RockspecConfig) (*rocks.Rockspec, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("rockspec: read %s: %w", path, err)
	}

	L := newSandboxedState(cfg)
	defer L.Close()

	if err := L.DoString(string(src)); err != nil {
		return nil, fmt.Errorf("rockspec: eval %s: %w", path, err)
	}

	spec := &rocks.Rockspec{}
	if err := harvest(L, spec); err != nil {
		return nil, fmt.Errorf("rockspec: harvest %s: %w", path, err)
	}

	return spec, nil
}

// newSandboxedState builds a fresh LState with only the safe libs installed
// and the rockspec-allowed os subset wired in.
func newSandboxedState(cfg rocks.RockspecConfig) *lua.LState {
	L := lua.NewState(lua.Options{SkipOpenLibs: true})
	// Whitelist: base, string, table, math. Each entry pushes the loader as
	// a function and calls it with the lib name as Lua's stdlib loaders
	// expect (mirrors LState.OpenLibs but trimmed).
	for _, lib := range []struct {
		name string
		fn   lua.LGFunction
	}{
		{"", lua.OpenBase},
		{lua.TabLibName, lua.OpenTable},
		{lua.StringLibName, lua.OpenString},
		{lua.MathLibName, lua.OpenMath},
	} {
		L.Push(L.NewFunction(lib.fn))
		L.Push(lua.LString(lib.name))
		L.Call(1, 0)
	}

	// Lock down the base-library doors to the host. Setting these to nil
	// turns `require(...)` and `loadfile(...)` into "attempt to call a nil
	// value" — a loud, recognisable Lua runtime error.
	//
	// Two groups blocked:
	//   - Host-reach: load*, require, dofile, io, package, debug, coroutine.
	//   - Environment manipulation: setfenv/getfenv re-wire the running
	//     thread's globals; raw* / setmetatable / getmetatable / collectgarbage
	//     can stage similar shadowing. The reach to the Go host is nil
	//     (gopher-lua confines them to Lua-side state), but a rockspec doing
	//     `setfenv(0, t)` could mutate what the harvester later reads via
	//     L.GetGlobal — defense-in-depth for the eval pass is cheap.
	for _, name := range []string{
		"require", "load", "loadfile", "dofile", "loadstring",
		"module", "package", "io", "debug", "coroutine",
		"newproxy",
		"setfenv", "getfenv",
		"rawset", "rawget", "rawequal", "rawlen",
		"setmetatable", "getmetatable",
		"collectgarbage",
	} {
		L.SetGlobal(name, lua.LNil)
	}

	// Install the tightly-scoped os table: only getenv, time, date.
	osTab := L.NewTable()
	osTab.RawSetString("getenv", L.NewFunction(buildGetenv(cfg.Env)))
	osTab.RawSetString("time", L.NewFunction(osTime))
	osTab.RawSetString("date", L.NewFunction(osDate))
	L.SetGlobal("os", osTab)

	return L
}

// osTime is a pass-through to time.Now().Unix(). gopher-lua's Lua 5.1
// `os.time()` would accept a date table; we don't — pass no arguments.
func osTime(l *lua.LState) int {
	l.Push(lua.LNumber(time.Now().Unix()))

	return 1
}

// osDate implements `os.date(format, [time])` for format strings only. The
// upstream Lua 5.1 contract supports a "*t" form that returns a table of
// fields; we intentionally omit it because the rockspec format almost never
// uses it and broader support would expand the sandbox surface for no gain.
// Lua argument positions for os.date(format, [time]).
const (
	osDateFormatArg = 1
	osDateTimeArg   = 2
)

func osDate(l *lua.LState) int {
	format := "%c"
	if l.GetTop() >= osDateFormatArg {
		format = l.CheckString(osDateFormatArg)
	}

	t := time.Now()
	if l.GetTop() >= osDateTimeArg {
		t = time.Unix(int64(l.CheckNumber(osDateTimeArg)), 0)
	}

	utc := false
	if strings.HasPrefix(format, "!") {
		utc = true
		format = format[1:]
	}

	if utc {
		t = t.UTC()
	}

	l.Push(lua.LString(strftime(format, t)))

	return 1
}

// strftime is a tiny strftime-equivalent covering the directives rockspecs
// realistically use. Unknown directives are passed through verbatim — this
// is conservative; rockspecs in practice only ever feed os.date a literal
// year/date for a placeholder, and we'd rather emit a recognisable token
// than silently drop characters.
// yearModulo reduces a four-digit year to its two-digit form for %y.
const yearModulo = 100

func strftime(format string, t time.Time) string {
	var b strings.Builder

	for i := 0; i < len(format); i++ {
		if format[i] != '%' || i+1 >= len(format) {
			b.WriteByte(format[i])

			continue
		}

		i++
		switch format[i] {
		case 'Y':
			fmt.Fprintf(&b, "%04d", t.Year())
		case 'm':
			fmt.Fprintf(&b, "%02d", int(t.Month()))
		case 'd':
			fmt.Fprintf(&b, "%02d", t.Day())
		case 'H':
			fmt.Fprintf(&b, "%02d", t.Hour())
		case 'M':
			fmt.Fprintf(&b, "%02d", t.Minute())
		case 'S':
			fmt.Fprintf(&b, "%02d", t.Second())
		case 'y':
			fmt.Fprintf(&b, "%02d", t.Year()%yearModulo)
		case 'c':
			b.WriteString(t.Format("Mon Jan  2 15:04:05 2006"))
		case '%':
			b.WriteByte('%')
		default:
			b.WriteByte('%')
			b.WriteByte(format[i])
		}
	}

	return b.String()
}

// ---- harvesting ----

func harvest(l *lua.LState, spec *rocks.Rockspec) error {
	spec.RockspecFormat = optString(l.GetGlobal("rockspec_format"))
	spec.Package = optString(l.GetGlobal("package"))
	spec.Version = optString(l.GetGlobal("version"))

	if tbl, ok := l.GetGlobal("description").(*lua.LTable); ok {
		spec.Description = harvestDescription(tbl)
	}

	if tbl, ok := l.GetGlobal("source").(*lua.LTable); ok {
		spec.Source = harvestSource(tbl)
	}

	if tbl, ok := l.GetGlobal("dependencies").(*lua.LTable); ok {
		spec.Dependencies = harvestDeps(tbl)
	}

	if tbl, ok := l.GetGlobal("build_dependencies").(*lua.LTable); ok {
		spec.BuildDependencies = harvestDeps(tbl)
	}

	if tbl, ok := l.GetGlobal("test_dependencies").(*lua.LTable); ok {
		spec.TestDependencies = harvestDeps(tbl)
	}

	if tbl, ok := l.GetGlobal("external_dependencies").(*lua.LTable); ok {
		spec.ExternalDependencies = harvestExternalDeps(tbl)
	}

	if tbl, ok := l.GetGlobal("supported_platforms").(*lua.LTable); ok {
		spec.SupportedPlatforms = harvestStringArray(tbl)
	}

	if tbl, ok := l.GetGlobal("build").(*lua.LTable); ok {
		b, err := harvestBuild(tbl)
		if err != nil {
			return err
		}

		spec.Build = b
	}

	return nil
}

func optString(v lua.LValue) string {
	if s, ok := v.(lua.LString); ok {
		return string(s)
	}

	return ""
}

func harvestDescription(tbl *lua.LTable) rocks.Description {
	return rocks.Description{
		Summary:    optString(tbl.RawGetString("summary")),
		Detailed:   optString(tbl.RawGetString("detailed")),
		License:    optString(tbl.RawGetString("license")),
		Homepage:   optString(tbl.RawGetString("homepage")),
		IssuesURL:  optString(tbl.RawGetString("issues_url")),
		Maintainer: optString(tbl.RawGetString("maintainer")),
		Labels:     stringArray(tbl.RawGetString("labels")),
	}
}

func harvestSource(tbl *lua.LTable) rocks.Source {
	return rocks.Source{
		URL:    optString(tbl.RawGetString("url")),
		Tag:    optString(tbl.RawGetString("tag")),
		Branch: optString(tbl.RawGetString("branch")),
		MD5:    optString(tbl.RawGetString("md5")),
		File:   optString(tbl.RawGetString("file")),
		Dir:    optString(tbl.RawGetString("dir")),
		Module: optString(tbl.RawGetString("module")),
	}
}

// harvestDeps walks the array part of a dependencies-table and parses each
// dependency string into Name + raw constraint operators.
//
// Constraint parsing here is intentionally minimal: it splits on commas and
// then on whitespace to extract op+version pairs, leaving Version.Components
// empty. The version-string parser in deps/version.go refines this further.
func harvestDeps(tbl *lua.LTable) []rocks.Dep {
	if tbl == nil {
		return nil
	}

	var deps []rocks.Dep

	tbl.ForEach(func(k, v lua.LValue) {
		if _, ok := k.(lua.LNumber); !ok {
			return
		}

		s, ok := v.(lua.LString)
		if !ok {
			return
		}

		if d, ok := parseDepString(string(s)); ok {
			deps = append(deps, d)
		}
	})

	return deps
}

// parseDepString returns the Dep for a string like "foo >= 1.0, < 2.0".
//
// On a malformed input (no leading identifier) it returns ok=false rather
// than fabricating a name — fail loud, but ForEach is best-effort over
// table data so the caller drops the entry. (Validation surfaces missing
// dependencies via Validate later.)
func parseDepString(s string) (rocks.Dep, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return rocks.Dep{}, false
	}

	parts := strings.Split(s, ",")
	first := strings.TrimSpace(parts[0])
	// Identifier is everything up to the first whitespace.
	name := first
	rest := ""

	if i := strings.IndexAny(first, " \t"); i >= 0 {
		name = first[:i]
		rest = strings.TrimSpace(first[i:])
	}

	if name == "" {
		return rocks.Dep{}, false
	}

	dep := rocks.Dep{Name: name}

	if rest != "" {
		if c, ok := parseConstraint(rest); ok {
			dep.Constraints = append(dep.Constraints, c)
		}
	}

	for _, more := range parts[1:] {
		more = strings.TrimSpace(more)
		if more == "" {
			continue
		}

		if c, ok := parseConstraint(more); ok {
			dep.Constraints = append(dep.Constraints, c)
		}
	}

	return dep, true
}

// parseConstraint extracts op + raw version from one comma-segment of a
// dependency string. Unknown op → ok=false, caller drops the segment.
func parseConstraint(s string) (rocks.VersionConstraint, bool) {
	s = strings.TrimSpace(s)

	ops := []string{">=", "<=", "==", "~=", "~>", ">", "<"}
	for _, op := range ops {
		if strings.HasPrefix(s, op) {
			rest := strings.TrimSpace(s[len(op):])

			return rocks.VersionConstraint{
				Op:      op,
				Version: rocks.Version{Raw: rest},
			}, true
		}
	}
	// No operator → treat as implicit ==.
	if s != "" {
		return rocks.VersionConstraint{
			Op:      "==",
			Version: rocks.Version{Raw: s},
		}, true
	}

	return rocks.VersionConstraint{}, false
}

func harvestExternalDeps(tbl *lua.LTable) map[string]rocks.ExternalDep {
	if tbl == nil {
		return nil
	}

	out := map[string]rocks.ExternalDep{}

	tbl.ForEach(func(k, v lua.LValue) {
		name, ok := k.(lua.LString)
		if !ok {
			return
		}

		sub, ok := v.(*lua.LTable)
		if !ok {
			return
		}

		out[string(name)] = rocks.ExternalDep{
			Header:  optString(sub.RawGetString("header")),
			Library: optString(sub.RawGetString("library")),
		}
	})

	return out
}

func harvestStringArray(tbl *lua.LTable) []string {
	return stringArray(tbl)
}

func stringArray(v lua.LValue) []string {
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}

	out := make([]string, 0, tbl.Len())
	for i := 1; i <= tbl.Len(); i++ {
		if s, ok := tbl.RawGetInt(i).(lua.LString); ok {
			out = append(out, string(s))
		}
	}

	return out
}

func stringMap(v lua.LValue) map[string]string {
	tbl, ok := v.(*lua.LTable)
	if !ok {
		return nil
	}

	out := map[string]string{}

	tbl.ForEach(func(k, vv lua.LValue) {
		ks, kok := k.(lua.LString)

		vs, vok := vv.(lua.LString)
		if !kok || !vok {
			return
		}

		out[string(ks)] = string(vs)
	})

	if len(out) == 0 {
		return nil
	}

	return out
}

func harvestBuild(tbl *lua.LTable) (rocks.Build, error) {
	b := rocks.Build{
		Type:             optString(tbl.RawGetString("type")),
		BuildTarget:      optString(tbl.RawGetString("build_target")),
		InstallTarget:    optString(tbl.RawGetString("install_target")),
		BuildCommand:     optString(tbl.RawGetString("build_command")),
		InstallCommand:   optString(tbl.RawGetString("install_command")),
		Variables:        stringMap(tbl.RawGetString("variables")),
		BuildVariables:   stringMap(tbl.RawGetString("build_variables")),
		InstallVariables: stringMap(tbl.RawGetString("install_variables")),
		CopyDirectories:  stringArray(tbl.RawGetString("copy_directories")),
	}

	if !allowedBuildTypes[b.Type] {
		return rocks.Build{}, fmt.Errorf("%w: build.type=%q",
			rocks.ErrUnsupportedRockspecFeature, b.Type)
	}

	if modsTbl, ok := tbl.RawGetString("modules").(*lua.LTable); ok {
		b.Modules = harvestModules(modsTbl)
	}

	if installTbl, ok := tbl.RawGetString("install").(*lua.LTable); ok {
		b.Install = harvestBuildInstall(installTbl)
	}

	if platsTbl, ok := tbl.RawGetString("platforms").(*lua.LTable); ok {
		plats, err := harvestPlatforms(platsTbl)
		if err != nil {
			return rocks.Build{}, err
		}

		if len(plats) > 0 {
			b.Platforms = plats
		}
	}

	return b, nil
}

// harvestPlatforms recurses into a build.platforms table, returning one
// rocks.Build per named platform overlay.
func harvestPlatforms(platsTbl *lua.LTable) (map[string]rocks.Build, error) {
	plats := map[string]rocks.Build{}

	var harvestErr error

	platsTbl.ForEach(func(k, v lua.LValue) {
		if harvestErr != nil {
			return
		}

		name, ok := k.(lua.LString)
		if !ok {
			return
		}

		sub, ok := v.(*lua.LTable)
		if !ok {
			return
		}

		pb, err := harvestBuild(sub)
		if err != nil {
			harvestErr = err

			return
		}

		plats[string(name)] = pb
	})

	if harvestErr != nil {
		return nil, harvestErr
	}

	return plats, nil
}

func harvestModules(tbl *lua.LTable) map[string]rocks.Module {
	out := map[string]rocks.Module{}

	tbl.ForEach(func(k, v lua.LValue) {
		name, ok := k.(lua.LString)
		if !ok {
			return
		}

		switch vv := v.(type) {
		case lua.LString:
			out[string(name)] = rocks.Module{Path: string(vv)}
		case *lua.LTable:
			m := rocks.Module{}
			// String shorthand for sources: `{"src/foo.c"}` (array form).
			if arr := stringArray(vv); len(arr) > 0 && vv.RawGetString("sources") == lua.LNil {
				m.Sources = arr
			}

			if s := stringArray(vv.RawGetString("sources")); len(s) > 0 {
				m.Sources = s
			}

			m.Incdirs = stringArray(vv.RawGetString("incdirs"))
			m.Libdirs = stringArray(vv.RawGetString("libdirs"))
			m.Libraries = stringArray(vv.RawGetString("libraries"))
			m.Defines = stringArray(vv.RawGetString("defines"))
			out[string(name)] = m
		}
	})

	if len(out) == 0 {
		return nil
	}

	return out
}

func harvestBuildInstall(tbl *lua.LTable) rocks.BuildInstall {
	return rocks.BuildInstall{
		Lua:  stringMap(tbl.RawGetString("lua")),
		Lib:  stringMap(tbl.RawGetString("lib")),
		Bin:  stringMap(tbl.RawGetString("bin")),
		Conf: stringMap(tbl.RawGetString("conf")),
	}
}
