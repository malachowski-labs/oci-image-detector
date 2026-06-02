// Package main is the entry point for the oci-image-detector CLI.
package main

import "github.com/malachowski-labs/oci-image-detector/cmd"

// version is injected at build time via -ldflags="-X main.version=<tag>".
// It falls back to "dev" when the binary is built without the flag (e.g. `go run`).
var version = "dev"

func main() {
	cmd.Execute(version)
}
