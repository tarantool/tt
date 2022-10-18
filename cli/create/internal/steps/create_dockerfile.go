package steps

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/apex/log"
	create_ctx "github.com/tarantool/tt/cli/create/context"
	"github.com/tarantool/tt/cli/create/internal/app_template"
	"github.com/tarantool/tt/cli/docker"
)

// CreateDockerfile represents create docker file step.
type CreateDockerfile struct {
}

// Run creates a docker file in application directory.
func (CreateDockerfile) Run(createCtx *create_ctx.CreateCtx,
	templateCtx *app_template.TemplateCtx) error {
	// Check if base Dockerfile already exists in application.
	buildDockerfiles := docker.GetDefaultBaseBuildDockerfiles()
	for _, dockerFile := range buildDockerfiles {
		fullPath := filepath.Join(templateCtx.AppPath, dockerFile)
		fileInfo, err := os.Stat(fullPath)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("Failed to get info of %s: %s", fullPath, err)
			}
		} else {
			if !fileInfo.Mode().IsRegular() {
				return fmt.Errorf("%s is not a regular file", fullPath)
			}
			log.Debugf("Dockerfile %s exists.", fullPath)
			return nil
		}
	}

	// Need to create new base Dockerfile for building.
	buildDockerfile := filepath.Join(templateCtx.AppPath, buildDockerfiles[0])
	if err := os.WriteFile(buildDockerfile, docker.DefaultBuildDockerfileContent,
		0644); err != nil {
		return fmt.Errorf("Error writing %s: %s", buildDockerfile, err)
	}

	return nil
}
