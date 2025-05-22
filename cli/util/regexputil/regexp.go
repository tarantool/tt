package regexputil

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/exp/maps"
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
		return renderedStr, fmt.Errorf("missing vars: %s\nin template string: %q",
			strings.Join(maps.Keys(missingVars), ","), templateStr)
	}

	return renderedStr, nil
}
