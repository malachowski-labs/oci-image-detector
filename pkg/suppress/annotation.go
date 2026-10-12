// Package suppress implements the inline annotation that lets a user silence
// findings on a single line of a scanned file.
//
// It only recognises the annotation; where a comment may sit differs per file
// format, so each detector decides which text to hand over.
package suppress

import "strings"

// Annotation is the inline marker that suppresses detection for a single
// line. When any detector finds this string on a line (or logical instruction),
// all image references on that line are dropped from the output.
//
// Usage examples:
//
//	# Dockerfile
//	FROM nginx:latest  # oci-image-detector:ignore
//
//	# Go / any text file
//	// "localhost:5000/image:v1" -> "localhost:5000/image"  // oci-image-detector:ignore
//
//	# Terraform HCL
//	image = "localhost:5000/example:v1"  # oci-image-detector:ignore
const Annotation = "oci-image-detector:ignore"

// IsIgnoredLine reports whether line carries the inline suppression annotation.
// The annotation must appear as a whole token — it must not be immediately
// followed by a letter, digit, or underscore, so that a longer marker like
// "oci-image-detector:ignoreXYZ" is not treated as a match.
func IsIgnoredLine(line string) bool {
	idx := strings.Index(line, Annotation)
	if idx < 0 {
		return false
	}
	rest := line[idx+len(Annotation):]
	return rest == "" || !isWordChar(rest[0])
}

// isWordChar reports whether b is a letter, digit, or underscore.
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}
