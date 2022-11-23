package docker

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/signal"
	"os/user"
	"strings"

	"github.com/apex/log"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/archive"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/moby/term"
)

const (
	// defaultDirPermissions is permissions for new directories.
	// 0755 - drwxr-xr-x
	defaultDirPermissions = os.FileMode(0755)
	// dockerFileName is a default Dockerfile file name.
	dockerFileName = "Dockerfile"
)

// RunOptions options for docker container run.
type RunOptions struct {
	// BuildContext docker image build context directory.
	BuildCtxDir string
	// ImageTag - docker image tag.
	ImageTag string
	// Command is a command to run in container.
	Command []string
	// Binds - directory bindings in "host_dir:container_dir" format.
	Binds []string
	// Verbose, if set, verbose output is enabled.
	Verbose bool
}

// interruptHandler start goroutine that handles interrupt signal and calls cancellation function.
// The returned function is to be called to stop signal handling.
func interruptHandler(cancelFunc context.CancelFunc) (stopSignalProcessing func()) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)
	go func() {
		_, ok := <-signals
		if ok {
			fmt.Println("Canceling operation...")
			cancelFunc()
		}
	}()

	return func() {
		close(signals)
		signal.Stop(signals)
		cancelFunc()
	}
}

// buildDockerImage builds docker image.
func buildDockerImage(dockerClient *client.Client, imageTag string, buildContextDir string,
	verbose bool, writer io.Writer) error {
	buildCtx, err := archive.TarWithOptions(buildContextDir, &archive.TarOptions{})
	if err != nil {
		return err
	}

	opts := types.ImageBuildOptions{
		Dockerfile: dockerFileName,
		Tags:       []string{imageTag},
		Remove:     true,
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer interruptHandler(cancelFunc)()
	if buildResponse, err := dockerClient.ImageBuild(ctx, buildCtx, opts); err == nil {
		if buildResponse.Body != nil {
			defer buildResponse.Body.Close()
			if !verbose {
				writer = ioutil.Discard
			}
			termFd, isTerm := term.GetFdInfo(writer)
			if err = jsonmessage.DisplayJSONMessagesStream(buildResponse.Body,
				writer, termFd, isTerm, nil); err != nil {
				if ctx.Err() == context.Canceled {
					return fmt.Errorf("the operation is interrupted")
				}
				return err
			}
		}
	} else {
		return fmt.Errorf("docker image build failed: %s", err)
	}
	return nil
}

// createContainer creates docker container and returns its ID.
func createContainer(dockerClient *client.Client, runOptions RunOptions) (string, error) {
	// Create directories on host, if they are not exist.
	for _, bind := range runOptions.Binds {
		if hostDir, _, separatorAppears := strings.Cut(bind, ":"); separatorAppears {
			if hostDir != "" {
				if err := os.MkdirAll(hostDir, defaultDirPermissions); err != nil {
					return "", err
				}
			}
		}
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	log.Debug("Creating docker container.")
	ctx := context.Background()
	createResponse, err := dockerClient.ContainerCreate(ctx, &container.Config{
		Image: runOptions.ImageTag,
		Cmd:   runOptions.Command,
		Tty:   false,
		User:  fmt.Sprintf("%s:%s", currentUser.Uid, currentUser.Gid),
	}, &container.HostConfig{Binds: runOptions.Binds}, nil, nil, "")
	if err != nil {
		return "", err
	}
	log.Debugf("Docker container '%s' is created.", createResponse.ID[:12])

	return createResponse.ID, nil
}

// RunContainer builds docker image and runs a container.
func RunContainer(runOptions RunOptions, writer io.Writer) error {
	dockerClient, err := client.NewClientWithOpts(client.FromEnv,
		client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}
	defer dockerClient.Close()

	log.Infof("Building docker image '%s'.", runOptions.ImageTag)
	if err = buildDockerImage(dockerClient, runOptions.ImageTag, runOptions.BuildCtxDir,
		runOptions.Verbose, writer); err != nil {
		return err
	}
	log.Info("Docker image is built.")

	containerId, err := createContainer(dockerClient, runOptions)
	if err != nil {
		return nil
	}
	defer func() {
		log.Debugf("Removing container %s", containerId[:12])
		if err := dockerClient.ContainerRemove(context.Background(), containerId,
			types.ContainerRemoveOptions{}); err != nil {
			log.Warnf("Failed to remove container %s", containerId[:12])
		}
	}()

	// Start docker container.
	ctx, cancelFunc := context.WithCancel(context.Background())
	log.Debugf("The following command is going to be invoked in the container: %s.",
		strings.Join(runOptions.Command, " "))
	if err := dockerClient.ContainerStart(ctx, containerId,
		types.ContainerStartOptions{}); err != nil {
		cancelFunc()
		return err
	}
	defer interruptHandler(cancelFunc)()

	out, err := dockerClient.ContainerLogs(ctx, containerId, types.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true})
	if err != nil {
		return err
	}
	stdcopy.StdCopy(writer, writer, out)
	out.Close()

	statusCh, errCh := dockerClient.ContainerWait(ctx, containerId,
		container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if ctx.Err() == context.Canceled {
			if err = dockerClient.ContainerStop(context.Background(), containerId,
				nil); err != nil {
				log.Warnf("Failed to stop the container %s", containerId[:12])
			}
			return fmt.Errorf("the operation is interrupted")
		}
		return err
	case st := <-statusCh:
		if st.StatusCode != 0 {
			return fmt.Errorf("container exit code is %d", st.StatusCode)
		}
	}
	return nil
}
