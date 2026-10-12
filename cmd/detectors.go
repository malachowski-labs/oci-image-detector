package cmd

import (
	// The built-in detectors register themselves with the default registry
	// from their init functions. Importing a detector package here is all it
	// takes to ship it in the binary; the order of the imports is irrelevant
	// because each detector declares its own tier.
	_ "github.com/malachowski-labs/oci-image-detector/internal/adapter/compose"
	_ "github.com/malachowski-labs/oci-image-detector/internal/adapter/dockerfile"
	_ "github.com/malachowski-labs/oci-image-detector/internal/adapter/generic"
	_ "github.com/malachowski-labs/oci-image-detector/internal/adapter/githubactions"
	_ "github.com/malachowski-labs/oci-image-detector/internal/adapter/helm"
	_ "github.com/malachowski-labs/oci-image-detector/internal/adapter/kubernetes"
	_ "github.com/malachowski-labs/oci-image-detector/internal/adapter/terraform"
)
