package docker

import (
	_ "embed"
)

//go:embed dockerfile.build.tt
var DefaultBuildDockerfileContent []byte

func GetDefaultBaseBuildDockerfiles() [2]string {
	return [2]string{
		"Dockerfile.build.tt",
		"Dockerfile.build.cartridge",
	}
}
