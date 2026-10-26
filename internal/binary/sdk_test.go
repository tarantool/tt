package binary_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestReadSDKSeparatesManifestValidation(t *testing.T) {
	const selected = "gc64-3.7.1-0-r93"

	for _, testCase := range []struct {
		name     string
		version  string
		products []string
		valid    bool
	}{
		{"valid manifest without product files", selected, []string{"tarantool-ee", "tcm"}, true},
		{"different owner version", "gc64-3.8.0-0-r94", []string{"tarantool-ee", "tcm"}, false},
		{"empty version", "", []string{"tarantool-ee", "tcm"}, false},
		{"unsafe version", "../outside", []string{"tarantool-ee", "tcm"}, false},
		{"empty products", selected, []string{}, false},
		{"unsupported component", selected, []string{"tarantool"}, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			directory := binary.InstallationPath(root, binary.ProgramSDK, selected)
			require.NoError(t, os.MkdirAll(directory, 0o755))

			data, err := json.Marshal(map[string]any{
				"version": testCase.version, "products": testCase.products,
			})
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(directory, binary.SDKManifestName),
				data, 0o644))

			sdk, err := binary.ReadSDK(root, selected)
			require.NoError(t, err)
			require.Equal(t, testCase.version, sdk.Version)

			err = binary.ValidateSDKManifest(sdk, selected)
			if !testCase.valid {
				require.ErrorIs(t, err, binary.ErrInvalidSDK)

				return
			}

			require.NoError(t, err)
			require.Error(t, binary.ValidateSDKFiles(root, sdk))
		})
	}
}

func TestBundleInstallationID(t *testing.T) {
	for _, testCase := range []struct {
		source   string
		expected string
	}{
		{"gc64-3.8.1-0-r93", "3.8.1"},
		{"gc64-3.8.1-0-r94", "3.8.1"},
		{"nogc64-3.8.1-0-r94", "3.8.1"},
		{"gc64-4.0.0-0-r1", "4.0.0"},
		{"debug-gc64-3.8.1-0-r93", "debug-3.8.1"},
		{"debug-nogc64-3.8.1-0-r94", "debug-3.8.1"},
		{"gc64-3.8.1-rc2-0-r93", "3.8.1-rc2"},
		{"gc64-3.8.1-beta1-10-gabcdef0-r93", "3.8.1-beta1"},
		{"gc64-3.8.1-alpha1-0-r93", "3.8.1-alpha1"},
		{"3.8.1", "3.8.1"},
	} {
		t.Run(testCase.source, func(t *testing.T) {
			parsed, err := version.Parse(testCase.source)
			require.NoError(t, err)
			require.Equal(t, testCase.expected,
				binary.BundleInstallationID(binary.ProgramEe, parsed, false))

			for _, program := range []binary.Program{
				binary.ProgramSDK, binary.ProgramTcm, binary.ProgramTtEe,
			} {
				require.Equal(t, testCase.source,
					binary.BundleInstallationID(program, parsed, false))
			}

			for _, program := range []binary.Program{
				binary.ProgramEe, binary.ProgramSDK, binary.ProgramTcm,
			} {
				require.Equal(t, "dev_"+testCase.source,
					binary.BundleInstallationID(program, parsed, true))
			}
		})
	}
}

func TestMatchInstalledEeVersion(t *testing.T) {
	// tt binary no longer installs Tarantool 2.x, but installations made by
	// older versions keep their gc64-2.11.1 and nogc64-2.11.1 names.
	installed := []string{
		"3.8.1", "3.8.1-rc2", "debug-3.8.1", "gc64-2.11.1", "nogc64-2.11.1",
		"sdk-gc64-3.8.1-0-r93", "sdk-gc64-3.8.1-0-r94",
	}

	for _, testCase := range []struct {
		requested string
		expected  string
	}{
		{"3.8.1", "3.8.1"},
		{"gc64-3.8.1-0-r93", "3.8.1"},
		{"gc64-3.8.1-0-r94", "3.8.1"},
		{"nogc64-3.8.1-0-r94", "3.8.1"},
		{"gc64-2.11.1", "gc64-2.11.1"},
		{"nogc64-2.11.1", "nogc64-2.11.1"},
		{"gc64-3.8.1-rc2-0-r93", "3.8.1-rc2"},
		{"debug-gc64-3.8.1-0-r93", "debug-3.8.1"},
		{"sdk-gc64-3.8.1-0-r94", "sdk-gc64-3.8.1-0-r94"},
	} {
		t.Run(testCase.requested, func(t *testing.T) {
			selected, err := binary.MatchInstalledVersion(
				binary.ProgramEe, testCase.requested, installed)
			require.NoError(t, err)
			require.Equal(t, testCase.expected, selected)
		})
	}

	for _, requested := range []string{"3", "3.8", "3.8.2", "gc64-2.11.2-0-r93"} {
		_, err := binary.MatchInstalledVersion(binary.ProgramEe, requested, installed)
		require.ErrorIs(t, err, version.ErrInstalledVersionNotFound)
	}

	for _, requested := range []string{"2.11.1", "sdk-3.8.1"} {
		_, err := binary.MatchInstalledVersion(binary.ProgramEe, requested, installed)
		require.ErrorIs(t, err, version.ErrAmbiguousInstalledVersion)
	}

	_, err := binary.MatchInstalledVersion(binary.ProgramEe, "3.8.1",
		[]string{"3.8.1-rc1", "debug-3.8.1"})
	require.ErrorIs(t, err, version.ErrAmbiguousInstalledVersion)
}

func TestMatchInstalledDevVersion(t *testing.T) {
	const (
		first  = "gc64-3.8.1-0-r93"
		second = "gc64-3.8.1-0-r94"
	)

	for _, testCase := range []struct {
		program   binary.Program
		requested string
		installed []string
		expected  string
		err       error
	}{
		{binary.ProgramEe, "3.8.1", []string{"3.8.1", "dev_" + first}, "3.8.1", nil},
		{binary.ProgramEe, "dev_" + first, []string{"3.8.1", "dev_" + first}, "dev_" + first, nil},
		{binary.ProgramEe, "dev_3.8.1", []string{"3.8.1", "dev_" + first}, "dev_" + first, nil},
		{binary.ProgramEe, "3.8.1", []string{"dev_" + first},
			"", version.ErrInstalledVersionNotFound},
		{binary.ProgramEe, "dev_3.8.1", []string{"3.8.1"}, "", version.ErrInstalledVersionNotFound},
		{binary.ProgramEe, "dev_3.8.1", []string{"dev_" + first, "dev_" + second},
			"", version.ErrAmbiguousInstalledVersion},
		{binary.ProgramSDK, "3.8.1", []string{first, "dev_" + first}, first, nil},
		{binary.ProgramSDK, "dev_3.8.1", []string{first, "dev_" + first}, "dev_" + first, nil},
		{binary.ProgramSDK, "sdk-dev_" + first, []string{first, "dev_" + first},
			"dev_" + first, nil},
		{binary.ProgramTcm, "sdk-3.8.1", []string{"sdk-" + first, "sdk-dev_" + first},
			"sdk-" + first, nil},
		{binary.ProgramTcm, "sdk-dev_3.8.1", []string{"sdk-" + first, "sdk-dev_" + first},
			"sdk-dev_" + first, nil},
		{binary.ProgramEe, "dev_3.8.1", []string{"sdk-dev_" + first},
			"", version.ErrInstalledVersionNotFound},
		{binary.ProgramTcm, "1.3.1", []string{"1.3.1-0-gabcdef0", "dev_1.3.1-0-gabcdef0"},
			"1.3.1-0-gabcdef0", nil},
		{binary.ProgramTcm, "dev_1.3.1", []string{"1.3.1-0-gabcdef0", "dev_1.3.1-0-gabcdef0"},
			"dev_1.3.1-0-gabcdef0", nil},
	} {
		t.Run(testCase.program.String()+"/"+testCase.requested, func(t *testing.T) {
			selected, err := binary.MatchInstalledVersion(testCase.program,
				testCase.requested, testCase.installed)
			require.ErrorIs(t, err, testCase.err)
			require.Equal(t, testCase.expected, selected)
		})
	}
}
