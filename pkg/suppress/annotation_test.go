package suppress_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/pkg/suppress"
)

func TestIsIgnoredLine(t *testing.T) {
	cases := []struct {
		line string
		want bool
	}{
		{`// "localhost:5000/image:v1" -> "localhost:5000/image"  // oci-image-detector:ignore`, true},
		{`FROM nginx:latest  # oci-image-detector:ignore`, true},
		{`image = "gcr.io/proj/app:v1"  # oci-image-detector:ignore`, true},
		{`ghcr.io/org/app:v1`, false},
		{`# oci-image-detector:ignoreXYZ is not a match`, false},
		{`oci-image-detector:ignore`, true}, // annotation without leading comment marker
	}
	for _, tc := range cases {
		t.Run(tc.line, func(t *testing.T) {
			if got := suppress.IsIgnoredLine(tc.line); got != tc.want {
				t.Errorf("IsIgnoredLine(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}
