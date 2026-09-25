package docker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"os/user"
	"strings"

	"github.com/moby/go-archive"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
	"github.com/moby/moby/client/pkg/jsonmessage"
	"github.com/moby/term"
	"github.com/tarantool/tt/sdk/log"
)

var (
	errContainerExitCodeIs       = errors.New("container exit code is ")
	errTheOperationIsInterrupted = errors.New("the operation is interrupted")
)

// spell-checker:ignore jsonmessage stdcopy

const (
	// defaultDirPermissions is permissions for new directories.
	// 0755 - drwxr-xr-x.
	defaultDirPermissions = os.FileMode(0o755)
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
func interruptHandler(cancelFunc context.CancelFunc) func() {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt)

	go func() {
		_, ok := <-signals
		if ok {
			_, _ = fmt.Fprintln(os.Stdout, "Canceling operation...")

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
func buildDockerImage(dockerClient *mobyclient.Client, imageTag, buildContextDir string,
	verbose bool, writer io.Writer,
) error {
	buildCtx, err := archive.TarWithOptions(buildContextDir, &archive.TarOptions{})
	if err != nil {
		return fmt.Errorf("failed to archive build context %s: %w", buildContextDir, err)
	}

	opts := mobyclient.ImageBuildOptions{
		Dockerfile: dockerFileName,
		Tags:       []string{imageTag},
		Remove:     true,
	}

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer interruptHandler(cancelFunc)()

	buildResult, err := dockerClient.ImageBuild(ctx, buildCtx, opts)
	if err != nil {
		return fmt.Errorf("docker image build failed: %w", err)
	}

	if buildResult.Body == nil {
		return nil
	}

	defer func() {
		_ = buildResult.Body.Close()
	}()

	if !verbose {
		writer = io.Discard
	}

	termFd, isTerm := term.GetFdInfo(writer)

	err = jsonmessage.DisplayJSONMessagesStream(buildResult.Body, writer, termFd, isTerm, nil)
	if err != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return errTheOperationIsInterrupted
		}

		return fmt.Errorf("docker image build failed: %w", err)
	}

	return nil
}

// createContainer creates docker container and returns its ID.
func createContainer(dockerClient *mobyclient.Client, runOptions RunOptions) (string, error) {
	// Create directories on host, if they are not exist.
	for _, bind := range runOptions.Binds {
		hostDir, _, separatorAppears := strings.Cut(bind, ":")
		if separatorAppears && hostDir != "" {
			err := os.MkdirAll(hostDir, defaultDirPermissions)
			if err != nil {
				return "", fmt.Errorf("failed to create directory %s: %w", hostDir, err)
			}
		}
	}

	currentUser, err := user.Current()
	if err != nil {
		return "", fmt.Errorf("failed to get current user: %w", err)
	}

	log.Debug("Creating docker container.")

	ctx := context.Background()

	createResponse, err := dockerClient.ContainerCreate(ctx, mobyclient.ContainerCreateOptions{
		Image: runOptions.ImageTag,
		Config: &container.Config{
			Cmd:  runOptions.Command,
			Tty:  false,
			User: fmt.Sprintf("%s:%s", currentUser.Uid, currentUser.Gid),
		},
		HostConfig: &container.HostConfig{Binds: runOptions.Binds},
	})
	if err != nil {
		return "", fmt.Errorf("image %s: %w", runOptions.ImageTag, err)
	}

	log.Debugf("Docker container '%s' is created.", createResponse.ID[:12])

	return createResponse.ID, nil
}

// RunContainer builds docker image and runs a container.
func RunContainer(runOptions RunOptions, writer io.Writer) error {
	dockerClient, err := mobyclient.New(mobyclient.FromEnv)
	if err != nil {
		return fmt.Errorf("failed to create docker client: %w", err)
	}

	defer func() {
		_ = dockerClient.Close()
	}()

	log.Infof("Building docker image '%s'.", runOptions.ImageTag)

	err = buildDockerImage(dockerClient, runOptions.ImageTag, runOptions.BuildCtxDir,
		runOptions.Verbose, writer)
	if err != nil {
		return err
	}

	log.Info("Docker image is built.")

	containerID, err := createContainer(dockerClient, runOptions)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	defer func() {
		log.Debugf("Removing container %s", containerID[:12])

		_, err := dockerClient.ContainerRemove(context.Background(), containerID,
			mobyclient.ContainerRemoveOptions{})
		if err != nil {
			log.Warnf("Failed to remove container %s", containerID[:12])
		}
	}()

	// Start docker container.
	ctx, cancelFunc := context.WithCancel(context.Background())

	log.Debugf("The following command is going to be invoked in the container: %s.",
		strings.Join(runOptions.Command, " "))

	_, err = dockerClient.ContainerStart(ctx, containerID, mobyclient.ContainerStartOptions{})
	if err != nil {
		cancelFunc()

		return fmt.Errorf("failed to start container: %w", err)
	}

	defer interruptHandler(cancelFunc)()

	out, err := dockerClient.ContainerLogs(ctx, containerID, mobyclient.ContainerLogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
	})
	if err != nil {
		return fmt.Errorf("failed to get container logs: %w", err)
	}

	_, _ = stdcopy.StdCopy(writer, writer, out)
	_ = out.Close()

	waitResult := dockerClient.ContainerWait(ctx, containerID,
		mobyclient.ContainerWaitOptions{Condition: container.WaitConditionNotRunning})
	select {
	case err := <-waitResult.Error:
		if errors.Is(ctx.Err(), context.Canceled) {
			_, err = dockerClient.ContainerStop(context.Background(), containerID,
				mobyclient.ContainerStopOptions{})
			if err != nil {
				log.Warnf("Failed to stop the container %s", containerID[:12])
			}

			return errTheOperationIsInterrupted
		}

		if err != nil {
			return fmt.Errorf("failed to wait for container: %w", err)
		}
	case st := <-waitResult.Result:
		if st.StatusCode != 0 {
			return fmt.Errorf("%w%d", errContainerExitCodeIs, st.StatusCode)
		}
	}

	return nil
}
