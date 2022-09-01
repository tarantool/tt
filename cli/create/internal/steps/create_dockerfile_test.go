package steps

import (
	"io/ioutil"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/docker"
)

func TestCreateDockerfile(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()
	templateCtx.AppPath = workDir

	createDockerfile := CreateDockerfile{}
	require.NoError(t, createDockerfile.Run(&createCtx, &templateCtx))

	expectedFileName := filepath.Join(workDir, "Dockerfile.build.tt")
	require.FileExists(t, expectedFileName)
	buf, err := os.ReadFile(expectedFileName)
	require.NoError(t, err)
	require.Equal(t, string(docker.DefaultBuildDockerfileContent), string(buf))
}

func TestCreateDockerfileSkipExistingTtFile(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()
	templateCtx.AppPath = workDir

	existingDockerfile := filepath.Join(workDir, "Dockerfile.build.tt")
	require.NoError(t, os.WriteFile(existingDockerfile, []byte(`# comment`), 0644))
	createDockerfile := CreateDockerfile{}
	require.NoError(t, createDockerfile.Run(&createCtx, &templateCtx))

	require.FileExists(t, existingDockerfile)
	buf, err := os.ReadFile(existingDockerfile)
	require.NoError(t, err)
	require.Equal(t, `# comment`, string(buf))
}

func TestCreateDockerfileSkipExistingCartridgeFile(t *testing.T) {
	workDir, err := ioutil.TempDir("", testWorkDirName)
	require.NoError(t, err)
	defer os.RemoveAll(workDir)

	var createCtx cmdcontext.CreateCtx
	templateCtx := NewTemplateContext()
	templateCtx.AppPath = workDir

	existingDockerfile := filepath.Join(workDir, "Dockerfile.build.cartridge")
	require.NoError(t, os.WriteFile(existingDockerfile, []byte(`# comment`), 0644))

	createDockerfile := CreateDockerfile{}
	require.NoError(t, createDockerfile.Run(&createCtx, &templateCtx))

	assert.FileExists(t, existingDockerfile)
	buf, err := os.ReadFile(existingDockerfile)
	require.NoError(t, err)
	require.Equal(t, `# comment`, string(buf))
	// Check Dockerfile.build.tt is not created.
	require.NoFileExists(t, filepath.Join(workDir, "Dockerfile.build.tt"))
}
