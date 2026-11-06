"""List the translated documentation pages that lag behind their English ones.

A translation of doc/<page>.md is doc/<page>.<locale>.md. It is stale when
git has a change of the English page that came after the last change of the
translation, and orphaned when the English page is gone. Uncommitted changes
count as the newest ones. The check needs the full history of the repository
and only reports: it fails with --strict alone.
"""

import argparse
import os
import re
import subprocess
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DOCS = ROOT / "doc"
TRANSLATION = re.compile(r"^(?P<stem>.+)\.(?P<locale>[a-z]{2})\.md$")


def git(*args):
    result = subprocess.run(["git", *args], cwd=ROOT, capture_output=True, text=True)
    result.check_returncode()
    return result.stdout.strip()


def uncommitted(path):
    return bool(git("status", "--porcelain", "--", str(path)))


def last_commit(path):
    return git("log", "-1", "--format=%H", "--", str(path))


def lag(source, translation):
    """Return the changes of source the translation has not followed."""
    if uncommitted(translation):
        return []
    since = last_commit(translation)
    if not since:
        return []
    changes = git("log", "--format=%h %s", f"{since}..HEAD", "--", str(source))
    changes = changes.splitlines() if changes else []
    if uncommitted(source):
        changes.insert(0, "(uncommitted changes)")
    return changes


def annotate(path, message):
    if os.getenv("GITHUB_ACTIONS"):
        print(f"::warning file={path}::{message}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--strict", action="store_true", help="fail on a stale translation")
    args = parser.parse_args()

    problems = 0
    for translation in sorted(DOCS.rglob("*.md")):
        match = TRANSLATION.match(translation.name)
        if not match:
            continue
        source = translation.with_name(match["stem"] + ".md")
        name = translation.relative_to(ROOT)
        if not source.exists():
            problems += 1
            message = f"{source.relative_to(ROOT)} no longer exists"
            print(f"{name}: orphaned, {message}")
            annotate(name, message)
            continue
        changes = lag(source, translation)
        if changes:
            problems += 1
            message = (
                f"{source.relative_to(ROOT)} changed since the translation, see "
                f"git diff {last_commit(translation)[:12]} -- {source.relative_to(ROOT)}"
            )
            print(f"{name}: stale, {message}")
            for change in changes:
                print(f"    {change}")
            annotate(name, message)

    if not problems:
        print("The translations are up to date.")
    return int(args.strict and problems > 0)


if __name__ == "__main__":
    raise SystemExit(main())
