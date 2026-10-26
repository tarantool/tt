#!/bin/sh
# The external process of the application installed in the system scope: it
# reports how it runs, then waits for a signal, and stops on SIGTERM. With
# CRASH_AFTER set, it fails on its own after that many seconds; with
# IGNORE_TERM set, it ignores SIGTERM and is to be killed.
echo "worker started: $* in $PWD"
if [ -n "$IGNORE_TERM" ]; then
	trap 'echo "worker ignores TERM"' TERM
else
	trap 'echo "worker got TERM"; exit 0' TERM
fi
if [ -n "$CRASH_AFTER" ]; then
	sleep "$CRASH_AFTER" &
	wait $!
	echo "worker crashes"
	exit 3
fi
while :; do
	sleep 1 &
	wait $!
done
