package connector

import (
	"context"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrepareUnixAddress(t *testing.T) {
	//nolint:usetesting // The short-path case must fit sun_path on macOS too.
	root, err := os.MkdirTemp("/tmp", "tt-connect-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	short := filepath.Join(root, "console.sock")
	address, cleanup, err := prepareUnixAddress(short)
	require.NoError(t, err)
	require.Equal(t, short, address)
	require.Nil(t, cleanup)

	t.Chdir(root)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	address, cleanup, err = prepareUnixAddress("console.sock")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(cwd, "console.sock"), address)
	require.Nil(t, cleanup)

	longDir := filepath.Join(t.TempDir(), strings.Repeat("x", unixSocketPathLimit()))
	require.NoError(t, os.MkdirAll(longDir, 0o700))
	t.Setenv("TMPDIR", longDir)

	longAddress := filepath.Join(longDir, "console.sock")

	address, cleanup, err = prepareUnixAddress(longAddress)
	require.NoError(t, err)
	require.NotNil(t, cleanup)
	require.LessOrEqual(t, len(address)+1, unixSocketPathLimit())

	target, err := os.Readlink(address)
	require.NoError(t, err)
	require.Equal(t, longAddress, target)
	cleanup()
	require.NoDirExists(t, filepath.Dir(address))

	current, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, cwd, current)
}

func TestConnectLongUnixPathConcurrent(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)

	//nolint:usetesting // The alias must fit sun_path even when the test directory is long.
	root, err := os.MkdirTemp("/tmp", "tt-connect-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })

	longDir := filepath.Join(root, strings.Repeat("x", unixSocketPathLimit()))
	require.NoError(t, os.Mkdir(longDir, 0o700))
	require.NoError(t, os.Symlink(longDir, filepath.Join(root, "dir")))

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "unix",
		filepath.Join(root, "dir", "console.sock"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = listener.Close() })

	unixListener, ok := listener.(*net.UnixListener)
	require.True(t, ok)
	require.NoError(t, unixListener.SetDeadline(time.Now().Add(5*time.Second)))

	const count = 12

	var server sync.WaitGroup

	server.Go(func() {
		for range count {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			server.Go(func() {
				defer func() { _ = conn.Close() }()

				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

				_, _ = fmt.Fprint(conn, "Tarantool 3.6.0 (Lua console)\n")
				_, _ = io.Copy(io.Discard, conn)
			})
		}
	})

	results := make(chan error, count)
	for range count {
		go func() {
			conn, err := ConnectContext(t.Context(), ConnectOpts{
				Network: UnixNetwork, Address: filepath.Join(longDir, "console.sock"),
			})
			if err == nil {
				err = conn.Close()
			}

			results <- err
		}()
	}

	for range count {
		select {
		case err := <-results:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("connection setup did not complete")
		}
	}

	server.Wait()

	current, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, cwd, current)
}

func TestConnectLongUnixPathErrorCleansAlias(t *testing.T) {
	//nolint:usetesting // The alias must fit sun_path even when the test directory is long.
	root, err := os.MkdirTemp("/tmp", "tt-connect-test-")
	require.NoError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	t.Setenv("TMPDIR", root)

	cwd, err := os.Getwd()
	require.NoError(t, err)

	_, err = Connect(ConnectOpts{Network: UnixNetwork,
		Address: filepath.Join(root, strings.Repeat("x", unixSocketPathLimit()), "missing.sock")})
	require.ErrorContains(t, err, "failed to dial")

	entries, err := os.ReadDir(root)
	require.NoError(t, err)
	require.Empty(t, entries)

	current, err := os.Getwd()
	require.NoError(t, err)
	require.Equal(t, cwd, current)
}

func TestConnectContextCancelsGreeting(t *testing.T) {
	for _, binary := range []bool{false, true} {
		t.Run(fmt.Sprintf("binary=%v", binary), func(t *testing.T) {
			listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { _ = listener.Close() })

			closed := make(chan struct{}, 2)
			ready := make(chan struct{}, 2)

			var server sync.WaitGroup

			server.Go(func() {
				count := 1
				if binary {
					count = 2
				}

				for index := range count {
					conn, err := listener.Accept()
					if err != nil {
						return
					}

					server.Go(func() {
						defer func() { _ = conn.Close() }()

						if binary && index == 0 {
							_, _ = fmt.Fprint(conn, "Tarantool 3.6.0 (Binary)\n")
						}

						ready <- struct{}{}

						_, _ = io.Copy(io.Discard, conn)

						closed <- struct{}{}
					})
				}
			})

			ctx, cancel := context.WithCancel(t.Context())

			defer cancel()

			done := make(chan error, 1)

			go func() {
				_, err := ConnectContext(ctx, ConnectOpts{
					Network: TCPNetwork, Address: listener.Addr().String(),
				})
				done <- err
			}()

			count := 1
			if binary {
				count = 2
			}

			for range count {
				select {
				case <-ready:
				case <-time.After(5 * time.Second):
					t.Fatal("connection did not reach greeting stage")
				}
			}

			cancel()

			select {
			case err := <-done:
				require.ErrorIs(t, err, context.Canceled)
			case <-time.After(5 * time.Second):
				t.Fatal("connection setup ignored cancellation")
			}

			server.Wait()
			require.NotEmpty(t, closed)
		})
	}
}

func TestPrepareUnixAddressBoundary(t *testing.T) {
	for _, length := range []int{unixSocketPathLimit() - 1, unixSocketPathLimit()} {
		t.Run(fmt.Sprintf("length=%d", length), func(t *testing.T) {
			address := "/" + strings.Repeat("x", length-1)
			prepared, cleanup, err := prepareUnixAddress(address)
			require.NoError(t, err)

			if cleanup != nil {
				defer cleanup()
			}

			if length < unixSocketPathLimit() {
				require.Equal(t, address, prepared)
				require.Nil(t, cleanup)
			} else {
				require.NotEqual(t, address, prepared)
				require.NotNil(t, cleanup)
				require.Less(t, len(prepared), unixSocketPathLimit())
			}
		})
	}
}
