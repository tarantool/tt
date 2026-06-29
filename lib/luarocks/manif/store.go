package manif

import (
	"errors"
	"fmt"
	"os"
	"path"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// FileStore implements the module-level ManifestStore interface against the
// local filesystem.
//
// Path conventions:
//
//   - ReadTree / WriteTree accept a tree-root directory; the manifest file
//     lives at `<tree>/manifest`. For Tarantool the rocks_dir is
//     `<tree-root>/share/tarantool/rocks/`, and `path` here is that
//     rocks_dir — i.e. the same directory that `path.rocks_dir(tree)`
//     returns upstream.
//
//   - ReadRock / WriteRock take the absolute path to the rock_manifest file
//     itself (`<rocks_dir>/<name>/<version>/rock_manifest`). Computing that
//     path is the caller's responsibility — keeps the interface minimal
//     and frees FileStore from owning tree-layout knowledge.
//
// FileStore is a value type with no state.
type FileStore struct{}

// manifestDirMode is the permission applied to directories created while
// writing a manifest: owner rwx, group rx, no world access.
const manifestDirMode = 0o750

// Static check that FileStore implements the module-root interface.
var _ rocks.ManifestStore = FileStore{}

// ReadTreeManifest is the package-level shortcut for FileStore.ReadTree.
// It exists so callers (notably tt pack's LuaGetRocksVersions replacement)
// can obtain a *rocks.Manifest without spelling FileStore out.
//
// This deliberately lives in the manif package rather than at the module
// root: the root rocks package cannot import manif without creating an
// import cycle (manif already imports root for the Manifest type).
func ReadTreeManifest(treePath string) (*rocks.Manifest, error) {
	return FileStore{}.ReadTree(treePath)
}

// ReadTree reads and decodes the tree-level manifest at `<path>/manifest`.
//
// The decoded shape is converted into a *rocks.Manifest. Fields that the
// in-memory type does not model (e.g. each repository entry's `modules` and
// `commands` sub-tables, or the `dependencies` constraint trees) are
// preserved enough to populate the Manifest projection needed by callers.
func (FileStore) ReadTree(treePath string) (*rocks.Manifest, error) {
	data, err := os.ReadFile(path.Join(treePath, "manifest"))
	if err != nil {
		return nil, fmt.Errorf("manif.FileStore.ReadTree: %w", err)
	}

	v, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("manif.FileStore.ReadTree: %w", err)
	}

	root, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("manif.FileStore.ReadTree: top-level value is %T, want map", v)
	}

	return manifestFromTable(root)
}

// WriteTree encodes m and writes it atomically to `<path>/manifest`.
func (FileStore) WriteTree(treePath string, m *rocks.Manifest) error {
	if m == nil {
		return errors.New("manif.FileStore.WriteTree: nil manifest")
	}

	tbl := manifestToTable(m)
	dst := path.Join(treePath, "manifest")

	return writeAtomic(dst, tbl)
}

// ReadRock reads a rock_manifest file at the given absolute path.
//
// Per persist `save_as_module`, rock_manifest files start with
// `return {\n ... }\n`. Parse is for assignments-mode files, so ReadRock
// strips the `return ` prefix and trailing newlines before delegating to
// the parser via a synthetic `rock_manifest = ...` wrapper.
func (FileStore) ReadRock(filePath string) (*rocks.RockManifest, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("manif.FileStore.ReadRock: %w", err)
	}
	// rock_manifest is either:
	//   - module-mode (`return { ... }`) when written by save_as_module, or
	//   - assignments-mode (`rock_manifest = { ... }`) which is what
	//     writer.lua actually emits via persist.save_from_table.
	// Upstream's `make_rock_manifest` (manif/writer.lua) wraps the data in
	// `{ rock_manifest = tree }` and calls save_from_table — so the on-disk
	// file is assignments-mode with a single `rock_manifest = { ... }` key.
	v, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("manif.FileStore.ReadRock: %w", err)
	}

	top, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("manif.FileStore.ReadRock: top-level is %T, want map", v)
	}

	inner, ok := top["rock_manifest"]
	if !ok {
		return nil, errors.New("manif.FileStore.ReadRock: missing rock_manifest key")
	}

	innerMap, ok := inner.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("manif.FileStore.ReadRock: rock_manifest is %T, want map", inner)
	}

	return rockManifestFromTable(innerMap)
}

// WriteRock encodes rm and writes it atomically to filePath.
func (FileStore) WriteRock(filePath string, rm *rocks.RockManifest) error {
	if rm == nil {
		return errors.New("manif.FileStore.WriteRock: nil rock manifest")
	}

	tbl := map[string]any{
		"rock_manifest": rockManifestToTable(rm),
	}

	return writeAtomic(filePath, tbl)
}

// writeAtomic serializes tbl, writes it to filePath.tmp, and renames into
// place. Matches upstream `save_table`'s tmp+rename atomicity.
func writeAtomic(filePath string, tbl any) error {
	dir := path.Dir(filePath)
	if err := os.MkdirAll(dir, manifestDirMode); err != nil {
		return fmt.Errorf("manif: mkdir %q: %w", dir, err)
	}

	tmp := filePath + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("manif: create %q: %w", tmp, err)
	}

	if err := Write(f, tbl); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)

		return err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("manif: close %q: %w", tmp, err)
	}

	if err := os.Rename(tmp, filePath); err != nil {
		_ = os.Remove(tmp)

		return fmt.Errorf("manif: rename %q -> %q: %w", tmp, filePath, err)
	}

	return nil
}

// manifestToTable projects a *rocks.Manifest into the table shape persist
// expects. Empty sub-maps render as `{}` (Lua's standard empty-table form),
// matching upstream output for a freshly-initialized tree.
func manifestToTable(m *rocks.Manifest) map[string]any {
	out := map[string]any{}

	cmd := map[string]any{}

	for k, v := range m.Commands {
		arr := make([]any, len(v))
		for i, s := range v {
			arr[i] = s
		}

		cmd[k] = arr
	}

	out["commands"] = cmd

	mod := map[string]any{}

	for k, v := range m.Modules {
		arr := make([]any, len(v))
		for i, s := range v {
			arr[i] = s
		}

		mod[k] = arr
	}

	out["modules"] = mod

	repo := map[string]any{}

	for pkg, versions := range m.Repository {
		verMap := map[string]any{}

		for ver, entry := range versions {
			ae := map[string]any{
				"arch": entry.Arch,
			}

			if len(entry.Modules) > 0 {
				modSub := map[string]any{}
				for k, v := range entry.Modules {
					modSub[k] = v
				}

				ae["modules"] = modSub
			}

			if len(entry.Commands) > 0 {
				cmdSub := map[string]any{}
				for k, v := range entry.Commands {
					cmdSub[k] = v
				}

				ae["commands"] = cmdSub
			}

			verMap[ver] = []any{ae}
		}

		repo[pkg] = verMap
	}

	out["repository"] = repo

	if len(m.Dependencies) > 0 {
		deps := map[string]any{}

		for pkg, vers := range m.Dependencies {
			verMap := map[string]any{}

			for ver, list := range vers {
				arr := make([]any, len(list))

				for i, d := range list {
					depTbl := map[string]any{"name": d.Name}

					if len(d.Constraints) > 0 {
						cs := make([]any, len(d.Constraints))
						for j, c := range d.Constraints {
							cs[j] = map[string]any{
								"op":      c.Op,
								"version": c.Version.Raw,
							}
						}

						depTbl["constraints"] = cs
					}

					arr[i] = depTbl
				}

				verMap[ver] = arr
			}

			deps[pkg] = verMap
		}

		out["dependencies"] = deps
	}

	return out
}

// manifestFromTable is the inverse of manifestToTable. It is intentionally
// tolerant of the richer upstream shape (e.g. arch tables with `modules`
// and `commands` sub-fields) by ignoring fields the in-memory type does
// not model — but it does NOT silently invent data when required fields
// are missing.
func manifestFromTable(t map[string]any) (*rocks.Manifest, error) {
	m := &rocks.Manifest{
		Repository:   map[string]map[string]rocks.RepoEntry{},
		Modules:      map[string][]string{},
		Commands:     map[string][]string{},
		Dependencies: map[string]map[string][]rocks.Dep{},
	}

	if v, ok := t["modules"]; ok {
		err := loadStringListMap(v, m.Modules, "modules")
		if err != nil {
			return nil, err
		}
	}

	if v, ok := t["commands"]; ok {
		err := loadStringListMap(v, m.Commands, "commands")
		if err != nil {
			return nil, err
		}
	}

	if v, ok := t["repository"]; ok {
		repoMap, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("manif.ReadTree: repository is %T, want map", v)
		}

		for pkg, vAny := range repoMap {
			verMap, ok := vAny.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("manif.ReadTree: repository.%s is %T, want map", pkg, vAny)
			}

			inner := map[string]rocks.RepoEntry{}

			for ver, vv := range verMap {
				entry, err := extractRepoEntry(vv)
				if err != nil {
					return nil, fmt.Errorf("manif.ReadTree: repository.%s.%s: %w", pkg, ver, err)
				}

				inner[ver] = entry
			}

			m.Repository[pkg] = inner
		}
	}

	if v, ok := t["dependencies"]; ok {
		depMap, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("manif.ReadTree: dependencies is %T, want map", v)
		}

		for pkg, vAny := range depMap {
			verMap, ok := vAny.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("manif.ReadTree: dependencies.%s is %T, want map", pkg, vAny)
			}

			inner := map[string][]rocks.Dep{}

			for ver, vv := range verMap {
				deps, err := loadDepList(vv)
				if err != nil {
					return nil, fmt.Errorf("manif.ReadTree: dependencies.%s.%s: %w", pkg, ver, err)
				}

				inner[ver] = deps
			}

			m.Dependencies[pkg] = inner
		}
	}

	return m, nil
}

func loadStringListMap(v any, dst map[string][]string, name string) error {
	tm, ok := v.(map[string]any)
	if !ok {
		return fmt.Errorf("manif.ReadTree: %s is %T, want map", name, v)
	}

	for k, raw := range tm {
		arr, ok := raw.([]any)
		if !ok {
			return fmt.Errorf("manif.ReadTree: %s.%s is %T, want array", name, k, raw)
		}

		lst := make([]string, len(arr))

		for i, e := range arr {
			s, ok := e.(string)
			if !ok {
				return fmt.Errorf("manif.ReadTree: %s.%s[%d] is %T, want string", name, k, i, e)
			}

			lst[i] = s
		}

		dst[k] = lst
	}

	return nil
}

func extractRepoEntry(v any) (rocks.RepoEntry, error) {
	// repository[pkg][ver] is an array of one or more arch entries.
	arr, ok := v.([]any)
	if !ok {
		return rocks.RepoEntry{}, fmt.Errorf("expected array of arch entries, got %T", v)
	}

	if len(arr) == 0 {
		return rocks.RepoEntry{}, errors.New("empty arch array")
	}

	entry, ok := arr[0].(map[string]any)
	if !ok {
		return rocks.RepoEntry{}, fmt.Errorf("first arch entry is %T, want map", arr[0])
	}

	arch, ok := entry["arch"].(string)
	if !ok {
		return rocks.RepoEntry{}, errors.New("arch entry missing 'arch' string")
	}

	re := rocks.RepoEntry{Arch: arch}
	if mods, ok := entry["modules"].(map[string]any); ok && len(mods) > 0 {
		re.Modules = map[string]string{}

		for k, v := range mods {
			s, ok := v.(string)
			if !ok {
				return rocks.RepoEntry{}, fmt.Errorf("modules.%s is %T, want string", k, v)
			}

			re.Modules[k] = s
		}
	}

	if cmds, ok := entry["commands"].(map[string]any); ok && len(cmds) > 0 {
		re.Commands = map[string]string{}

		for k, v := range cmds {
			s, ok := v.(string)
			if !ok {
				return rocks.RepoEntry{}, fmt.Errorf("commands.%s is %T, want string", k, v)
			}

			re.Commands[k] = s
		}
	}

	return re, nil
}

func loadDepList(v any) ([]rocks.Dep, error) {
	arr, ok := v.([]any)
	if !ok {
		// Upstream LuaRocks writes a rock's dependency list as a Lua table.
		// When the rock has no dependencies the list is the empty table `{}`,
		// which decodes to an empty map (Lua can't distinguish an empty array
		// from an empty map). Treat that as "no dependencies" so the native
		// reader can load manifests written by the gopher-lua backend.
		if m, isMap := v.(map[string]any); isMap && len(m) == 0 {
			return nil, nil
		}

		return nil, fmt.Errorf("expected dep array, got %T", v)
	}

	out := make([]rocks.Dep, len(arr))

	for i, raw := range arr {
		m, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("dep entry %d is %T, want map", i, raw)
		}

		name, _ := m["name"].(string)
		d := rocks.Dep{Name: name}

		if cs, ok := m["constraints"].([]any); ok {
			for _, c := range cs {
				cm, ok := c.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("constraint is %T, want map", c)
				}

				op, _ := cm["op"].(string)
				ver, _ := cm["version"].(string)
				d.Constraints = append(d.Constraints, rocks.VersionConstraint{
					Op:      op,
					Version: rocks.Version{Raw: ver},
				})
			}
		}

		out[i] = d
	}

	return out, nil
}

// rockManifestToTable lays out a RockManifest into the nested
// per-directory shape persist expects.
func rockManifestToTable(rm *rocks.RockManifest) map[string]any {
	out := map[string]any{}
	if rm.Rockspec != "" {
		out["rockspec"] = rm.Rockspec
	}

	addDir := func(name string, m map[string]string) {
		if len(m) == 0 {
			return
		}

		out[name] = stringMapToNested(m)
	}
	addDir("lua", rm.Lua)
	addDir("lib", rm.Lib)
	addDir("bin", rm.Bin)
	addDir("conf", rm.Conf)
	addDir("doc", rm.Doc)

	return out
}

// stringMapToNested folds a map of slash-separated paths into a nested
// tree, mirroring upstream `make_rock_manifest` (manif/writer.lua:256-289).
func stringMapToNested(m map[string]string) map[string]any {
	root := map[string]any{}

	for full, md5 := range m {
		parts := strings.Split(full, "/")
		cur := root

		for i, p := range parts {
			if i == len(parts)-1 {
				cur[p] = md5

				break
			}

			next, ok := cur[p].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[p] = next
			}

			cur = next
		}
	}

	return root
}

func rockManifestFromTable(t map[string]any) (*rocks.RockManifest, error) {
	rm := &rocks.RockManifest{
		Lua:  map[string]string{},
		Lib:  map[string]string{},
		Bin:  map[string]string{},
		Conf: map[string]string{},
		Doc:  map[string]string{},
	}

	if v, ok := t["rockspec"]; ok {
		s, ok := v.(string)
		if !ok {
			return nil, fmt.Errorf("rock_manifest.rockspec is %T, want string", v)
		}

		rm.Rockspec = s
	}

	for key, dst := range map[string]map[string]string{
		"lua": rm.Lua, "lib": rm.Lib, "bin": rm.Bin, "conf": rm.Conf, "doc": rm.Doc,
	} {
		if v, ok := t[key]; ok {
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("rock_manifest.%s is %T, want map", key, v)
			}

			flattenNested("", m, dst)
		}
	}

	return rm, nil
}

func flattenNested(prefix string, m map[string]any, dst map[string]string) {
	for k, v := range m {
		full := k
		if prefix != "" {
			full = prefix + "/" + k
		}

		switch x := v.(type) {
		case string:
			dst[full] = x
		case map[string]any:
			flattenNested(full, x, dst)
		default:
			// Numeric keys etc. should not appear; the only producer
			// (Lua persist) only emits string-keyed trees at this level.
			// Encode unexpected leaves as a sentinel stringification so
			// we don't lose data — and surface it for inspection rather
			// than dropping.
			dst[full] = fmt.Sprintf("%v", x)
		}
	}
}
