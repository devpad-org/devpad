package agentbin

import (
	_ "embed"
)

// Version is set at build time via -ldflags to match the agent binary's version.
var Version = "dev"

// Binary contains the compiled devpad-agent binary for the host architecture.
// It is embedded at build time and can be copied into workspace containers
// to update the agent without rebuilding the Docker image.
//
//go:embed devpad-agent
var Binary []byte
