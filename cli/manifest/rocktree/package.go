package rocktree

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/go-luarocks/rockspec"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// The directories of a stage laid out as an unpacked binary rock, and where
// the tree deploys each of them.
const (
	// StageLua holds the Lua modules and every other file a component lays out
	// but a native module, deployed under share/tarantool/.
	StageLua = "lua"
	// StageLib holds the native modules, deployed under lib/tarantool/.
	StageLib = "lib"
	// StageBin holds the programs of executable components, deployed under
	// bin/ as they are.
	StageBin = "bin"

	shareTarantool = "share/tarantool"
	libTarantool   = "lib/tarantool"
	binDir         = "bin"
	// rockRevision is the LuaRocks revision of every package rock: tt makes
	// the rock from the package once per version, so there is no other.
	rockRevision = "-1"
	// backupSuffix ends the name of the backup a deploy makes of a file in its
	// way.
	backupSuffix = "~"

	dirPerm  os.FileMode = 0o755
	filePerm os.FileMode = 0o644
	execPerm os.FileMode = 0o755
	// execBits are the executable bits of a mode.
	execBits os.FileMode = 0o111
)

// errVersion reports a package version that has no LuaRocks form.
var errVersion = errors.New("the version has no LuaRocks form")

// RockVersion maps the SemVer version of a tt package to the version of its
// rock. LuaRocks versions are dot-separated numbers and words with a numeric
// revision after a dash, so the pre-release and build parts of SemVer, after
// '-' and '+', become more dot-separated parts, and the revision is 1:
//
//	1.2.0                      1.2.0-1
//	1.2.1-dev.3+gabc1234       1.2.1.dev.3.gabc1234-1
//	0.0.0-dev.5+gabc1234.dirty 0.0.0.dev.5.gabc1234.dirty-1
//	2.0.0-rc.1                 2.0.0.rc.1-1
//
// Two versions of a package are two versions of its rock: LuaRocks weighs
// the words (dev above every release, rc below it), so its order of the rock
// versions is not SemVer's, which tt does not ask it for.
func RockVersion(semver string) (string, error) {
	mapped := strings.NewReplacer("-", ".", "+", ".").Replace(strings.TrimPrefix(semver, "v"))

	if mapped == "" || strings.Contains(mapped, "..") || strings.HasPrefix(mapped, ".") ||
		strings.HasSuffix(mapped, ".") {
		return "", fmt.Errorf("%w: %q", errVersion, semver)
	}

	for _, r := range mapped {
		if !isAlnum(r) && r != '.' {
			return "", fmt.Errorf("%w: %q", errVersion, semver)
		}
	}

	version := mapped + rockRevision

	_, err := deps.ParseVersion(version)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", errVersion, semver, err)
	}

	return version, nil
}

// isAlnum reports whether r is an ASCII letter or digit.
func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// Package describes the rock a tt package is installed as.
type Package struct {
	// Name is the package name, which is the rock's.
	Name string
	// Version is the package's SemVer version; RockVersion gives the rock's.
	Version string
	// Summary is the package's description, if any.
	Summary string
	// Dependencies are the rocks the package requires, each at the version
	// its lock pins, as rock name → rock version. The tree manifest records
	// them as the rock's dependencies, which is how it tells that the
	// package's rocks are required.
	Dependencies map[string]string
}

// Rockspec renders the rockspec of pkg's rock and evaluates it, so that its
// RawSource is the rockspec the tree installs with the rock. The rock is
// never fetched or built: its source names no archive, its build type is
// none, and its programs are deployed as they are rather than behind the
// command wrappers of Lua scripts.
func Rockspec(pkg Package) (*luarocks.Rockspec, error) {
	version, err := RockVersion(pkg.Version)
	if err != nil {
		return nil, fmt.Errorf("the rock of %s %s: %w", pkg.Name, pkg.Version, err)
	}

	var src strings.Builder

	fmt.Fprintf(&src, "-- The rock of the tt package %s %s, which tt installs from the\n"+
		"-- files it lays out. It is never fetched or built.\n", pkg.Name, pkg.Version)
	src.WriteString("rockspec_format = \"3.0\"\n")
	fmt.Fprintf(&src, "package = %s\n", luaQuote(pkg.Name))
	fmt.Fprintf(&src, "version = %s\n", luaQuote(version))
	src.WriteString("source = {\n   url = \"file://.\",\n}\n")
	fmt.Fprintf(&src, "description = {\n   summary = %s,\n}\n", luaQuote(pkg.Summary))

	if len(pkg.Dependencies) > 0 {
		names := make([]string, 0, len(pkg.Dependencies))
		for name := range pkg.Dependencies {
			names = append(names, name)
		}

		slices.Sort(names)

		src.WriteString("dependencies = {\n")

		for _, name := range names {
			fmt.Fprintf(&src, "   %s,\n", luaQuote(name+" == "+pkg.Dependencies[name]))
		}

		src.WriteString("}\n")
	}

	src.WriteString("build = {\n   type = \"none\",\n}\n")
	src.WriteString("deploy = {\n   wrap_bin_scripts = false,\n}\n")

	name := pkg.Name + "-" + version + ".rockspec"

	spec, err := rockspec.EvalSource(name, []byte(src.String()), luarocks.RockspecConfig{})
	if err != nil {
		return nil, fmt.Errorf("the rockspec of %s %s: %w", pkg.Name, pkg.Version, err)
	}

	return spec, nil
}

// luaQuote quotes s as a Lua 5.1 string literal: Lua knows no \u or \x
// escape, so every byte outside printable ASCII is a decimal one.
func luaQuote(text string) string {
	var out strings.Builder

	out.WriteByte('"')

	for i := range len(text) {
		char := text[i]

		switch {
		case char == '"' || char == '\\':
			out.WriteByte('\\')
			out.WriteByte(char)
		case char < ' ' || char > '~':
			out.WriteString("\\" + strconv.Itoa(int(char)))
		default:
			out.WriteByte(char)
		}
	}

	out.WriteByte('"')

	return out.String()
}

// InstallPackage installs the stage stage, laid out as an unpacked binary
// rock, as the rock of spec into the tree rooted at root, in the transaction
// change: the rock's other versions are removed first, and every file of the
// rock has to take its plain path, so that require and the programs of the
// tree reach the package's own files; one another rock holds, at its plain
// path or a versioned one, fails it with an error wrapping tree.ErrNotPlain,
// the tree as it was - whichever of the two the tree's conflict rules would
// give the plain path, the other would lose a file to the package.
//
// A file at one of those paths that no rock owns is what an older tt laid out
// by hand, before the package was a rock; it is removed rather than backed up
// by the deploy, as a build of that tt overwrote it.
func InstallPackage(change *tree.Tx, root string, spec *luarocks.Rockspec, stage string) error {
	targets, err := stageTargets(stage)
	if err != nil {
		return err
	}

	err = makeRoom(change.View(), root, spec.Package, targets)
	if err != nil {
		return err
	}

	_, err = change.InstallStaged(spec, stage, tree.StagedOpts{
		RequirePlain:        true,
		RemoveOtherVersions: true,
	})
	if err != nil {
		return fmt.Errorf("installing %s %s: %w", spec.Package, spec.Version, err)
	}

	return nil
}

// PreparePackage makes room in the tree rooted at root, in the transaction
// change, for the rock ref of the tree src views - a package's rock, about to
// be grafted from an archive (tree.Tx.Graft) - as InstallPackage does for a
// stage: a file at a path the rock is deployed to that no rock owns is
// removed, and one another rock than the package's owns fails it with an error
// wrapping tree.ErrNotPlain.
func PreparePackage(change *tree.Tx, root string, src *tree.View, ref tree.RockRef) error {
	files, err := src.Files(ref)
	if err != nil {
		return fmt.Errorf("reading the rock %s: %w", ref, err)
	}

	sections := map[tree.Section]string{
		tree.SectionLua: shareTarantool,
		tree.SectionLib: libTarantool,
		tree.SectionBin: binDir,
	}

	var targets []string

	for _, file := range files {
		if deployed, ok := sections[file.Section]; ok {
			targets = append(targets, filepath.Join(filepath.FromSlash(deployed),
				filepath.FromSlash(file.Entry)))
		}
	}

	return makeRoom(change.View(), root, ref.Name, targets)
}

// deployDirs maps each deployed directory of a stage to the one of the tree
// its files go to.
func deployDirs() map[string]string {
	return map[string]string{
		StageLua: shareTarantool,
		StageLib: libTarantool,
		StageBin: binDir,
	}
}

// stageTargets lists the paths in a tree, relative to its root, the files of
// stage are deployed to.
func stageTargets(stage string) ([]string, error) {
	var targets []string

	for sub, deployed := range deployDirs() {
		base := filepath.Join(stage, sub)

		err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
			if errors.Is(err, fs.ErrNotExist) && path == base {
				return fs.SkipAll
			}

			if err != nil || entry.IsDir() {
				return err
			}

			rel, err := filepath.Rel(base, path)
			if err != nil {
				return fmt.Errorf("staging %s: %w", path, err)
			}

			targets = append(targets, filepath.Join(filepath.FromSlash(deployed), rel))

			return nil
		})
		if err != nil {
			return nil, fmt.Errorf("installing the package rock: %w", err)
		}
	}

	return targets, nil
}

// makeRoom removes the files of the tree at root at targets, paths relative to
// it, that no rock of view owns, and fails with an error wrapping
// tree.ErrNotPlain, before anything is removed, when another rock than name
// owns one: the tree's conflict rules would give the plain path to one of the
// two by their names, and the other's require would load the wrong file.
func makeRoom(view *tree.View, root, name string, targets []string) error {
	var unowned, taken []string

	for _, target := range targets {
		owner, _, owned := view.Owner(target)

		switch {
		case !owned:
			unowned = append(unowned, target)
		case owner.Name != name:
			taken = append(taken, target+" ("+owner.String()+")")
		}
	}

	if len(taken) > 0 {
		slices.Sort(taken)

		return fmt.Errorf("%w: the rock %s would take the place of the files of other rocks: %s",
			tree.ErrNotPlain, name, strings.Join(taken, ", "))
	}

	for _, target := range unowned {
		err := os.Remove(filepath.Join(root, target))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("removing %s, which no rock owns: %w", target, err)
		}
	}

	return nil
}

// PackageDependencies maps the dependencies the manifest man declares,
// globally and in the components of the product named product, to the
// versions the lock product prod pins: the Dependencies of the package's rock.
// A declaration the product does not pin is not its.
func PackageDependencies(
	man *manifest.Manifest, product string, prod manifest.LockProduct,
) map[string]string {
	declared := maps.Clone(man.Dependencies)
	if declared == nil {
		declared = map[string]manifest.Dependency{}
	}

	for _, name := range man.EffectiveProducts()[product].Components {
		maps.Copy(declared, man.Components[name].Dependencies)
	}

	pinned := map[string]string{}

	for _, dep := range prod.Dependencies {
		if _, ok := declared[dep.Name]; ok {
			pinned[dep.Name] = dep.Version
		}
	}

	return pinned
}

// StageUntracked lays out the files of untracked, the files of the tree at
// src no rock of it lists (tree.GraftReport.Untracked), into stage as an
// unpacked binary rock, and returns how many it staged: those under
// share/tarantool/, lib/tarantool/ and bin/. They are a package's own files,
// as a tt that did not install a package as a rock packed them; the backups
// a deploy made are left out.
func StageUntracked(src string, untracked []string, stage string) (int, error) {
	staged := 0

	for _, path := range untracked {
		if strings.HasSuffix(path, backupSuffix) {
			continue
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return staged, fmt.Errorf("staging %s: %w", path, err)
		}

		dst, ok := stagePath(filepath.ToSlash(rel))
		if !ok {
			continue
		}

		err = copyFile(path, filepath.Join(stage, filepath.FromSlash(dst)))
		if err != nil {
			return staged, err
		}

		staged++
	}

	return staged, nil
}

// stagePath maps the slash path rel of a file of a tree to its path in a
// stage, false for a file outside the deployed directories.
func stagePath(rel string) (string, bool) {
	for sub, deployed := range deployDirs() {
		rest, ok := strings.CutPrefix(rel, deployed+"/")
		if ok && rest != "" {
			return sub + "/" + rest, true
		}
	}

	return "", false
}

// copyFile copies the regular file src to dst, creating dst's parents, as an
// executable when src is one.
func copyFile(src, dst string) error {
	info, err := os.Lstat(src)
	if err != nil {
		return fmt.Errorf("staging %s: %w", src, err)
	}

	if !info.Mode().IsRegular() {
		return nil
	}

	mode := filePerm
	if info.Mode().Perm()&execBits != 0 {
		mode = execPerm
	}

	err = os.MkdirAll(filepath.Dir(dst), dirPerm)
	if err != nil {
		return fmt.Errorf("staging %s: %w", src, err)
	}

	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("staging %s: %w", src, err)
	}

	defer func() { _ = source.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("staging %s: %w", src, err)
	}

	_, err = io.Copy(out, source)
	if err != nil {
		_ = out.Close()

		return fmt.Errorf("staging %s: %w", src, err)
	}

	err = out.Close()
	if err != nil {
		return fmt.Errorf("staging %s: %w", src, err)
	}

	return nil
}
