"""The names of the systems the checks of rocks run on, as an os of a skip
gives them: the rocks catalogue check, test/rocks-catalog.yaml, and the
LuaRocks corpus, test/luarocks-corpus.yaml."""

import platform
import re

SYSTEMS = ("linux", "darwin")
# The distributions rocks.yml checks the rocks on, by the ID of their
# /etc/os-release: an os of a skip names one of them, or one of them and the
# major number of its version, as rocky-8.
DISTROS = ("arch", "debian", "fedora", "gentoo", "rocky", "ubuntu")
_DISTRO = re.compile(rf"(?:{'|'.join(DISTROS)})(?:-\d+)?")


def is_platform(value):
    """Whether an os of a skip may name the value: a system, or a
    distribution alone or with its major version."""
    return value in SYSTEMS or _DISTRO.fullmatch(value) is not None


def platforms(os_release=None):
    """Return the names an os of a skip may give the system the check runs
    on: linux or darwin and, on Linux, the ID of /etc/os-release alone and
    with the major number of its VERSION_ID (debian, debian-12). os_release
    is the content of os-release, read from the system when None."""
    system = platform.system().lower()
    names = {system}
    if system != "linux":
        return names
    if os_release is None:
        try:
            os_release = platform.freedesktop_os_release()
        except OSError:
            return names
    distro = os_release.get("ID", "")
    if distro:
        names.add(distro)
        major = os_release.get("VERSION_ID", "").split(".")[0]
        if major:
            names.add(f"{distro}-{major}")
    return names
