package regexputil

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var (
	errMissingVarsInTemplateString = errors.New("missing vars: ")
)

var varPattern = regexp.MustCompile(`{{\s*([^ ]+)\s*}}`)

// ApplyVars replaces '{{ key }}' in str string by a value from the data map.
func ApplyVars(templateStr string, data map[string]string) (string, error) {
	missingVars := make(map[string]bool, 0)
	renderedStr := varPattern.ReplaceAllStringFunc(templateStr, func(varNameStr string) string {
		if subMatches := varPattern.FindStringSubmatch(varNameStr); subMatches != nil {
			if val, found := data[subMatches[1]]; !found {
				missingVars[subMatches[1]] = true
			} else {
				return val
			}
		}
		return varNameStr
	})

	if len(missingVars) > 0 {
		return renderedStr, fmt.Errorf("%w%s\nin template string: %q",
			errMissingVarsInTemplateString,
			strings.Join(slices.Collect(maps.Keys(missingVars)), ","), templateStr)
	}

	return renderedStr, nil
}
