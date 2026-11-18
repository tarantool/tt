package runtimepin_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/runtimepin"
	"github.com/tarantool/tt/v3/internal/binary"
)

// platformManifest is a manifest of the given [platform] constraints.
func platformManifest(t *testing.T, platform string) *manifest.Manifest {
	t.Helper()

	man, _, err := manifest.ParseManifest([]byte("manifest_version = '0.1'\n\n" +
		"[package]\nname = 'app'\n\n[platform]\n" + platform))
	require.NoError(t, err)

	return man
}

// install lays an installation of program as tt binary does, and makes it
// the active one when active is set.
func install(t *testing.T, root string, program binary.Program, id string, active bool) {
	t.Helper()

	dir := binary.InstallationPath(root, program, id)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, program.Exec()), []byte("#!/bin/sh\n"),
		0o755))

	if active {
		link := filepath.Join(root, binary.BinDirName, program.Exec())
		require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
		require.NoError(t, os.Symlink(filepath.Join(dir, program.Exec()), link))
	}
}

// noReleases is a source of prebuilt archives that must not be asked.
func noReleases(t *testing.T) runtimepin.Releases {
	t.Helper()

	return func(context.Context, binary.Program) ([]string, string, error) {
		t.Fatal("the sources of prebuilt archives are asked")

		return nil, "", nil
	}
}

// releases is a source of prebuilt archives with the given releases.
func releases(byProgram map[binary.Program][]string) runtimepin.Releases {
	return func(_ context.Context, program binary.Program) ([]string, string, error) {
		return byProgram[program], "fake", nil
	}
}

func emptyLock() *manifest.Lock {
	return &manifest.Lock{LockVersion: manifest.LockVersion}
}

func TestHoldKeepsWhatStillSatisfies(t *testing.T) {
	t.Parallel()

	man := platformManifest(t, "tarantool = '>=3.8.0'\ntt = '>=3.0.0'\n")
	previous := &manifest.Lock{Tarantool: "3.7.1", Tt: "3.0.0", Tcm: "1.3.1"}
	lock := emptyLock()

	warnings := runtimepin.Hold(man, previous, lock)

	assert.Empty(t, lock.Tarantool)
	assert.Equal(t, "3.0.0", lock.Tt)
	// [platform].tcm is not set: TCM is pinned no more, silently.
	assert.Empty(t, lock.Tcm)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], `pinned tarantool 3.7.1, which does not satisfy ">=3.8.0"`)
}

func TestHoldOfNoLockPinsNothing(t *testing.T) {
	t.Parallel()

	man := platformManifest(t, "tarantool = '>=3.0.0'\ntt = '>=3.0.0'\n")
	lock := emptyLock()

	assert.Empty(t, runtimepin.Hold(man, nil, lock))
	assert.False(t, runtimepin.IsPinned(man.Platform, lock))
}

func TestPinHoldsTheLockedVersion(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	install(t, root, binary.ProgramCe, "3.9.0", true)

	man := platformManifest(t, "tarantool = '>=3.0.0'\ntt = '>=3.0.0'\n")
	lock := emptyLock()

	warnings := runtimepin.Pin(context.Background(), man,
		&manifest.Lock{Tarantool: "3.8.1", Tt: "3.0.0"}, lock,
		runtimepin.Options{ProjectRoot: root, Releases: noReleases(t)})

	assert.Empty(t, warnings)
	assert.Equal(t, "3.8.1", lock.Tarantool)
	assert.Equal(t, "3.0.0", lock.Tt)
}

func TestPinTakesTheActiveInstallationFirst(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	install(t, root, binary.ProgramCe, "3.9.0", false)
	install(t, root, binary.ProgramCe, "3.8.1", true)
	install(t, root, binary.ProgramTt, "3.0.0", false)
	install(t, root, binary.ProgramTt, "3.1.0", false)
	// Neither an SDK component nor a build of a commit is a version to pin.
	install(t, root, binary.ProgramTt, "sdk-3.9.0", false)
	install(t, root, binary.ProgramTt, "abcdef1", false)

	man := platformManifest(t, "tarantool = '>=3.0.0'\ntt = '>=3.0.0'\n")
	lock := emptyLock()

	warnings := runtimepin.Pin(context.Background(), man, nil, lock,
		runtimepin.Options{
			ProjectRoot: root,
			Tarantool:   runtimepin.Active{Version: "3.7.0-0-gabcdef0", Flavor: "ce"},
			Tt:          runtimepin.Active{Version: "3.0.0", Flavor: ""},
			Releases:    noReleases(t),
		})

	assert.Empty(t, warnings)
	assert.Equal(t, "3.8.1", lock.Tarantool)
	assert.Equal(t, "3.1.0", lock.Tt)
}

func TestPinSkipsAnActiveInstallationThatDoesNotSatisfy(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	install(t, root, binary.ProgramCe, "3.9.0", false)
	install(t, root, binary.ProgramCe, "3.7.1", true)

	man := platformManifest(t, "tarantool = '>=3.8.0'\ntt = '>=3.0.0'\n")
	lock := emptyLock()

	runtimepin.Pin(context.Background(), man, nil, lock, runtimepin.Options{
		ProjectRoot: root,
		Tt:          runtimepin.Active{Version: "3.0.0", Flavor: ""},
	})

	assert.Equal(t, "3.9.0", lock.Tarantool)
}

func TestPinTakesWhatTtRunsWith(t *testing.T) {
	t.Parallel()

	man := platformManifest(t, "tarantool = '>=3.0.0'\ntt = '>=3.0.0'\n")
	lock := emptyLock()

	warnings := runtimepin.Pin(context.Background(), man, nil, lock, runtimepin.Options{
		ProjectRoot: t.TempDir(),
		Tarantool:   runtimepin.Active{Version: "3.8.1-0-ge643f88a5e0c", Flavor: "ce"},
		Tt:          runtimepin.Active{Version: "3.0.0-alpha.2", Flavor: ""},
		Releases:    noReleases(t),
	})

	assert.Empty(t, warnings)
	// The release a build of a tag is of, by the name tt binary installs it.
	assert.Equal(t, "3.8.1", lock.Tarantool)
	assert.Equal(t, "3.0.0-alpha.2", lock.Tt)
}

func TestPinTakesTheNewestRelease(t *testing.T) {
	t.Parallel()

	man := platformManifest(t,
		"tarantool = '>=3.0.0,<3.9.0'\ntt = '>=3.0.0'\ntcm = '>=1.0.0'\n")
	lock := emptyLock()

	warnings := runtimepin.Pin(context.Background(), man, nil, lock, runtimepin.Options{
		ProjectRoot: t.TempDir(),
		// A build past a tag is no release, and Enterprise is not Community.
		Tarantool: runtimepin.Active{Version: "3.8.1-12-gabcdef0", Flavor: "ce"},
		Tt:        runtimepin.Active{Version: "2.15.0", Flavor: ""},
		Releases: releases(map[binary.Program][]string{
			binary.ProgramCe: {"3.7.1", "3.9.0", "3.8.2", "3.8.10"},
			// No stable release of tt 3 yet: the newest prerelease.
			binary.ProgramTt:  {"2.15.0", "3.0.0-alpha.1", "3.0.0-alpha.2"},
			binary.ProgramTcm: {"1.3.1", "1.10.0"},
		}),
	})

	assert.Empty(t, warnings)
	assert.Equal(t, "3.8.10", lock.Tarantool)
	assert.Equal(t, "3.0.0-alpha.2", lock.Tt)
	assert.Equal(t, "1.10.0", lock.Tcm)
	assert.True(t, runtimepin.IsPinned(man.Platform, lock))
}

func TestPinOfEnterpriseAsksForTheEnterpriseProducts(t *testing.T) {
	t.Parallel()

	man := platformManifest(t, "tarantool = '>=3.0.0[ee]'\ntt = '>=2.0.0[ee]'\n")
	lock := emptyLock()

	var asked []binary.Program

	warnings := runtimepin.Pin(context.Background(), man, nil, lock, runtimepin.Options{
		ProjectRoot: t.TempDir(),
		// A Community tarantool and tt run tt: neither is the Enterprise one.
		Tarantool: runtimepin.Active{Version: "3.8.1-0-ge643f88a5e0c", Flavor: "ce"},
		Tt:        runtimepin.Active{Version: "3.0.0", Flavor: ""},
		Releases: func(_ context.Context, program binary.Program) ([]string, string, error) {
			asked = append(asked, program)

			return map[binary.Program][]string{
				binary.ProgramEe:   {"3.4.1", "3.5.0"},
				binary.ProgramTtEe: {"2.15.0"},
			}[program], "customer-zone", nil
		},
	})

	assert.Empty(t, warnings)
	assert.Equal(t, []binary.Program{binary.ProgramEe, binary.ProgramTtEe}, asked)
	assert.Equal(t, "3.5.0", lock.Tarantool)
	assert.Equal(t, "2.15.0", lock.Tt)
}

func TestPinLeavesUnpinnedWhatNothingGives(t *testing.T) {
	t.Parallel()

	man := platformManifest(t,
		"tarantool = '>=3.0.0[ee]'\ntt = '>=3.0.0'\ntcm = '>=1.0.0'\n")
	lock := emptyLock()

	warnings := runtimepin.Pin(context.Background(), man, nil, lock, runtimepin.Options{
		ProjectRoot: t.TempDir(),
		Tt:          runtimepin.Active{Version: "3.0.0", Flavor: ""},
		Releases: func(_ context.Context, program binary.Program) ([]string, string, error) {
			return nil, "customer-zone", binary.ErrSourcePrompts
		},
	})

	assert.Empty(t, lock.Tarantool)
	assert.Equal(t, "3.0.0", lock.Tt)
	assert.Empty(t, lock.Tcm)
	require.Len(t, warnings, 2)
	assert.Contains(t, warnings[0], "app.manifest.lock pins no tarantool-ee")
	assert.Contains(t, warnings[0], "tt binary install tarantool <version>ee")
	assert.Contains(t, warnings[1], "app.manifest.lock pins no tcm")
	assert.Contains(t, warnings[1], "the sources of prebuilt archives could not answer")
	assert.False(t, runtimepin.IsPinned(man.Platform, lock))
}

func TestPinOfAFailedSourceWarns(t *testing.T) {
	t.Parallel()

	man := platformManifest(t, "tarantool = '>=3.0.0'\ntt = '>=3.0.0'\n")
	lock := emptyLock()

	failed := errors.New("dial tcp: connection refused")

	warnings := runtimepin.Pin(context.Background(), man, nil, lock, runtimepin.Options{
		ProjectRoot: t.TempDir(),
		Tt:          runtimepin.Active{Version: "3.0.0", Flavor: ""},
		Releases: func(context.Context, binary.Program) ([]string, string, error) {
			return nil, "rws", failed
		},
	})

	assert.Empty(t, lock.Tarantool)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "connection refused")
	assert.Contains(t, warnings[0], "no installation of tarantool in")
}

func TestProgram(t *testing.T) {
	t.Parallel()

	community := platformManifest(t, "tarantool = '>=3.0.0'\ntt = '>=3.0.0[ce]'\n").Platform
	enterprise := platformManifest(t,
		"tarantool = '>=3.0.0[ee]'\ntt = '>=3.0.0[ee]'\ntcm = '>=1.0.0'\n").Platform

	assert.Equal(t, binary.ProgramCe,
		runtimepin.Program(community, runtimepin.ComponentTarantool))
	assert.Equal(t, binary.ProgramTt, runtimepin.Program(community, runtimepin.ComponentTt))
	assert.Equal(t, binary.ProgramEe,
		runtimepin.Program(enterprise, runtimepin.ComponentTarantool))
	assert.Equal(t, binary.ProgramTtEe, runtimepin.Program(enterprise, runtimepin.ComponentTt))
	assert.Equal(t, binary.ProgramTcm, runtimepin.Program(enterprise, runtimepin.ComponentTcm))
	assert.False(t, runtimepin.Wanted(community, runtimepin.ComponentTcm))
	assert.True(t, runtimepin.Wanted(enterprise, runtimepin.ComponentTcm))
}
