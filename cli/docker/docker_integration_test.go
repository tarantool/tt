//go:build integration_docker

package docker

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func findAndRemoveBuiltImage(t *testing.T, dockerClient *client.Client, expectedTag string) {
	ctx := context.Background()
	imageList, err := dockerClient.ImageList(ctx, types.ImageListOptions{})
	require.NoError(t, err)
	imgFound := false
	for _, img := range imageList {
		for _, imgTag := range img.RepoTags {
			if imgTag == "ubuntu:tt_test" {
				imgFound = true
				dockerClient.ImageRemove(ctx, img.ID, types.ImageRemoveOptions{})
			}
		}
	}
	require.True(t, imgFound)
}

func TestBuildImage(t *testing.T) {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv,
		client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer dockerClient.Close()

	require.NoError(t, buildDockerImage(dockerClient, "ubuntu:tt_test", "testdata", false,
		os.Stdout))
	findAndRemoveBuiltImage(t, dockerClient, "ubuntu:tt_test")
}

func TestBuildImageFail(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, dockerFileName),
		[]byte(`FROM ubuntu:16.04
	COPY /non-existing-file /
	`), 0664))

	dockerClient, err := client.NewClientWithOpts(client.FromEnv,
		client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer dockerClient.Close()

	err = buildDockerImage(dockerClient, "ubuntu:tt_test", tmpDir, false, os.Stdout)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "COPY failed"))
}

func TestBuildImageOutputVerbose(t *testing.T) {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv,
		client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer dockerClient.Close()

	tmpDir := t.TempDir()
	out, err := os.Create(filepath.Join(tmpDir, "out.log"))
	require.NoError(t, err)

	require.NoError(t, buildDockerImage(dockerClient, "ubuntu:tt_test", "testdata", true, out))
	out.Close()
	findAndRemoveBuiltImage(t, dockerClient, "ubuntu:tt_test")

	in, err := os.Open(filepath.Join(tmpDir, "out.log"))
	require.NoError(t, err)
	defer in.Close()
	scanner := bufio.NewScanner(in)
	require.True(t, scanner.Scan())
	require.Equal(t, "Step 1/1 : FROM ubuntu:16.04", scanner.Text())
	require.True(t, scanner.Scan())
	require.True(t, scanner.Scan())
	require.True(t, strings.Contains(scanner.Text(), "Successfully built"))
	require.True(t, scanner.Scan())
	require.Equal(t, "Successfully tagged ubuntu:tt_test", scanner.Text())
}

func TestBuildImageOutput(t *testing.T) {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv,
		client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer dockerClient.Close()

	tmpDir := t.TempDir()
	out, err := os.Create(filepath.Join(tmpDir, "out.log"))
	require.NoError(t, err)

	require.NoError(t, buildDockerImage(dockerClient, "ubuntu:tt_test", "testdata", false, out))
	out.Close()
	findAndRemoveBuiltImage(t, dockerClient, "ubuntu:tt_test")

	in, err := os.Open(filepath.Join(tmpDir, "out.log"))
	require.NoError(t, err)
	defer in.Close()
	scanner := bufio.NewScanner(in)
	require.False(t, scanner.Scan())
}

func checkNoContainers(t *testing.T, imageTag string) {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	require.NoError(t, err)
	defer cli.Close()

	containers, err := cli.ContainerList(ctx, types.ContainerListOptions{
		Latest: true,
		Limit:  1,
	})
	require.NoError(t, err)
	containerFound := false
	for _, container := range containers {
		if container.Image == imageTag {
			containerFound = true
		}
	}
	require.False(t, containerFound)
}

func TestRunContainer(t *testing.T) {
	tmpDir := t.TempDir()

	err := RunContainer(RunOptions{
		BuildCtxDir: "testdata",
		ImageTag:    "ubuntu:tt_test",
		Command:     []string{"touch", "/work/file_from_container"},
		Binds:       []string{fmt.Sprintf("%s:/work", tmpDir)},
	}, os.Stdout)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(tmpDir, "file_from_container"))

	checkNoContainers(t, "ubuntu:tt_test")
}

func TestRunContainerInvalidDockerfile(t *testing.T) {
	tmpDir := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, dockerFileName), []byte("Foo"), 0664))

	err := RunContainer(RunOptions{
		BuildCtxDir: tmpDir,
		ImageTag:    "ubuntu:tt_test",
		Command:     []string{"touch", "/work/file_from_container"},
		Binds:       []string{fmt.Sprintf("%s:/work", tmpDir)},
	}, os.Stdout)
	require.True(t, strings.Contains(err.Error(), "dockerfile parse error"))
	assert.NoFileExists(t, filepath.Join(tmpDir, "file_from_container"))
}

func TestRunContainerFailContainerCommand(t *testing.T) {
	tmpDir := t.TempDir()

	err := RunContainer(RunOptions{
		BuildCtxDir: "testdata",
		ImageTag:    "ubuntu:tt_test",
		Command:     []string{"touch", "/file_in_root"},
		Binds:       []string{fmt.Sprintf("%s:/work", tmpDir)},
	}, os.Stdout)
	require.Error(t, err)

	checkNoContainers(t, "ubuntu:tt_test")
}

func TestRunContainerNotExistingBind(t *testing.T) {
	tmpDir := t.TempDir()

	err := RunContainer(RunOptions{
		BuildCtxDir: "testdata",
		ImageTag:    "ubuntu:tt_test",
		Command:     []string{"touch", "/work/file_from_container"},
		Binds:       []string{fmt.Sprintf("%s:/work", filepath.Join(tmpDir, "non_existing_dir"))},
	}, os.Stdout)
	require.NoError(t, err)
	assert.FileExists(t, filepath.Join(tmpDir, "non_existing_dir", "file_from_container"))

	checkNoContainers(t, "ubuntu:tt_test")
}
