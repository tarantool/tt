"""List the Go modules under sdk/ of tt and their tags, and check the release binding.

The SDK modules are released with Go tags before tt itself is released, and the
tag of a module carries the release of tt it belongs to: sdk/<name>/v3.M.P for
tt v3.M.P. A tag needs a major version in the module path, so the modules under
sdk/ keep their directories and their paths end in /v3: sdk -> tt/sdk/v3,
sdk/build -> tt/sdk/build/v3.

The tools find the modules by their go.mod files, not by a list here. They are
used by sdk-release.yml, which tags the modules, and by publish.yml, which
refuses a release of tt whose SDK is not tagged at the version its go.mod
requires.
"""

import argparse
import dataclasses
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PREFIX = "github.com/tarantool/tt/sdk"

# A release tag: vMAJOR.MINOR.PATCH with an optional pre-release suffix.
RELEASE = re.compile(r"v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?")
MODULE = re.compile(r"^module\s+(\S+)$", re.MULTILINE)
MAJOR = re.compile(r"/v(\d+)$")


def command(*args, cwd=ROOT):
    result = subprocess.run(args, cwd=cwd, capture_output=True, text=True)
    result.check_returncode()
    return result.stdout


def git(*args, cwd=ROOT):
    return command("git", *args, cwd=cwd)


@dataclasses.dataclass(frozen=True)
class Module:
    """One Go module of the SDK: its directory, path and require of it."""

    directory: str
    path: str

    @property
    def tag_prefix(self):
        """The prefix of the tags of the module: its directory, as the repository lays it out."""
        return self.directory

    @property
    def major(self):
        """The major version in the path, the digits between two dots."""
        match = MAJOR.search(self.path)
        return int(match.group(1)) if match else 0

    def tag(self, version):
        return f"{self.tag_prefix}/{version}"


def modules(root=ROOT):
    """Return the modules under sdk/, by directory, the root module first."""
    found = {}
    for path in sorted((root / "sdk").rglob("go.mod")):
        match = MODULE.search(path.read_text())
        if match is None:
            raise SystemExit(f"{path}: no module path")
        relative = path.parent.relative_to(root).as_posix()
        module = Module(relative, match.group(1))
        if module.major == 0:
            raise SystemExit(
                f"{path}: {module.path} has no major version in its path: "
                "a module of the SDK is released as sdk/<name>/vN"
            )
        found[relative] = module
    return found


def release(tag):
    """Return the version of a release tag of tt, refusing another tag."""
    match = RELEASE.fullmatch(tag)
    if match is None:
        raise SystemExit(f"{tag} is not a release tag of tt, vX.Y.Z[-PRE]")
    return tag


def required(go_mod, module):
    """Return the version the go.mod requires of the module, or None."""
    for line in go_mod.read_text().splitlines():
        fields = line.split()
        if len(fields) >= 2 and fields[0] == module.path and not line.lstrip().startswith("//"):
            return fields[1]
    return None


def blobs(root, revision, directory):
    """Return the id, mode and path of every file of a directory at a revision."""
    return git("ls-tree", "-r", revision, "--", directory, cwd=root)


def check_release(tag, root=ROOT):
    """Check that the SDK the tag releases is bound to the tags of its modules.

    Return the problems: a module whose version in the go.mod of the release
    is not its tag, a tag of another release of tt, or a tree that differs
    from the one the tag was made at.
    """
    version = release(tag)
    release_major, release_minor = (int(part) for part in version[1:].split(".")[:2])
    problems = []
    go_mod = root / "go.mod"
    for module in modules(root).values():
        wanted = required(go_mod, module)
        if wanted is None:
            problems.append(f"{module.directory}: go.mod does not require {module.path}")
            continue
        if module.major != release_major:
            problems.append(
                f"{module.directory}: {module.path} has major version v{module.major}, "
                f"the release {tag} is v{release_major}"
            )
            continue
        if not wanted.startswith(f"v{release_major}.{release_minor}."):
            problems.append(
                f"{module.directory}: go.mod requires {wanted}, "
                f"and the release {tag} is v{release_major}.{release_minor}"
            )
            continue
        problems += check_tag(root, module, wanted)
    return problems


def check_tag(root, module, version):
    """Check that the tag of the module exists and matches the released tree."""
    tag = module.tag(version)
    listed = git("tag", "--list", tag, cwd=root).split()
    if not listed:
        return [
            f"{module.directory}: {tag} is not a tag: run sdk-release.yml "
            f"with the version of the release first"
        ]
    try:
        tagged = git("rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}", cwd=root).strip()
    except subprocess.CalledProcessError as error:
        return [f"{module.directory}: {tag} cannot be resolved: {error.stderr.strip()}"]
    if blobs(root, tagged, module.directory) != blobs(root, "HEAD", module.directory):
        return [
            f"{module.directory}: the tree of the module in the release differs from "
            f"{tag}: move the version of the module in go.mod, or tag the change "
            "with sdk-release.yml"
        ]
    return []


def planned(tt_tag, root=ROOT):
    """Return the version each module gets with the release of tt, by directory.

    A module gets the major and minor of the release, and the patch of the
    version it has in the go.mod while that version is of the same major and
    minor: the patch grows only when the module changed, which bumps the
    go.mod. A new minor gives every module its vX.Y.0.
    """
    version = release(tt_tag)
    match = RELEASE.fullmatch(version)
    major, minor, patch = (int(part) for part in match.groups()[:3])
    suffix = match.group(4) or ""
    go_mod = root / "go.mod"
    versions = {}
    for module in modules(root).values():
        current = required(go_mod, module)
        same_minor = current and re.fullmatch(rf"v{major}\.{minor}\.\d+(-.+)?", current)
        if same_minor:
            module_patch = int(re.match(rf"v{major}\.{minor}\.(\d+)", current).group(1))
            text = f"v{major}.{minor}.{module_patch}"
        else:
            text = f"v{major}.{minor}.{patch}"
        if suffix:
            text = f"{text}-{suffix}"
        versions[module.directory] = text
    return versions


def bump(tt_tag, root=ROOT):
    """Write the planned versions of the modules into the requires of every go.mod."""
    versions = planned(tt_tag, root)
    by_path = {module.path: versions[module.directory] for module in modules(root).values()}
    edited = []
    for path in go_mods(root):
        text = path.read_text()
        lines = []
        for line in text.splitlines(keepends=True):
            fields = line.split()
            if len(fields) >= 2 and fields[0] in by_path and "=>" not in line:
                version = by_path[fields[0]]
                if fields[1] != version:
                    line = line.replace(fields[0] + " " + fields[1], f"{fields[0]} {version}", 1)
            lines.append(line)
        new = "".join(lines)
        if new != text:
            path.write_text(new)
            edited.append(path.relative_to(root).as_posix())
    return edited


def go_mods(root=ROOT):
    """Return every go.mod of the repository, not those of test fixtures."""
    for path in sorted(root.rglob("go.mod")):
        parts = set(path.parts)
        if {".git", ".claude", ".worktrees", "testdata", "vendor", "third_party"} & parts:
            continue
        yield path


def check_modules(tt_tag, root=ROOT):
    """Check that every go.mod requires the planned versions of the modules."""
    versions = planned(tt_tag, root)
    by_path = {module.path: versions[module.directory] for module in modules(root).values()}
    problems = []
    for path in go_mods(root):
        for line in path.read_text().splitlines():
            fields = line.split()
            if len(fields) >= 2 and fields[0] in by_path and "=>" not in line:
                version = by_path[fields[0]]
                if fields[1] != version:
                    problems.append(
                        f"{path.relative_to(root).as_posix()}: {fields[0]} at {fields[1]}, "
                        f"the release {tt_tag} gives it {version}"
                    )
    return problems


def create(tt_tag, push, root=ROOT):
    """Tag every module at its planned version and return the tags made."""
    versions = planned(tt_tag, root)
    tags = []
    for module in modules(root).values():
        version = versions[module.directory]
        tag = module.tag(version)
        if git("tag", "--list", tag, cwd=root).split():
            continue
        git(
            "tag",
            "-a",
            tag,
            "-m",
            f"SDK of tt {tt_tag}",
            cwd=root,
        )
        tags.append(tag)
    if push and tags:
        git("push", "origin", *tags, cwd=root)
    return tags


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    sub = parser.add_subparsers(dest="check", required=True)
    for name, help_text in (
        ("plan", "the version each module gets with the release of tt"),
        ("bump", "write the planned versions into the go.mod files"),
        ("check-sdk", "check that the go.mod files require the planned versions"),
        ("create", "tag the modules at their planned versions"),
        ("check-release", "check that the release of tt is bound to the module tags"),
    ):
        command_parser = sub.add_parser(name, help=help_text)
        command_parser.add_argument("tag", help="the release tag of tt, vX.Y.Z")
        if name == "create":
            command_parser.add_argument(
                "--push", action="store_true", help="push the tags after making them"
            )
    args = parser.parse_args(argv)
    if args.check == "plan":
        for directory, version in planned(args.tag).items():
            print(f"{directory}\t{version}")
        return 0
    if args.check == "bump":
        for path in bump(args.tag):
            print(path)
        return 0
    if args.check == "check-sdk":
        problems = check_modules(args.tag)
    elif args.check == "create":
        for tag in create(args.tag, args.push):
            print(tag)
        return 0
    else:
        problems = check_release(args.tag)
    for problem in problems:
        print(problem, file=sys.stderr)
    return int(bool(problems))


if __name__ == "__main__":
    raise SystemExit(main())
