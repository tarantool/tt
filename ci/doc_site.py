"""Build or serve the documentation site, in English and in its translations.

Zensical builds a site in one language. The English one is zensical.toml as
it is, in site/. A translation, say ru, is a site of its own in site/ru/,
built from a copy of doc/ in which doc/<page>.ru.md takes the place of
doc/<page>.md, so that a page with no translation is the English one, and
from the [project] of zensical.toml with the changes of zensical.ru.toml:
its site_description, and its nav, which maps the titles the nav sets to
their translations. Each zensical.<locale>.toml beside zensical.toml makes a
translation. The copy, its config and its cache are in .zensical/ru/.

Zensical checks the links of every page and the anchors of the English site.
In a translated site, an English page that has no translation links to the
translations, which may not have caught up with the section it links to yet,
so Zensical does not check the anchors there, and this script does: an anchor
a translation lacks fails the build when a translated page links to it, and is
only a warning when an English page does, until the translation catches up.

Zensical builds the pages the nav omits, and this script fails on them.
"""

import argparse
import copy
import json
import os
import re
import shutil
import signal
import subprocess
import sys
import time
import tomllib
from html.parser import HTMLParser
from pathlib import Path
from urllib.parse import unquote, urldefrag, urljoin, urlsplit

import pathspec

ROOT = Path(__file__).resolve().parents[1]
CONFIG = ROOT / "zensical.toml"
WORK = ROOT / ".zensical"
TRANSLATION = re.compile(r"^(?P<stem>.+)\.(?P<locale>[a-z]{2})\.md$")
LOCALE_CONFIG = re.compile(r"^zensical\.(?P<locale>[a-z]{2})\.toml$")
LOCALE_SETTINGS = {"site_description", "nav"}


class Failure(Exception):
    pass


def locale_configs():
    """Return the config files of the translations by locale."""
    paths = {}
    for path in sorted(CONFIG.parent.glob("zensical.*.toml")):
        match = LOCALE_CONFIG.match(path.name)
        if match:
            paths[match["locale"]] = path
    return paths


def load():
    """Return zensical.toml, with the changes of the translations by locale
    in "locales"."""
    with CONFIG.open("rb") as file:
        config = tomllib.load(file)
    config["locales"] = {}
    for locale, path in locale_configs().items():
        with path.open("rb") as file:
            changes = tomllib.load(file)
        unknown = sorted(set(changes) - LOCALE_SETTINGS)
        if unknown:
            raise Failure(
                f"{path.name} sets {', '.join(unknown)}; a translation sets only"
                f" {', '.join(sorted(LOCALE_SETTINGS))}",
            )
        config["locales"][locale] = changes
    return config


def patterns(text):
    return pathspec.GitIgnoreSpec.from_lines(text.splitlines())


def page_url(page):
    """Return the URL of a page relative to the site, as Zensical makes it."""
    stem = page.removesuffix(".md")
    if stem == "index" or stem.endswith("/index"):
        return stem.removesuffix("index")
    return stem + "/"


def sources(config):
    """Return the pages of the site and, by locale, the translations of them."""
    project = config["project"]
    docs = ROOT / project["docs_dir"]
    excluded = patterns(project.get("exclude_docs", ""))
    pages = []
    translations = {locale: {} for locale in config["locales"]}
    for path in sorted(docs.rglob("*.md")):
        name = path.relative_to(docs).as_posix()
        match = TRANSLATION.match(path.name)
        if match and match["locale"] in translations:
            page = path.with_name(match["stem"] + ".md").relative_to(docs).as_posix()
            translations[match["locale"]][page] = name
        elif not excluded.match_file(name):
            pages.append(name)
    # A translation of a page that is gone is not built: mage docs:stale
    # reports it.
    for translated in translations.values():
        for page in set(translated) - set(pages):
            del translated[page]
    return pages, translations


def nav_pages(nav):
    for item in nav:
        values = item.values() if isinstance(item, dict) else [item]
        for value in values:
            if isinstance(value, str):
                yield value
            else:
                yield from nav_pages(value)


def translate_nav(nav, titles):
    result = []
    for item in nav:
        if isinstance(item, dict):
            item = {
                titles.get(title, title): value
                if isinstance(value, str)
                else translate_nav(value, titles)
                for title, value in item.items()
            }
        result.append(item)
    return result


def check_nav(config, pages):
    project = config["project"]
    hidden = patterns(project.get("not_in_nav", ""))
    listed = set(nav_pages(project["nav"]))
    problems = [
        f"{project['docs_dir']}/{page}: not in the nav of zensical.toml"
        for page in pages
        if page not in listed and not hidden.match_file(page)
    ]
    problems += [
        f"the nav of zensical.toml lists {page}, which {project['docs_dir']}/ does not have"
        for page in sorted(listed - set(pages))
    ]
    for problem in problems:
        print(problem, file=sys.stderr)
    return not problems


def zensical(command, config, *args):
    subprocess.run(
        [sys.executable, "-m", "zensical", command, "--config-file", str(config), *args],
        check=True,
    )


def prepare(config, locale, translated):
    """Write the project of the site in locale and return its config file."""
    project = config["project"]
    changes = config["locales"][locale]
    work = WORK / locale
    docs = work / "doc"
    shutil.rmtree(docs, ignore_errors=True)
    shutil.copytree(
        ROOT / project["docs_dir"],
        docs,
        ignore=lambda _, names: [name for name in names if TRANSLATION.match(name)],
    )
    for page, translation in translated.items():
        shutil.copy2(ROOT / project["docs_dir"] / translation, docs / page)

    site = copy.deepcopy(project)
    site["docs_dir"] = "doc"
    site["site_dir"] = "site"
    site["site_url"] = project["site_url"].rstrip("/") + f"/{locale}/"
    if "site_description" in changes:
        site["site_description"] = changes["site_description"]
    site["nav"] = translate_nav(project["nav"], changes.get("nav", {}))
    theme = site.setdefault("theme", {})
    theme["language"] = locale
    if "custom_dir" in theme:
        theme["custom_dir"] = os.path.relpath(ROOT / theme["custom_dir"], work)
    # check_anchors checks them.
    site.setdefault("validation", {})["invalid_link_anchors"] = False
    # The pages doc/overrides/partials/actions.html sends to their
    # translation for an edit.
    site.setdefault("extra", {})["translated"] = sorted(page_url(page) for page in translated)

    # JSON is YAML, which Zensical reads in the format of mkdocs.yml.
    path = work / "mkdocs.yml"
    path.write_text(json.dumps(site, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return path


def build_translation(config, locale, translated, pages, strict):
    path = prepare(config, locale, translated)
    try:
        zensical("build", path, *(["--strict"] if strict else []))
    except subprocess.CalledProcessError:
        print(
            f"The {locale} site is built from {path.parent.relative_to(ROOT)}/doc/, where"
            f" page.md is page.{locale}.md of doc/ when the page has a translation.",
            file=sys.stderr,
        )
        raise
    site = ROOT / config["project"].get("site_dir", "site") / locale
    shutil.rmtree(site, ignore_errors=True)
    shutil.move(WORK / locale / "site", site)
    return check_anchors(config, site, locale, translated, pages)


class Anchors(HTMLParser):
    """The ids of an HTML page and the links of it."""

    def __init__(self):
        super().__init__()
        self.ids = set()
        self.links = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if attrs.get("id"):
            self.ids.add(attrs["id"])
        if tag == "a" and attrs.get("name"):
            self.ids.add(attrs["name"])
        if tag == "a" and attrs.get("href"):
            self.links.append(attrs["href"])


def check_anchors(config, site, locale, translated, pages):
    """Check the anchors of the links between the pages of a translated site.

    Return whether no translated page links to an anchor the site lacks.
    """
    docs = config["project"]["docs_dir"]
    parsed = {}
    for path in site.rglob("*.html"):
        parser = Anchors()
        parser.feed(path.read_text(encoding="utf-8"))
        parsed[path.relative_to(site).as_posix()] = parser
    by_url = {page_url(page): page for page in pages}

    def source(page):
        return f"{docs}/{translated.get(page, page)}"

    broken = 0
    for name, parser in sorted(parsed.items()):
        page = by_url.get(name.removesuffix("index.html"))
        if page is None:
            continue
        for href in parser.links:
            target, fragment = urldefrag(href)
            parts = urlsplit(target)
            if not fragment or parts.scheme or parts.netloc or target.startswith("/"):
                continue
            file = urljoin("/" + name, target).lstrip("/")
            if file == "" or file.endswith("/"):
                file += "index.html"
            # Zensical checks the links to pages.
            if file not in parsed or unquote(fragment) in parsed[file].ids:
                continue
            linked = by_url.get(file.removesuffix("index.html"), file)
            if page in translated:
                broken += 1
                print(
                    f"{source(page)}: links to {linked}#{fragment}, an anchor"
                    f" {source(linked)} does not have",
                    file=sys.stderr,
                )
            else:
                message = (
                    f"{source(page)}, in English in the {locale} site, links to"
                    f" {linked}#{fragment}, an anchor {source(linked)} does not have yet"
                )
                print(message)
                if os.getenv("GITHUB_ACTIONS"):
                    print(f"::warning file={source(linked)}::{message}")
    return broken == 0


def build(config, pages, translations):
    if not check_nav(config, pages):
        raise Failure("the nav of zensical.toml does not match the pages")
    shutil.rmtree(ROOT / config["project"].get("site_dir", "site"), ignore_errors=True)
    zensical("build", CONFIG, "--strict")
    for locale, translated in translations.items():
        if not build_translation(config, locale, translated, pages, strict=True):
            raise Failure(f"broken anchors in the {locale} site")


def snapshot(config):
    """Return the times the sources of the site last changed."""
    files = [
        CONFIG,
        *locale_configs().values(),
        *(ROOT / config["project"]["docs_dir"]).rglob("*"),
    ]
    return {path: path.stat().st_mtime_ns for path in files if path.is_file()}


def serve(config, pages, translations):
    """Serve the site: Zensical serves and rebuilds the English one, with live
    reload; this script rebuilds the translations into it on a change."""
    check_nav(config, pages)
    site = ROOT / config["project"].get("site_dir", "site")
    shutil.rmtree(site, ignore_errors=True)
    # Stops the server below on a kill as on Ctrl-C.
    signal.signal(signal.SIGTERM, lambda *_: sys.exit(128 + signal.SIGTERM))
    server = subprocess.Popen([sys.executable, "-m", "zensical", "serve", "--config-file", CONFIG])
    try:
        while not (site / "sitemap.xml").exists():
            if server.poll() is not None:
                return server.returncode
            time.sleep(0.2)
        built = None
        while server.poll() is None:
            current = snapshot(config)
            missing = any(not (site / locale).exists() for locale in translations)
            if current != built or missing:
                built = current
                config = load()
                pages, translations = sources(config)
                for locale, translated in translations.items():
                    try:
                        build_translation(config, locale, translated, pages, strict=False)
                    except (subprocess.CalledProcessError, OSError) as error:
                        print(f"cannot build the {locale} site: {error}", file=sys.stderr)
            time.sleep(1)
        return server.returncode
    finally:
        server.terminate()
        server.wait()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("command", choices=["build", "serve"])
    args = parser.parse_args()

    try:
        config = load()
        pages, translations = sources(config)
        if args.command == "serve":
            return serve(config, pages, translations)
        build(config, pages, translations)
    except subprocess.CalledProcessError as error:
        return error.returncode
    except Failure as error:
        print(f"The documentation site does not build: {error}.", file=sys.stderr)
        return 1
    except KeyboardInterrupt:
        return 130
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
