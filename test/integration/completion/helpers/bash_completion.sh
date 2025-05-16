#!/usr/bin/env bash
# Bash helper script for testing completions.
# It sources the tt completion script and then simulates the completion environment
# by setting COMP_LINE, COMP_POINT, COMP_WORDS, COMP_CWORD and calling
# the main tt completion function.

# Placeholder for the path to the tt completion script generated for the test.
TT_COMPLETION_SCRIPT_PATH="$1"
# Placeholder for the command line to get completions for.
LINE_TO_COMPLETE="$2"

if [ -r /usr/share/bash-completion/bash_completion ]; then
    source /usr/share/bash-completion/bash_completion
else
    echo "HELPER_ERROR: Bash completion system not found." >&2
    exit 1
fi

if [[ -f "$TT_COMPLETION_SCRIPT_PATH" ]]; then
    source "$TT_COMPLETION_SCRIPT_PATH"
else
    echo "HELPER_ERROR: Completion script not found at $TT_COMPLETION_SCRIPT_PATH" >&2
    exit 1
fi

COMP_LINE="$LINE_TO_COMPLETE"
COMP_POINT="${#COMP_LINE}"

# Attempt to find the main 'tt' completion function name.
TT_MAIN_COMP_FUNC=$(complete -p tt 2>/dev/null | sed 's/.*-F \([^ ]*\) .*/\1/')
if [[ -z "$TT_MAIN_COMP_FUNC" ]]; then
    TT_MAIN_COMP_FUNC="_tt_main" # Default fallback.
    if ! declare -F "$TT_MAIN_COMP_FUNC" > /dev/null; then
        FOUND_FUNC=$(declare -F | grep -oP '_tt[[:alnum:]_]*' | head -n 1)
        if [[ -n "$FOUND_FUNC" ]]; then
            TT_MAIN_COMP_FUNC=$FOUND_FUNC
        else
            echo "HELPER_ERROR: Main tt completion function not found after sourcing." >&2
            exit 1
        fi
    fi
fi

read -ra COMP_WORDS <<< "$COMP_LINE"

if [[ "$COMP_LINE" == *" " ]]; then
    COMP_CWORD=${#COMP_WORDS[@]}
    COMP_WORDS+=("")
else
    COMP_CWORD=$((${#COMP_WORDS[@]} - 1))
fi

if declare -F "$TT_MAIN_COMP_FUNC" > /dev/null; then
    "$TT_MAIN_COMP_FUNC"
    for item in "${COMPREPLY[@]}"; do
        echo "$item"
    done
else
    echo "HELPER_ERROR: Main tt completion function '$TT_MAIN_COMP_FUNC' is not callable." >&2
    exit 1
fi
