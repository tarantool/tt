package binary_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/bundle"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestCheckRequestedVersion(t *testing.T) {
	for _, test := range []struct {
		program   binary.Program
		requested string
		// unsupported is whether the request is a version tt does not install.
		unsupported bool
	}{
		{binary.ProgramCe, "2.11.5", true},
		{binary.ProgramCe, "1.10.15", true},
		{binary.ProgramCe, "2.11", true},
		{binary.ProgramCe, "2", true},
		{binary.ProgramCe, "2.10.0-rc1", true},
		{binary.ProgramCe, "2.11.0-entrypoint", true},
		{binary.ProgramEe, "2.11.1", true},
		{binary.ProgramEe, "gc64-2.11.1-0-r609", true},
		{binary.ProgramSDK, "sdk-gc64-2.11.1-0-r609", true},
		{binary.ProgramSDK, "2.11", true},
		{binary.ProgramCe, "3.0.0", false},
		{binary.ProgramCe, "3", false},
		{binary.ProgramCe, "", false},
		{binary.ProgramCe, "master", false},
		{binary.ProgramCe, "pr/123", false},
		// Commits, all-numeric or not.
		{binary.ProgramCe, "1234567", false},
		{binary.ProgramCe, "2345678abc", false},
		{binary.ProgramCe, "2abcdef", false},
		{binary.ProgramCe, "abcdef0", false},
		// tt, TCM and tt-ee have versions of their own.
		{binary.ProgramTt, "2.15.0", false},
		{binary.ProgramTtEe, "2.15.0", false},
		{binary.ProgramTcm, "1.3.1", false},
	} {
		t.Run(test.program.String()+"/"+test.requested, func(t *testing.T) {
			err := binary.CheckRequestedVersion(test.program, test.requested)
			if !test.unsupported {
				require.NoError(t, err)

				return
			}

			require.ErrorIs(t, err, binary.ErrUnsupportedVersion)
			assert.EqualError(t, err, "unsupported version: "+test.program.String()+" "+
				test.requested+": tt 3 installs Tarantool 3.0 and later only; "+
				"use tt 2 to install Tarantool "+test.requested)
		})
	}
}

var errTestPlatform = errors.New("no archive for this platform")

// releasesTestSource is a source of tt that lists the tags of its releases
// and publishes archives for the host of those in platforms only.
type releasesTestSource struct {
	releases  []string
	platforms map[string]bool
	err       error
}

func (releasesTestSource) Name() string { return "test-source" }

func (releasesTestSource) Products() []string { return []string{binary.ProgramTt.String()} }

func (releasesTestSource) List(context.Context, bundle.Query) ([]bundle.Artifact, error) {
	return nil, nil
}

func (releasesTestSource) Fetch(context.Context, bundle.Artifact, io.Writer) error {
	return nil
}

func (s releasesTestSource) Releases(context.Context) ([]string, error) {
	return s.releases, s.err
}

func (s releasesTestSource) CheckPlatform(tag, goos, goarch string) error {
	if goos != runtime.GOOS || goarch != runtime.GOARCH {
		return fmt.Errorf("%w: asked for %s/%s", errTestPlatform, goos, goarch)
	}

	if !s.platforms[tag] {
		return errTestPlatform
	}

	return nil
}

// TestPrebuiltVersionsReleases takes the release tags of a source that
// lists them, sorted, without those that publish no archive for the host
// and those that name no version.
func TestPrebuiltVersionsReleases(t *testing.T) {
	source := releasesTestSource{
		releases: []string{"v3.0.0-alpha.2", "nightly", "v2.15.2", "v2.15.1", "v1.0.0"},
		platforms: map[string]bool{
			"v3.0.0-alpha.2": true, "nightly": true, "v2.15.2": true, "v1.0.0": true,
		},
	}

	versions, err := binary.PrebuiltVersions(t.Context(), source, binary.ProgramTt)
	require.NoError(t, err)

	tags := make([]string, 0, len(versions))
	for _, found := range versions {
		tags = append(tags, found.Str)
	}

	assert.Equal(t, []string{"v1.0.0", "v2.15.2", "v3.0.0-alpha.2"}, tags)

	source.err = errTestPlatform

	_, err = binary.PrebuiltVersions(t.Context(), source, binary.ProgramTt)
	require.ErrorIs(t, err, errTestPlatform)
	require.EqualError(t, err, "test-source: no archive for this platform")
}
