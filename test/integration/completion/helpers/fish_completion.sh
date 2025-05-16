#!/usr/bin/env fish
# Fish helper script for testing completions.
# It sources the provided completion script and then executes 'complete -C'
# with the command line that needs to be completed.

# Placeholder for the path to the tt completion script generated for the test.
set TT_COMPLETION_SCRIPT_PATH "$argv[1]"
# Placeholder for the command line to get completions for.
set LINE_TO_COMPLETE "$argv[2]"

if test -f "$TT_COMPLETION_SCRIPT_PATH"
    source "$TT_COMPLETION_SCRIPT_PATH"
else
    echo "HELPER_ERROR: Completion script not found at $TT_COMPLETION_SCRIPT_PATH" >&2
    exit 1
end

complete -C"$LINE_TO_COMPLETE"
