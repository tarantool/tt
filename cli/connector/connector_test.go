package connector

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetConnectTimeout(t *testing.T) {
	require.Equal(t, DefaultConnectTimeout, getConnectTimeout(ConnectOpts{}))
	require.Equal(t, 2*time.Second, getConnectTimeout(ConnectOpts{
		ConnectTimeout: 2 * time.Second,
	}))
}

func TestPrepareUnixAddressShortAbsolutePath(t *testing.T) {
	address := filepath.Join(t.TempDir(), "instance.sock")
	if len(address)+1 > unixSocketPathLimit() {
		t.Skip("temporary directory path is too long for this test")
	}

	prepared, cleanup, err := prepareUnixAddress(address)

	require.NoError(t, err)
	require.Equal(t, address, prepared)
	require.Nil(t, cleanup)
}

func TestPrepareUnixAddressRelativePath(t *testing.T) {
	const address = "run/instance.sock"

	prepared, cleanup, err := prepareUnixAddress(address)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	defer cleanup()

	require.Equal(t, address, prepared)
}

func TestPrepareUnixAddressLongPath(t *testing.T) {
	const socketName = "instance.sock"

	originalWorkDir, err := os.Getwd()
	require.NoError(t, err)

	socketDir := t.TempDir()
	for len(filepath.Join(socketDir, socketName))+1 <= unixSocketPathLimit() {
		socketDir = filepath.Join(socketDir, strings.Repeat("d", 32))
	}
	require.NoError(t, os.MkdirAll(socketDir, 0o755))

	prepared, cleanup, err := prepareUnixAddress(filepath.Join(socketDir, socketName))
	require.NoError(t, err)
	require.NotNil(t, cleanup)

	cleanedUp := false
	defer func() {
		if !cleanedUp {
			cleanup()
		}
	}()

	currentWorkDir, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, filepath.Clean(socketDir), currentWorkDir)
	require.Equal(t, "./"+socketName, prepared)

	cleanup()
	cleanedUp = true

	currentWorkDir, err = os.Getwd()
	require.NoError(t, err)
	require.Equal(t, originalWorkDir, currentWorkDir)
}

func TestPrepareUnixAddressLongSocketName(t *testing.T) {
	socketName := strings.Repeat("s", unixSocketPathLimit())
	address := filepath.Join(t.TempDir(), socketName)

	prepared, cleanup, err := prepareUnixAddress(address)

	require.ErrorContains(t, err, "socket name is longer")
	require.Empty(t, prepared)
	require.Nil(t, cleanup)
}
