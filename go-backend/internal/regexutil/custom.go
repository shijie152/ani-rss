// Package regexutil translates the small Java/RE2 compatibility gap needed by
// user-provided episode expressions while preserving Java capture indexes.
package regexutil

import (
	"errors"
	"regexp"
	"strings"
)

var errCaptureIndex = errors.New("custom episode capture index is invalid")

// CompileCapturePattern replaces Java non-capturing groups with ordinary Go
// groups, then maps the requested Java capture index to the resulting Go
// index. This keeps the configured customEpisodeGroupIndex stable even when a
// non-capturing group appears before the episode capture.
func CompileCapturePattern(expression string, javaIndex int) (*regexp.Regexp, int, error) {
	if javaIndex < 1 {
		return nil, 0, errCaptureIndex
	}
	var captures []int
	nonCaptures := []int{}
	inClass, escaped := false, false
	for index := 0; index < len(expression); index++ {
		char := expression[index]
		if escaped {
			escaped = false
			continue
		}
		if char == '\\' {
			escaped = true
			continue
		}
		if char == '[' {
			inClass = true
			continue
		}
		if char == ']' && inClass {
			inClass = false
			continue
		}
		if char != '(' || inClass {
			continue
		}
		if strings.HasPrefix(expression[index:], "(?:") {
			nonCaptures = append(nonCaptures, index)
			continue
		}
		if index+1 < len(expression) && expression[index+1] == '?' {
			// Go's named capture spelling is also a capture. Inline flags and
			// other (?...) constructs do not change capture numbering.
			if strings.HasPrefix(expression[index:], "(?P<") || strings.HasPrefix(expression[index:], "(?<") {
				captures = append(captures, index)
			}
			continue
		}
		captures = append(captures, index)
	}
	if javaIndex > len(captures) {
		return nil, 0, errCaptureIndex
	}
	target := captures[javaIndex-1]
	shift := 0
	for _, position := range nonCaptures {
		if position < target {
			shift++
		}
	}
	compiled, err := regexp.Compile(strings.ReplaceAll(expression, "(?:", "("))
	if err != nil {
		return nil, 0, err
	}
	return compiled, javaIndex + shift, nil
}
