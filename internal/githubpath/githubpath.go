// Package githubpath recognises the directories GitHub reserves for workflow
// and action definitions.
//
// Several detectors need the same answer: the GitHub Actions detector claims
// the YAML files in those directories, and the other YAML detectors must leave
// them alone whatever the files are called.
package githubpath

import "strings"

// IsReserved reports whether filePath lies under .github/workflows/ or
// .github/actions/, case-insensitively and at any depth in the tree.
// filePath is slash-separated.
func IsReserved(filePath string) bool {
	lower := strings.ToLower(filePath)
	return strings.Contains(lower, ".github/workflows/") ||
		strings.Contains(lower, ".github/actions/")
}
