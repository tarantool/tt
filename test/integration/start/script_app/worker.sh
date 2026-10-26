#!/bin/sh
# The external process of the application: it reports how it runs, leaves
# a state file in its working directory, then waits for a signal, reporting
# each one, and stops on SIGTERM.
echo "worker started: $* $WORKER_GREETING in $PWD"
echo "$*" > worker.state
trap 'echo "worker got TERM"; exit 0' TERM
trap 'echo "worker got HUP"' HUP
while :; do
	sleep 1 &
	wait $!
done
