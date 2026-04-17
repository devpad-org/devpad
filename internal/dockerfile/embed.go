package dockerfile

import (
	_ "embed"
)

// Content contains the workspace Dockerfile, embedded at build time.
// The Makefile copies docker/workspace/Dockerfile here before building.
//
//go:embed Dockerfile
var Content []byte
