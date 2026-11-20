"""Check the commit messages, the changelog and the SDK API of a change.

gitlint and changelog are notes of a pull request and never fail CI. api
compares the API of every Go module under sdk/ of the head with that of the
base and fails on an incompatible change, unless the pull request has the
sdk-breaking label; with --release it compares a release with the release
before it (release_base tells which and whether a break fails).
"""

import argparse
import contextlib
import dataclasses
import html
import json
import os
import re
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
NOTES = ROOT / "pr-notes"

# The label of a pull request that changes the SDK API incompatibly on purpose.
BREAKING_LABEL = "sdk-breaking"

# A release tag: vMAJOR.MINOR.PATCH with an optional pre-release suffix.
RELEASE = re.compile(r"v(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?")

APIDIFF_SECTIONS = {"Incompatible changes:": "incompatible", "Compatible changes:": "compatible"}


def command(*args, cwd=ROOT, env=None):
    return subprocess.run(args, cwd=cwd, env=env, capture_output=True, text=True)


def git(*args):
    result = command("git", *args)
    result.check_returncode()
    return result.stdout.strip()


def commit(revision):
    return git("rev-parse", "--verify", "--end-of-options", f"{revision}^{{commit}}")


def report(name, output, failed, markdown=None):
    print(output)
    NOTES.mkdir(exist_ok=True)
    (NOTES / f"{name}.txt").write_text(output + "\n")
    if summary := os.getenv("GITHUB_STEP_SUMMARY"):
        if markdown is None:
            markdown = (
                f"### {name}: {'attention needed' if failed else 'OK'}\n\n"
                f"<pre>{html.escape(output[:50000])}</pre>\n\n"
            )
        with open(summary, "a") as stream:
            stream.write(markdown)
    return int(failed)


def pull_request_labels():
    """Return the labels of the pull request of the event, None off a pull request."""
    event_path = os.getenv("GITHUB_EVENT_PATH")
    event = json.loads(Path(event_path).read_text()) if event_path else {}
    pull_request = event.get("pull_request")
    if pull_request is None:
        return None
    return {label["name"] for label in pull_request.get("labels", [])}


@contextlib.contextmanager
def checkout(commit, path):
    git("worktree", "add", "--detach", str(path), commit)
    try:
        yield path
    finally:
        git("worktree", "remove", "--force", str(path))


def sdk_modules(tree):
    modules = {}
    for path in (tree / "sdk").rglob("go.mod"):
        if {"vendor", "third_party", "testdata"} & set(path.parts):
            continue
        match = re.search(r"^module\s+(\S+)", path.read_text(), re.MULTILINE)
        if match:
            modules[path.parent.relative_to(tree)] = match[1]
    return modules


@dataclasses.dataclass
class ModuleDiff:
    """The change of the API of one SDK module, named by its directory."""

    path: str
    # same, changed, added, removed or failed: the API could not be compared.
    state: str
    incompatible: list = dataclasses.field(default_factory=list)
    compatible: list = dataclasses.field(default_factory=list)
    error: str = ""

    @property
    def breaking(self):
        return self.state == "removed" or bool(self.incompatible)

    def headline(self):
        if self.state == "failed":
            return "the API could not be compared"
        if self.state == "added":
            return "added module, compatible"
        if self.state == "removed":
            return "removed module, incompatible"
        if self.state == "same":
            return "no API changes"
        counts = [
            f"{len(changes)} {kind}"
            for kind, changes in (
                ("incompatible", self.incompatible),
                ("compatible", self.compatible),
            )
            if changes
        ]
        return ", ".join(counts)

    def details(self):
        """Return the lines under the headline: incompatible changes first."""
        if self.state == "failed":
            return self.error.splitlines()
        lines = []
        for title, changes in (
            ("Incompatible changes:", self.incompatible),
            ("Compatible changes:", self.compatible),
        ):
            if changes:
                lines += [title, *(f"- {change}" for change in changes)]
        return lines


def parse_apidiff(text):
    """Return the incompatible and the compatible changes of an apidiff report."""
    changes = {"incompatible": [], "compatible": []}
    section = None
    for line in text.splitlines():
        if line in APIDIFF_SECTIONS:
            section = changes[APIDIFF_SECTIONS[line]]
        elif section is not None and line.startswith("- "):
            section.append(line[2:])
        elif line.strip():
            raise ValueError(f"unexpected line in the report of apidiff: {line!r}")
    return changes["incompatible"], changes["compatible"]


def export_api(directory, export):
    """Write the API of the Go module in directory to export; return the error."""
    # apidiff -m loads the packages a pattern matches, the ones of every SDK
    # module the module requires among them, and names the module by the
    # first package it loaded. "./..." loads the packages of the module in
    # directory, and only those while every SDK module it requires has a
    # replace: the workspace is off below. A module that changed the name of
    # its path rather than its major version fails to export this way, which
    # is right: nothing compares a module whose packages are under another
    # module path, and renaming one is incompatible by itself.
    result = command(
        "apidiff",
        "-m",
        "-w",
        str(export),
        ".",
        cwd=directory,
        env={**os.environ, "GOWORK": "off"},
    )
    if result.returncode:
        return (result.stdout + result.stderr).strip() or f"apidiff exited with {result.returncode}"
    return ""


def compare_module(path, old, new, workdir, before, after):
    """Compare the API of the SDK module in directory path of the trees old and new."""
    name = path.as_posix()
    exports = []
    for side, tree in (("base", old), ("head", new)):
        export = workdir / f"{side}.api"
        error = export_api(tree / path, export)
        if error:
            return ModuleDiff(name, "failed", error=f"Exporting the API of the {side}:\n{error}")
        exports.append(export)
    result = command("apidiff", "-m", *(str(export) for export in exports))
    # Both exports are named by the module path of their side, so a module
    # whose path did not change compares with itself; the module path is
    # reported apart, as a change of its own.
    changed_path = before != after
    diagnostics = [
        line
        for line in result.stderr.splitlines()
        if line.strip()
        and not line.startswith("Ignoring internal package ")
        and not (changed_path and "different module paths" in line)
    ]
    if result.returncode or diagnostics:
        error = "\n".join([*diagnostics, result.stdout.strip()]).strip()
        return ModuleDiff(name, "failed", error=f"Comparing the APIs:\n{error}")
    try:
        incompatible, compatible = parse_apidiff(result.stdout)
    except ValueError as error:
        return ModuleDiff(name, "failed", error=str(error))
    state = "changed" if incompatible or compatible else "same"
    return ModuleDiff(name, state, incompatible, compatible)


def api_diff(base, head):
    """Compare the API of every SDK module of head with that of base.

    Return None when nothing under sdk/ changed. The go.mod and go.sum of the
    modules are there too: a change of a dependency of the SDK is compared.
    """
    if not git("diff", "--name-only", base, head, "--", "sdk"):
        return None
    diffs = []
    with tempfile.TemporaryDirectory(prefix="tt-sdk-api-") as temporary:
        temp = Path(temporary)
        with checkout(base, temp / "base") as old, checkout(head, temp / "head") as new:
            before, after = sdk_modules(old), sdk_modules(new)
            for index, path in enumerate(sorted(before.keys() | after.keys())):
                if path not in after:
                    diffs.append(ModuleDiff(path.as_posix(), "removed"))
                    continue
                if path not in before:
                    diffs.append(ModuleDiff(path.as_posix(), "added"))
                    continue
                workdir = temp / str(index)
                workdir.mkdir()
                diff = compare_module(path, old, new, workdir, before[path], after[path])
                if before[path] != after[path] and diff.state != "failed":
                    diff.state = "changed"
                    diff.incompatible.insert(
                        0,
                        f"module path: changed from {before[path]} to {after[path]}",
                    )
                diffs.append(diff)
    return diffs


def rank(diff):
    """Order the modules of a report: what fails first, unchanged last."""
    if diff.state == "failed":
        return 0
    if diff.breaking:
        return 1
    return 2 if diff.state != "same" else 3


@dataclasses.dataclass
class Policy:
    """What an incompatible change does to the check."""

    # Why an incompatible change passes, "allowed by the sdk-breaking label",
    # or None when it fails the check.
    allowed: str | None
    # How to let an incompatible change through, for a failed check.
    hint: str


def api_report(diffs, policy, compared):
    """Return the text, the markdown summary and whether the check failed.

    compared names what was compared: "The SDK API of HEAD against BASE".
    """
    if diffs is None:
        text = f"{compared}: nothing under sdk/ changed."
        return text, f"### SDK API: no changes\n\n{text}\n\n", False
    failed_modules = [diff for diff in diffs if diff.state == "failed"]
    breaking = any(diff.breaking for diff in diffs)
    if failed_modules:
        title = "could not be compared"
    elif breaking:
        title = "incompatible changes"
        if policy.allowed:
            title += f", {policy.allowed}"
    elif any(diff.state != "same" for diff in diffs):
        title = "compatible changes only"
    else:
        title = "no API changes"
    failed = bool(failed_modules) or (breaking and not policy.allowed)

    text = [f"{compared}: {title}.", ""]
    markdown = [f"### SDK API: {title}", "", f"{compared}.", ""]
    same = []
    for diff in sorted(diffs, key=lambda diff: (rank(diff), diff.path)):
        if diff.state == "same":
            same.append(diff.path)
            continue
        details = diff.details()
        text += [f"{diff.path}: {diff.headline()}", *(f"  {line}" for line in details), ""]
        markdown += [f"#### `{diff.path}`: {diff.headline()}", ""]
        if details:
            block = html.escape("\n".join(details))
            markdown += [f"<pre>{block}</pre>", ""]
    if same:
        text += [f"No API changes: {', '.join(same)}", ""]
        markdown += ["No API changes: " + ", ".join(f"`{path}`" for path in same) + ".", ""]
    if failed_modules:
        note = (
            "The check fails until the API of every module is compared: "
            "the errors above tell why it was not."
        )
    elif breaking and policy.allowed:
        note = (
            f"The incompatible changes are {policy.allowed}: "
            "a module built on the SDK before them may no longer build."
        )
    elif breaking:
        note = (
            "A module built on the SDK before these changes may no longer build: "
            "keep the old API, adding rather than changing. "
            f"For a break on purpose, {policy.hint}"
        )
    else:
        note = ""
    if note:
        text.append(note)
        markdown += [note, ""]
    markdown.append("")
    return "\n".join(text).rstrip(), "\n".join(markdown), failed


def annotate(title, failed, allowed):
    """Show the outcome of the check on the run of GitHub Actions."""
    if os.getenv("GITHUB_ACTIONS") != "true":
        return
    if failed:
        print(f"::error title=SDK API::{title}")
    elif allowed:
        print(f"::warning title=SDK API::{title}")


def release_key(tag):
    """Return the order of semantic versions of a release tag, None for another tag."""
    match = RELEASE.fullmatch(tag)
    if not match:
        return None
    major, minor, patch, prerelease = match.groups()
    numbers = (int(major), int(minor), int(patch))
    if prerelease is None:
        return (*numbers, 1)
    parts = tuple(
        (0, int(part), "") if part.isdigit() else (1, 0, part) for part in prerelease.split(".")
    )
    return (*numbers, 0, parts)


def is_stable(key):
    return key[3] == 1


def release_base(tag, tags):
    """Return the release tag is compared with, and whether a break fails it.

    A release is compared with a release of tt before it of its major
    version. A stable release, with a stable one before it in its major, is
    compared with that one, and an incompatible change of the SDK fails it:
    the SDK is kept compatible between stable releases. A pre-release, or
    the first stable release of a major version, is compared with the
    release just before it, and an incompatible change only warns: until a
    major version is released its SDK may change. (None, False) for a tag
    that is no release, and for the first release of a major version.
    """
    key = release_key(tag)
    if key is None:
        return None, False
    earlier = sorted(
        (other_key, other)
        for other in tags
        if (other_key := release_key(other)) and other_key[0] == key[0] and other_key < key
    )
    stable = [other for other_key, other in earlier if is_stable(other_key)]
    if is_stable(key) and stable:
        return stable[-1], True
    return (earlier[-1][1], False) if earlier else (None, False)


def api_check(args):
    """Run the api check: return the text, the markdown and whether it failed."""
    if args.release:
        return release_check(args.release, args.head, args.allow_incompatible)
    head = commit(args.head)
    base = git("merge-base", commit(args.base), head)
    labels = pull_request_labels()
    if args.allow_incompatible:
        allowed = "allowed by --allow-incompatible"
    elif labels is not None and BREAKING_LABEL in labels:
        allowed = f"allowed by the {BREAKING_LABEL} label"
    else:
        allowed = None
    if labels is not None:
        hint = (
            f"add the {BREAKING_LABEL} label to the pull request; "
            "the check runs again on a label and passes, listing the changes."
        )
    else:
        hint = (
            "pass --allow-incompatible, which reports the changes without failing; "
            f"in CI, the {BREAKING_LABEL} label of the pull request does that."
        )
    text, markdown, failed = api_report(
        api_diff(base, head),
        Policy(allowed, hint),
        f"The SDK API of {head[:12]} against the merge base {base[:12]}",
    )
    annotate(text.splitlines()[0], failed, allowed)
    return text, markdown, failed


def release_check(tag, head_revision, allow_incompatible):
    """Compare the SDK of the release tag, at head_revision or the tag, with its base."""
    key = release_key(tag)
    if key is None:
        text = f"{tag} is not a release tag vX.Y.Z[-PRE]: there is no SDK API to compare."
        return text, f"### SDK API: not a release\n\n{text}\n\n", False
    head = commit(head_revision or tag)
    base_tag, strict = release_base(tag, git("tag", "--list", "v*").splitlines())
    if base_tag is None:
        text = f"{tag} is the first release of v{key[0]}: there is no SDK API to compare with."
        return text, f"### SDK API: first release\n\n{text}\n\n", False
    if allow_incompatible:
        allowed = "allowed by --allow-incompatible"
    elif not strict:
        kind = "the first stable release" if is_stable(key) else "a pre-release"
        allowed = f"allowed for {kind} of v{key[0]}"
    else:
        allowed = None
    hint = (
        "run publish.yml on the tag with allow-incompatible-sdk: "
        f"gh workflow run publish.yml --ref {tag} -f allow-incompatible-sdk=true."
    )
    base = commit(base_tag)
    text, markdown, failed = api_report(
        api_diff(base, head),
        Policy(allowed, hint),
        f"The SDK API of {tag} ({head[:12]}) against "
        f"{'the stable release' if strict else 'the release'} {base_tag} ({base[:12]})",
    )
    annotate(text.splitlines()[0], failed, allowed)
    return text, markdown, failed


def note(check, base, head):
    """Run gitlint or changelog: return the note and whether it asks for attention."""
    head = commit(head)
    base = git("merge-base", commit(base), head)
    if check == "gitlint":
        result = command("gitlint", "--ignore-stdin", "--commits", f"{base}..{head}")
        output = (result.stdout + result.stderr).strip() or "Commit messages passed gitlint."
        return output, result.returncode != 0
    labels = pull_request_labels() or set()
    updated = bool(git("diff", "--name-only", base, head, "--", "CHANGELOG.md"))
    failed = not updated and "no-changelog" not in labels
    output = (
        "Update CHANGELOG.md or add the no-changelog label."
        if failed
        else "CHANGELOG.md changed or no-changelog is set."
    )
    return output, failed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("check", choices=("gitlint", "changelog", "api"))
    parser.add_argument("--base", help="the base of the pull request")
    parser.add_argument("--head", help="the head of the pull request, or the commit of --release")
    parser.add_argument(
        "--release",
        metavar="TAG",
        help="api: compare the SDK of the release TAG, at --head or at the tag, "
        "with the release before it",
    )
    parser.add_argument(
        "--allow-incompatible",
        action="store_true",
        help="api: report incompatible changes of the SDK API without failing",
    )
    args = parser.parse_args()
    if args.release:
        if args.check != "api" or args.base:
            parser.error("--release is an option of api, without --base")
    elif not (args.base and args.head):
        parser.error("--base and --head are required")
    markdown = None
    try:
        if args.check == "api":
            output, markdown, failed = api_check(args)
        else:
            output, failed = note(args.check, args.base, args.head)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        output, markdown, failed = f"Could not complete {args.check}: {error}", None, True
    return report(args.check, output, failed, markdown)


if __name__ == "__main__":
    raise SystemExit(main())
