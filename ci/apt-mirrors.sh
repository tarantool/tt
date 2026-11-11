#!/bin/sh
# Make apt fall back to mirrors outside ubuntu.com when the archive of
# Canonical does not answer: on 2026-11-11 every apt-get of the GitHub
# runners failed for hours on "Temporary failure resolving
# 'archive.ubuntu.com'" (actions/runner-images#15296, #15297) while the
# rest of the Internet resolved it. apt checks every index against the keys
# of the Ubuntu archive, so a mirror cannot change a package; at worst it is
# a few hours behind.
#
# The archive and the security pocket are read through a mirror list, the
# way the x86 runners of GitHub read them already (Azure's mirror first,
# then the archive): the mirrors below are appended to it, or follow the
# archive in a list of their own elsewhere - the arm64 runners and the
# ubuntu image of ci/Dockerfile. apt retries a failed fetch, then moves to
# the next mirror.
#
# Run as root, on Ubuntu. Usage: apt-mirrors.sh
set -eu

list=/etc/apt/apt-mirrors.txt

case "$(dpkg --print-architecture)" in
amd64)
	archive=http://archive.ubuntu.com/ubuntu/
	mirrors="http://mirrors.edge.kernel.org/ubuntu/ http://mirror.yandex.ru/ubuntu/"
	;;
arm64)
	archive=http://ports.ubuntu.com/ubuntu-ports/
	mirrors="http://mirror.yandex.ru/ubuntu-ports/ http://mirrors.ocf.berkeley.edu/ubuntu-ports/"
	;;
*)
	exit 0
	;;
esac

if [ ! -s "$list" ]; then
	printf '%s\tpriority:1\n' "$archive" > "$list"
fi

priority=$(wc -l < "$list")
for mirror in $mirrors; do
	if grep -q "^$mirror" "$list"; then
		continue
	fi

	priority=$((priority + 1))
	printf '%s\tpriority:%d\n' "$mirror" "$priority" >> "$list"
done

for sources in /etc/apt/sources.list.d/ubuntu.sources /etc/apt/sources.list; do
	if [ -f "$sources" ]; then
		sed -i -E \
			"s#https?://(azure\.)?(archive|ports|security)\.ubuntu\.com/ubuntu(-ports)?/#mirror+file:$list#g" \
			"$sources"
	fi
done

# apt tries a mirror Acquire::Retries times before the next one, and by
# default waits longer before each try: a mirror that does not answer then
# costs some 15 seconds for each of the dozens of indexes, and an update
# that takes 5 seconds through a mirror alone does not end in 5 minutes. The
# tries stay, without the wait.
printf 'Acquire::Retries "3";\nAcquire::Retries::Delay "false";\n' > /etc/apt/apt.conf.d/80-retries
