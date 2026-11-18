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

// archivesTestSource is a source of archives of tcm that lists artifacts, and
// asks for credentials while prompts is set.
type archivesTestSource struct {
	artifacts []bundle.Artifact
	prompts   bool
}

func (archivesTestSource) Name() string { return "archives" }

func (archivesTestSource) Products() []string { return []string{binary.ProgramTcm.String()} }

func (s archivesTestSource) List(context.Context, bundle.Query) ([]bundle.Artifact, error) {
	return s.artifacts, nil
}

func (archivesTestSource) Fetch(context.Context, bundle.Artifact, io.Writer) error {
	return nil
}

func (s archivesTestSource) Prompts() bool { return s.prompts }

// TestReleaseIDs names the release builds of a source as tt binary installs
// them, each once, and asks no source that would ask for credentials.
func TestReleaseIDs(t *testing.T) {
	saved := binary.BundleSources()

	t.Cleanup(func() { binary.SetBundleSources(saved) })

	tt := releasesTestSource{
		releases:  []string{"v3.0.0-alpha.2", "v2.15.2"},
		platforms: map[string]bool{"v3.0.0-alpha.2": true, "v2.15.2": true},
	}
	tcm := archivesTestSource{artifacts: []bundle.Artifact{
		{Version: "1.3.1", Name: "tcm-1.3.1.tar.gz"},
		{Version: "1.4.0-rc1", Name: "tcm-1.4.0-rc1.tar.gz"},
		{Version: "1.3.1", Flavor: []string{"debug"}, Name: "tcm-debug-1.3.1.tar.gz"},
		{Version: "1.2.0", Name: "tcm-1.2.0.tar.gz"},
	}}

	binary.SetBundleSources([]bundle.Source{tt, tcm})

	ids, source, err := binary.ReleaseIDs(t.Context(), binary.ProgramTt)
	require.NoError(t, err)
	assert.Equal(t, "test-source", source)
	assert.Equal(t, []string{"2.15.2", "3.0.0-alpha.2"}, ids)

	ids, _, err = binary.ReleaseIDs(t.Context(), binary.ProgramTcm)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"1.2.0", "1.3.1"}, ids)

	tcm.prompts = true
	binary.SetBundleSources([]bundle.Source{tt, tcm})

	_, source, err = binary.ReleaseIDs(t.Context(), binary.ProgramTcm)
	require.ErrorIs(t, err, binary.ErrSourcePrompts)
	assert.Equal(t, "archives", source)

	_, _, err = binary.ReleaseIDs(t.Context(), binary.ProgramCe)
	require.ErrorIs(t, err, binary.ErrNoBundleSource)
}
