package githubpath_test

import (
	"testing"

	"github.com/malachowski-labs/oci-image-detector/internal/githubpath"
)

func TestIsReserved(t *testing.T) {
	cases := []struct {
		path string
		want bool
	}{
		{".github/workflows/ci.yml", true},
		{".github/actions/build/action.yaml", true},
		{"services/api/.github/workflows/ci.yml", true},
		{".GitHub/Workflows/CI.yml", true},
		{".github/dependabot.yml", false},
		{"workflows/ci.yml", false},
		{"chart/values.yaml", false},
	}

	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			if got := githubpath.IsReserved(tc.path); got != tc.want {
				t.Errorf("IsReserved(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
