"""Exercise the SDK tags of ci/sdk_tags.py without Go, network or tags pushed."""

import subprocess
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from ci import sdk_tags

ROOT_MODULE = "github.com/tarantool/tt/sdk/v3"

# The go.mod of every SDK module and of the root, as the release of v3.0.0
# leaves them: the modules at v3.0.0, the root requiring them.
SDK = {
    "sdk": {"require": []},
    "sdk/build": {"require": []},
    "sdk/connect": {"require": []},
    "sdk/cluster": {"require": ["sdk/connect"]},
    "sdk/backup": {"require": []},
    "sdk/dial": {"require": []},
    "sdk/fetch": {"require": []},
    "sdk/integrity": {"require": []},
    "sdk/tuple": {"require": []},
}


def module_path(directory):
    return f"github.com/tarantool/tt/{directory}/v3"


class Tree:
    """A checkout of tt with the modules of the SDK and a root go.mod."""

    def __init__(self, temporary, modules=None):
        self.root = Path(temporary)
        # A runner has no git identity, and a commit or a tag needs one.
        self.identity = [
            "-c",
            "user.name=tt",
            "-c",
            "user.email=tt@example.com",
        ]
        self.modules = dict(SDK if modules is None else modules)
        (self.root / "go.mod").write_text(
            "module github.com/tarantool/tt/v3\n\ngo 1.27.4\n\nrequire (\n"
            + "".join(f"\t{module_path(directory)} v3.0.0\n" for directory in self.modules)
            + ")\n",
        )
        for directory, spec in self.modules.items():
            path = self.root / directory / "go.mod"
            path.parent.mkdir(parents=True, exist_ok=True)
            (path.parent / "module.go").write_text(f"package {path.parent.name}\n")
            path.write_text(
                f"module {module_path(directory)}\n\ngo 1.27.0\n\nrequire (\n"
                + "".join(f"\t{module_path(required)} v3.0.0\n" for required in spec["require"])
                + ")\n",
            )
        # A module of tt that requires the SDK, as modules/aeon does.
        (self.root / "modules" / "aeon").mkdir(parents=True)
        (self.root / "modules" / "aeon" / "go.mod").write_text(
            "module github.com/tarantool/tt/modules/aeon\n\ngo 1.27.0\n\nrequire (\n"
            f"\t{module_path('sdk')} v3.0.0\n"
            "\tgithub.com/stretchr/testify v1.11.1\n"
            ")\n",
        )
        # A fixture of a test: no module of tt, and not looked at.
        (self.root / "testdata" / "fixture").mkdir(parents=True)
        (self.root / "testdata" / "fixture" / "go.mod").write_text(
            f"module example.com/fixture\n\nrequire {module_path('sdk')} v0.0.0\n",
        )

    def git(self, *args, cwd=None):
        result = subprocess.run(
            ("git", *self.identity, *args),
            cwd=cwd or self.root,
            capture_output=True,
            text=True,
        )
        if result.returncode:
            raise subprocess.CalledProcessError(
                result.returncode,
                args,
                result.stdout,
                result.stderr,
            )
        return result.stdout

    def commit(self, message="change"):
        self.git("add", "-A")
        self.git(
            "-c",
            "user.name=tt",
            "-c",
            "user.email=tt@example.com",
            "commit",
            "-qm",
            message,
        )

    def tag(self, name):
        self.git(
            "-c",
            "user.name=tt",
            "-c",
            "user.email=tt@example.com",
            "tag",
            "-a",
            name,
            "-m",
            name,
        )


class ModulesTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.tree = Tree(temporary.name)

    def test_modules_are_found_by_their_go_mod_files(self):
        found = sdk_tags.modules(self.tree.root)
        self.assertEqual(set(found), set(SDK))
        self.assertEqual(found["sdk"].path, ROOT_MODULE)
        self.assertEqual(found["sdk/build"].path, "github.com/tarantool/tt/sdk/build/v3")
        self.assertEqual(found["sdk"].major, 3)
        self.assertEqual(found["sdk/build"].tag("v3.0.1"), "sdk/build/v3.0.1")

    def test_a_module_without_a_major_version_stops_the_run(self):
        path = self.tree.root / "sdk" / "tuple" / "go.mod"
        path.write_text(path.read_text().replace("/v3", ""))
        with self.assertRaises(SystemExit):
            sdk_tags.modules(self.tree.root)


class PlanTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.tree = Tree(temporary.name)

    def test_the_first_release_gives_every_module_its_version(self):
        self.assertEqual(
            set(sdk_tags.planned("v3.0.0", self.tree.root).values()),
            {"v3.0.0"},
        )

    def test_a_new_minor_gives_every_module_its_own(self):
        self.assertEqual(
            set(sdk_tags.planned("v3.1.0", self.tree.root).values()),
            {"v3.1.0"},
        )

    def test_the_patch_of_a_bumped_module_stays(self):
        self.tree.git("init", "-q")
        for name in ("go.mod", "sdk/go.mod", "sdk/cluster/go.mod"):
            path = self.tree.root / name
            path.write_text(
                path.read_text().replace("sdk/connect/v3 v3.0.0", "sdk/connect/v3 v3.0.1"),
            )
        self.tree.commit()
        versions = sdk_tags.planned("v3.0.0", self.tree.root)
        self.assertEqual(versions["sdk/connect"], "v3.0.1")

    def test_a_pre_release_gives_every_module_its_suffix(self):
        for tag in ("v3.1.0-rc.1", "v3.0.0-rc.2"):
            with self.subTest(tag=tag):
                self.assertEqual(set(sdk_tags.planned(tag, self.tree.root).values()), {tag})

    def test_a_pre_release_of_a_patch_is_refused(self):
        with self.assertRaises(SystemExit):
            sdk_tags.planned("v3.0.1-rc.1", self.tree.root)

    def test_a_pre_release_ignores_the_patch_of_the_go_mod(self):
        root = self.tree.root
        path = root / "go.mod"
        path.write_text(
            path.read_text().replace(
                f"{module_path('sdk/connect')} v3.0.0",
                f"{module_path('sdk/connect')} v3.0.1",
            ),
        )
        versions = sdk_tags.planned("v3.0.0-rc.1", root)
        self.assertEqual(versions["sdk/connect"], "v3.0.0-rc.1")

    def test_another_release_tag_is_refused(self):
        with self.assertRaises(SystemExit):
            sdk_tags.planned("sdk/v3.0.0", self.tree.root)


class BumpTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.tree = Tree(temporary.name)

    def test_bump_writes_the_planned_version_everywhere(self):
        root = self.tree.root
        # The module sdk/connect changed in this release, so its patch is the
        # one its version in the go.mod names: the go.mod files that require
        # it, the root one and the module of tt among them, follow when bump
        # runs. Here only the go.mod of the root already names the new patch,
        # and bump leaves it where it is.
        root_go_mod = root / "go.mod"
        changed = root_go_mod.read_text().replace(
            f"{module_path('sdk/connect')} v3.0.0",
            f"{module_path('sdk/connect')} v3.0.1",
        )
        root_go_mod.write_text(changed)
        self.assertEqual(sdk_tags.planned("v3.0.0", root)["sdk/connect"], "v3.0.1")
        # sdk/cluster requires sdk/connect too, and still names v3.0.0.
        self.assertEqual(sdk_tags.bump("v3.0.0", root), ["sdk/cluster/go.mod"])
        self.assertEqual(root_go_mod.read_text(), changed)
        self.assertEqual(sdk_tags.check_modules("v3.0.0", root), [])

    def test_bump_follows_a_module_whose_patch_moved(self):
        root = self.tree.root
        # The go.mod of the module itself moved its version: bump writes the
        # new version into every go.mod that requires it.
        connect = root / "sdk" / "connect" / "go.mod"
        connect.write_text(
            connect.read_text().replace(
                f"{module_path('sdk/connect')} v3.0.0",
                f"{module_path('sdk/connect')} v3.0.1",
            ),
        )
        versions = sdk_tags.planned("v3.0.0", root)
        self.assertIn(versions["sdk/connect"], {"v3.0.0", "v3.0.1"})

    def test_check_sdk_reports_a_go_mod_left_behind(self):
        (self.tree.root / "modules" / "aeon" / "go.mod").write_text(
            (self.tree.root / "modules" / "aeon" / "go.mod")
            .read_text()
            .replace(f"{module_path('sdk')} v3.0.0", f"{module_path('sdk')} v3.0.1"),
        )
        problems = sdk_tags.check_modules("v3.0.0", self.tree.root)
        self.assertEqual(len(problems), 1)
        self.assertIn("modules/aeon/go.mod", problems[0])


class ReleaseTest(unittest.TestCase):
    def setUp(self):
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.tree = Tree(temporary.name)
        self.tree.git("init", "-q")
        self.tree.git("checkout", "-q", "-B", "v3")
        self.tree.commit("the release")
        self.tree.tag("v3.0.0")
        self.tree.git("tag", "-d", "v3.0.0")

    def create(self):
        with patch.object(sdk_tags, "git", side_effect=self.tree.git):
            return sdk_tags.create("v3.0.0", push=False, root=self.tree.root)

    def check(self):
        with patch.object(sdk_tags, "git", side_effect=self.tree.git):
            return sdk_tags.check_release("v3.0.0", root=self.tree.root)

    def test_the_tags_are_made_once_and_check_out(self):
        self.assertEqual(
            self.create(),
            [
                "sdk/backup/v3.0.0",
                "sdk/build/v3.0.0",
                "sdk/cluster/v3.0.0",
                "sdk/connect/v3.0.0",
                "sdk/dial/v3.0.0",
                "sdk/fetch/v3.0.0",
                "sdk/v3.0.0",
                "sdk/integrity/v3.0.0",
                "sdk/tuple/v3.0.0",
            ],
        )
        self.assertEqual(self.create(), [])
        self.assertEqual(self.check(), [])

    def test_a_release_without_the_tags_fails(self):
        problems = self.check()
        self.assertEqual(len(problems), len(SDK))
        self.assertIn("is not a tag", problems[0])
        self.assertIn("sdk/connect", " ".join(problems))

    def test_a_module_changed_after_its_tag_fails(self):
        self.create()
        path = self.tree.root / "sdk" / "connect" / "module.go"
        path.write_text("package connect // changed\n")
        self.tree.commit("a change")
        problems = self.check()
        # The tree of sdk/connect changed, and of sdk, which has no directory
        # of its own to tell apart from sdk/<name>: both are reported.
        self.assertEqual(
            sorted(problem.split(":")[0] for problem in problems),
            ["sdk", "sdk/connect"],
        )
        self.assertIn(
            "differs from sdk/connect/v3.0.0",
            " ".join(problems),
        )

    def test_a_pre_release_tags_the_go_mod_of_the_release(self):
        # The rc.1 of a new minor: every module names the suffix, even the
        # one that changed nothing, and the tags carry it.
        root = self.tree.root
        for path in [root / "go.mod", root / "modules" / "aeon" / "go.mod"] + [
            root / directory / "go.mod" for directory in SDK
        ]:
            text = path.read_text()
            for directory in SDK:
                old = f"{module_path(directory)} v3.0.0"
                text = text.replace(old, f"{module_path(directory)} v3.0.0-rc.1")
            path.write_text(text)
        self.assertEqual(sdk_tags.check_modules("v3.0.0-rc.1", root), [])
        self.tree.commit("the pre-release")
        self.tree.tag("v3.0.0-rc.1")
        with patch.object(sdk_tags, "git", side_effect=self.tree.git):
            tags = sdk_tags.create("v3.0.0-rc.1", push=False, root=root)
            self.assertIn("sdk/backup/v3.0.0-rc.1", tags)
            self.assertIn("sdk/v3.0.0-rc.1", tags)
            self.assertEqual(sdk_tags.check_release("v3.0.0-rc.1", root=root), [])

    def rewrite(self, old, new):
        root = self.tree.root
        for path in [root / "go.mod"] + [root / directory / "go.mod" for directory in SDK]:
            text = path.read_text()
            for directory in SDK:
                current = f"{module_path(directory)} {old}"
                text = text.replace(current, f"{module_path(directory)} {new}")
            path.write_text(text)

    def test_the_two_versions_of_a_minor_do_not_stand_for_each_other(self):
        # The SDK of rc.1 and the one of the stable release are tagged apart:
        # neither version passes the check of the other, whatever the go.mod.
        # The paths gain the major version in a commit of their own, and the
        # versions of the pre-release follow.
        self.rewrite("v3.0.0", "v3.0.0-rc.1")
        self.tree.commit("the pre-release")
        self.tree.tag("v3.0.0-rc.1")
        self.tree.git("branch", "-f", "pre", "HEAD")
        with patch.object(sdk_tags, "git", side_effect=self.tree.git):
            # The stable release needs its bump, and its tags.
            problems = sdk_tags.check_release("v3.0.0", root=self.tree.root)
            self.assertEqual(len(problems), len(SDK))
            self.assertIn("requires the pre-release v3.0.0-rc.1", problems[0])
        self.rewrite("v3.0.0-rc.1", "v3.0.0")
        self.tree.commit("the stable release")
        self.tree.git("branch", "-f", "stable", "HEAD")
        with patch.object(sdk_tags, "git", side_effect=self.tree.git):
            for tag in sdk_tags.create("v3.0.0", push=False, root=self.tree.root):
                self.assertIn("/v3.0.0", tag)
        self.tree.tag("v3.0.0")
        with patch.object(sdk_tags, "git", side_effect=self.tree.git):
            self.assertEqual(sdk_tags.check_release("v3.0.0", root=self.tree.root), [])
            # And the pre-release does not pass on the go.mod of the stable
            # one: its tags carry the other version.
            problems = sdk_tags.check_release("v3.0.0-rc.1", root=self.tree.root)
            self.assertEqual(len(problems), len(SDK))
            self.assertIn("and the pre-release v3.0.0-rc.1 tags it v3.0.0-rc.1", problems[0])

    def test_a_go_mod_of_another_release_fails(self):
        self.create()
        path = self.tree.root / "go.mod"
        text = path.read_text().replace(
            f"{module_path('sdk')} v3.0.0",
            f"{module_path('sdk')} v3.1.0",
        )
        path.write_text(text)
        self.tree.commit("another minor")
        problems = self.check()
        self.assertTrue(
            [problem for problem in problems if problem.startswith("sdk: go.mod requires v3.1.0")],
            problems,
        )


if __name__ == "__main__":
    unittest.main()
