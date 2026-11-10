//go:build mage && unix

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/magefile/mage/sh"
)

const (
	testImageDockerfile = "ci/Dockerfile"
	testRequirements    = "test/requirements.txt"
	testImageName       = "tt-test"

	// containerCache is where the container has the test cache of its own,
	// <test cache>/docker on the host.
	containerCache = "/tt-test-cache"

	// dockerSocket is the socket of the daemon on Linux and in the machine of
	// Docker Desktop alike.
	dockerSocket = "/var/run/docker.sock"

	volumeFlag = "--volume"

	testTmpDirEnv = "TT_TEST_TMPDIR"
)

var (
	errNoTargets      = errors.New("no targets to run: mage docker \"<target> ...\"")
	errNoGoVersion    = errors.New("go.mod sets no go version")
	errNotInARepoRoot = errors.New("mage docker runs from the root of the repository")

	goVersionRe = regexp.MustCompile(`(?m)^go\s+(\S+)\s*$`)

	// dockerPassedEnv are the variables of the host the targets in the
	// container see, if they are set.
	dockerPassedEnv = []string{
		testTarantoolEnv,
		"TT_TEST_ROCKS_MIRROR",
		"TT_ENABLE_COREDUMP_TESTS",
		"TT_ENABLE_DOCKER_CLEANUP",
		"TT_CLI_EE_USERNAME",
		"TT_CLI_EE_PASSWORD",
		"TT_CLI_BUILD_SSL",
		"PYTEST_ADDOPTS",
		"GOFLAGS",
		fuzzTimeEnv,
	}
)

// Docker runs mage targets in a container of the test image.
// The image, built from ci/Dockerfile the first time, has what CI installs to
// test tt. The targets are one argument: mage docker "lint:full unit:full".
// TT_TEST_TARANTOOL, latest unless set, picks Tarantool; a path names an
// executable in the container. PYTEST_ADDOPTS and the other variables of the
// tests are passed in. The container works in the checkout, at its path on
// the host, as the user of the host, with the network and the docker daemon
// of the host, which the docker tests use. Go, mage and the versions of
// Tarantool stay in <test cache>/docker between runs; delete it to start
// over.
func Docker(targets string) error {
	args := strings.Fields(targets)
	if len(args) == 0 {
		return errNoTargets
	}

	image, err := testImage()
	if err != nil {
		return err
	}

	runArgs, err := dockerRunArgs()
	if err != nil {
		return err
	}

	runArgs = append(runArgs, image, "mage")
	runArgs = append(runArgs, args...)

	return sh.RunV("docker", runArgs...)
}

// testImage builds the test image unless it is there. The tag is a hash of
// what it is built from, so a change to any of it builds a new one.
func testImage() (string, error) {
	dockerfile, err := os.ReadFile(testImageDockerfile)
	if err != nil {
		return "", fmt.Errorf("%w: %w", errNotInARepoRoot, err)
	}

	requirements, err := os.ReadFile(testRequirements)
	if err != nil {
		return "", fmt.Errorf("cannot read the test requirements: %w", err)
	}

	goVersion, err := goModVersion()
	if err != nil {
		return "", err
	}

	hash := sha256.New()
	for _, part := range [][]byte{dockerfile, requirements, []byte(goVersion)} {
		_, _ = fmt.Fprintf(hash, "%d\n", len(part))
		_, _ = hash.Write(part)
	}

	image := testImageName + ":" + hex.EncodeToString(hash.Sum(nil))[:12]

	err = exec.CommandContext(context.Background(), "docker", "image", "inspect", image).Run()
	if err == nil {
		return image, nil
	}

	_, _ = fmt.Fprintf(os.Stdout, "Building the test image %s...\n", image)

	buildContext, err := os.MkdirTemp("", "tt-test-image-")
	if err != nil {
		return "", fmt.Errorf("cannot create the build context: %w", err)
	}

	defer func() { _ = os.RemoveAll(buildContext) }()

	const requirementsMode = 0o644

	err = os.WriteFile(filepath.Join(buildContext, "requirements.txt"), requirements,
		requirementsMode)
	if err != nil {
		return "", fmt.Errorf("cannot write the build context: %w", err)
	}

	err = sh.RunV("docker", "build", "--build-arg", "GO_VERSION="+goVersion,
		"--tag", image, "--file", testImageDockerfile, buildContext)
	if err != nil {
		return "", fmt.Errorf("cannot build the test image: %w", err)
	}

	return image, nil
}

// goModVersion is the go version go.mod sets.
func goModVersion() (string, error) {
	goMod, err := os.ReadFile("go.mod")
	if err != nil {
		return "", fmt.Errorf("cannot read go.mod: %w", err)
	}

	match := goVersionRe.FindSubmatch(goMod)
	if match == nil {
		return "", errNoGoVersion
	}

	return string(match[1]), nil
}

// dockerRunArgs are the arguments of docker run up to the image.
func dockerRunArgs() ([]string, error) {
	checkout, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("cannot get the current directory: %w", err)
	}

	cache, err := testCacheDir()
	if err != nil {
		return nil, err
	}

	hostCache := filepath.Join(cache, "docker")

	tmpDir, err := dockerTmpDir()
	if err != nil {
		return nil, err
	}

	const dirMode = 0o700

	for _, dir := range []string{
		tmpDir,
		filepath.Join(hostCache, "home"),
		filepath.Join(hostCache, "bin"),
	} {
		err = os.MkdirAll(dir, dirMode)
		if err != nil {
			return nil, fmt.Errorf("cannot create %s: %w", dir, err)
		}
	}

	// The daemon is to mount the path itself, not a link to it, as /tmp is
	// on macOS.
	tmpDir, err = filepath.EvalSymlinks(tmpDir)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve the temporary directory: %w", err)
	}

	args := []string{
		"run", "--rm", "--init",
		"--network", "host",
		// rocks tests isolate the network with unshare -r -n, which the
		// seccomp profile of docker denies. Its AppArmor profile lets it
		// be, where an unconfined process may not: Ubuntu 24.04 restricts
		// user namespaces to the processes a profile allows them.
		"--security-opt", "seccomp=unconfined",
		"--workdir", checkout,
	}

	args = appendVolumes(args, checkout, tmpDir, dockerSocket)
	args = append(args, volumeFlag, hostCache+":"+containerCache)
	args = appendEnv(args,
		"TMPDIR="+tmpDir,
		"HOME="+containerCache+"/home",
		testCacheEnv+"="+containerCache,
		// The tt that installs Tarantool is a Linux one, which must not take
		// the place of the tt of the host in the checkout.
		"TTEXE="+containerCache+"/bin/tt",
		// The checkout belongs to the user of the host, whom git in the
		// container does not know.
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=safe.directory",
		"GIT_CONFIG_VALUE_0=*",
	)

	// A worktree keeps its repository elsewhere.
	gitDir, err := sh.Output("git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err == nil && !strings.HasPrefix(gitDir, checkout+string(os.PathSeparator)) {
		args = appendVolumes(args, gitDir)
	}

	if isTerminal(os.Stdin) && isTerminal(os.Stdout) {
		args = append(args, "--interactive", "--tty")
	}

	if runtime.GOOS == "linux" {
		args = append(args, linuxUserArgs()...)
	}

	for _, name := range dockerPassedEnv {
		if _, ok := os.LookupEnv(name); ok {
			args = appendEnv(args, name)
		}
	}

	return args, nil
}

// dockerTmpDir is the temporary directory of the tests in the container. It is
// at the same path on the host for the docker daemon to mount what the tests
// make in it, and is short for the unix sockets of the instances to fit:
// TT_TEST_TMPDIR, or a directory of the user in /tmp, not in TMPDIR, which is
// a long path on macOS.
func dockerTmpDir() (string, error) {
	if dir := os.Getenv(testTmpDirEnv); dir != "" {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", fmt.Errorf("cannot resolve %s=%s: %w", testTmpDirEnv, dir, err)
		}

		return abs, nil
	}

	return filepath.Join("/tmp", "tt-test-"+strconv.Itoa(os.Getuid())), nil
}

// linuxUserArgs run the container as the user of the host, for the files the
// tests write in the checkout to be the user's, in the group of the socket of
// the docker daemon. Docker Desktop maps the owner of the files it shares by
// itself, and its socket is root's.
func linuxUserArgs() []string {
	args := []string{"--user", strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())}

	current, err := user.Current()
	if err == nil {
		args = appendEnv(args, "USER="+current.Username)
	}

	var socket syscall.Stat_t

	if syscall.Stat(dockerSocket, &socket) == nil {
		args = append(args, "--group-add", strconv.FormatUint(uint64(socket.Gid), 10))
	}

	return args
}

// appendVolumes mounts each of paths at its own path in the container.
func appendVolumes(args []string, paths ...string) []string {
	for _, path := range paths {
		args = append(args, volumeFlag, path+":"+path)
	}

	return args
}

// appendEnv sets the variables of the container: NAME=value, or NAME for the
// value of the host.
func appendEnv(args []string, variables ...string) []string {
	for _, variable := range variables {
		args = append(args, "--env", variable)
	}

	return args
}

// isTerminal reports whether file is a terminal.
func isTerminal(file *os.File) bool {
	info, err := file.Stat()

	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
