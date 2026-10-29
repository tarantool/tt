"""Report non-blocking commit, changelog and SDK API checks for a PR."""

import argparse
import contextlib
import html
import json
import os
import re
import subprocess
import tempfile
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]


def command(*args, cwd=ROOT):
    return subprocess.run(args, cwd=cwd, capture_output=True, text=True)


def git(*args):
    result = command("git", *args)
    result.check_returncode()
    return result.stdout.strip()


def report(name, output, failed):
    print(output)
    directory = ROOT / "pr-notes"
    directory.mkdir(exist_ok=True)
    (directory / f"{name}.txt").write_text(output + "\n")
    if summary := os.getenv("GITHUB_STEP_SUMMARY"):
        with open(summary, "a") as stream:
            stream.write(
                f"### {name}: {'attention needed' if failed else 'OK'}\n\n"
                f"<pre>{html.escape(output[:50000])}</pre>\n\n",
            )
    return int(failed)


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
        if "vendor" in path.parts or "third_party" in path.parts:
            continue
        match = re.search(r"^module\s+(\S+)", path.read_text(), re.MULTILINE)
        if match:
            modules[path.parent.relative_to(tree)] = match[1]
    return modules


def api_diff(base, head):
    if not git("diff", "--name-only", base, head, "--", "sdk"):
        return "No SDK changes.", False
    messages = []
    failed = False
    with tempfile.TemporaryDirectory(prefix="tt-pr-api-") as temporary:
        temp = Path(temporary)
        with checkout(base, temp / "base") as old, checkout(head, temp / "head") as new:
            before, after = sdk_modules(old), sdk_modules(new)
            for index, module in enumerate(sorted(before.keys() | after.keys())):
                if module not in before or module not in after:
                    messages.append(f"{module}: {'added' if module in after else 'removed'} module")
                    failed = True
                    continue
                exports = [temp / f"{index}-base.api", temp / f"{index}-head.api"]
                for tree, modules, export in zip((old, new), (before, after), exports, strict=True):
                    result = command(
                        "apidiff",
                        "-m",
                        "-w",
                        str(export),
                        modules[module],
                        cwd=tree / module,
                    )
                    if result.returncode:
                        messages.append(
                            f"{module}: API export failed\n{result.stdout}{result.stderr}",
                        )
                        failed = True
                        break
                else:
                    result = command("apidiff", "-m", *(str(path) for path in exports))
                    changes = result.stdout.strip()
                    diagnostics = result.stderr.strip()
                    output = changes or "No API changes."
                    if diagnostics:
                        output += f"\n{diagnostics}"
                    messages.append(f"{module}:\n{output}")
                    failed |= bool(result.returncode or changes)
    return "\n\n".join(messages), failed


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("check", choices=("gitlint", "changelog", "api"))
    parser.add_argument("--base", required=True)
    parser.add_argument("--head", required=True)
    args = parser.parse_args()
    try:
        base = git("rev-parse", "--verify", "--end-of-options", f"{args.base}^{{commit}}")
        head = git("rev-parse", "--verify", "--end-of-options", f"{args.head}^{{commit}}")
        base = git("merge-base", base, head)
        if args.check == "gitlint":
            result = command("gitlint", "--ignore-stdin", "--commits", f"{base}..{head}")
            output = (result.stdout + result.stderr).strip() or "Commit messages passed gitlint."
            failed = result.returncode != 0
        elif args.check == "changelog":
            event_path = os.getenv("GITHUB_EVENT_PATH")
            event = json.loads(Path(event_path).read_text()) if event_path else {}
            labels = {label["name"] for label in event.get("pull_request", {}).get("labels", [])}
            updated = bool(git("diff", "--name-only", base, head, "--", "CHANGELOG.md"))
            failed = not updated and "no-changelog" not in labels
            output = (
                "Update CHANGELOG.md or add the no-changelog label."
                if failed
                else "CHANGELOG.md changed or no-changelog is set."
            )
        else:
            output, failed = api_diff(base, head)
    except (OSError, ValueError, subprocess.SubprocessError) as error:
        output, failed = f"Could not complete {args.check}: {error}", True
    return report(args.check, output, failed)


if __name__ == "__main__":
    raise SystemExit(main())
