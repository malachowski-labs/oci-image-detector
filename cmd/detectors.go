package cmd

import (
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/dockerfile"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/generic"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/githubactions"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/helm"
	"github.com/malachowski-labs/oci-image-detector/internal/adapter/terraform"
	"github.com/malachowski-labs/oci-image-detector/internal/port"
	"github.com/malachowski-labs/oci-image-detector/internal/registry"
)

// builtinDetectors returns the registry of every detector shipped with the
// binary. Adding a detector means adding one line here; the order of the lines
// is irrelevant because each detector declares its own tier.
func builtinDetectors() (*registry.Registry, error) {
	reg := registry.New()

	for _, d := range []port.Detector{
		dockerfile.New(),
		generic.New(),
		githubactions.New(),
		helm.New(),
	} {
		if err := reg.Register(d); err != nil {
			return nil, err
		}
	}

	for _, d := range []port.DirectoryAwareDetector{
		terraform.New(),
	} {
		if err := reg.RegisterDir(d); err != nil {
			return nil, err
		}
	}

	return reg, nil
}
