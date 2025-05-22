package pack

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
)

func TestCreateControlDir(t *testing.T) {
	testCases := []struct {
		name         string
		packCtx      *PackCtx
		cmdCtx       cmdcontext.CmdCtx
		opts         *config.CliOpts
		destPath     string
		correctError func(err error) bool
		correctDir   func(controlPath string) bool
	}{
		{
			name: "All correct parameters",
			packCtx: &PackCtx{
				Name:    "test",
				Version: "1.0.0",
				RpmDeb: RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
				},
			},
			opts:     &config.CliOpts{},
			destPath: t.TempDir(),
			correctError: func(err error) bool {
				return err == nil
			},
			correctDir: func(controlPath string) bool {
				return true
			},
		},
		{
			name: "Default case",
			packCtx: &PackCtx{
				Name:    "",
				Version: "",
				RpmDeb: RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
				},
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{
					InstancesEnabled: ".",
				},
			},
			destPath: t.TempDir(),
			correctError: func(err error) bool {
				return err == nil
			},
			correctDir: func(controlPath string) bool {
				return true
			},
		},
		{
			name: "Wrong dependency",
			packCtx: &PackCtx{
				Name:    "",
				Version: "",
				RpmDeb: RpmDebCtx{
					Deps:              []string{"tarantool==master"},
					WithTarantoolDeps: false,
				},
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{
					InstancesEnabled: ".",
				},
			},
			destPath: t.TempDir(),
			correctError: func(err error) bool {
				return strings.Contains(err.Error(), "unexpected token \"master\"")
			},
			correctDir: func(controlPath string) bool {
				return true
			},
		},
		{
			name: "Unexisting postinst script passed",
			packCtx: &PackCtx{
				Name:    "test",
				Version: "1.0.0",
				RpmDeb: RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
					PostInst:          "nothing",
				},
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{
					InstancesEnabled: ".",
				},
			},
			destPath: t.TempDir(),
			correctError: func(err error) bool {
				return strings.Contains(err.Error(), "lstat nothing: no such file or directory")
			},
			correctDir: func(controlPath string) bool {
				return true
			},
		},
		{
			name: "Unexisting preinst script passed",
			packCtx: &PackCtx{
				Name:    "test",
				Version: "1.0.0",
				RpmDeb: RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
					PreInst:           "nothing",
				},
			},
			opts: &config.CliOpts{
				Env: &config.TtEnvOpts{
					InstancesEnabled: ".",
				},
			},
			destPath: t.TempDir(),
			correctError: func(err error) bool {
				return strings.Contains(err.Error(), "lstat nothing: no such file or directory")
			},
			correctDir: func(controlPath string) bool {
				return true
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(testCase.destPath, "control_test_dir")
			err := createControlDir(testCase.cmdCtx, *testCase.packCtx, testCase.opts, path)
			require.Truef(t, testCase.correctError(err), "wrong error caught: %v", err)
			require.Truef(t, testCase.correctDir(path), "wrong directory structure")
		})
	}
}

func TestCreateControlFile(t *testing.T) {
	testCases := []struct {
		name          string
		basePath      string
		debControlCtx *map[string]interface{}
		correctError  func(err error) bool
	}{
		{
			name:     "Everything is ok",
			basePath: t.TempDir(),
			debControlCtx: &map[string]interface{}{
				"Name":         "test",
				"Version":      "1.0.0",
				"Maintainer":   "dev",
				"Architecture": "amd64",
				"Depends":      "tarantool",
			},
			correctError: func(err error) bool {
				return err == nil
			},
		},
		{
			name:     "Unexisting base directory",
			basePath: "nothing",
			debControlCtx: &map[string]interface{}{
				"Name":         "test",
				"Version":      "1.0.0",
				"Maintainer":   "dev",
				"Architecture": "amd64",
				"Depends":      "tarantool",
			},
			correctError: func(err error) bool {
				return strings.Contains(err.Error(),
					"open nothing/control: no such file or directory")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := createControlFile(testCase.basePath, testCase.debControlCtx)
			require.Truef(t, testCase.correctError(err), "wrong error: %v", err)
		})
	}
}

func TestAddDependenciesDeb(t *testing.T) {
	testDebControlCtx := &map[string]interface{}{
		"Depends": "",
	}
	resultDepends := "test (>= 1.0.1)"
	testDeps := []PackDependency{
		{
			Name: "test",
			Relations: []DepRelation{
				{
					Relation: ">=",
					Version:  "1.0.1",
				},
			},
		},
	}
	addDependenciesDeb(testDebControlCtx, testDeps)
	require.Equalf(t, (*testDebControlCtx)["Depends"], resultDepends,
		"dependency was not added, expected: %s, got: %s",
		(*testDebControlCtx)["Depends"], resultDepends)
}

func TestGetDebRelation(t *testing.T) {
	testCases := []struct {
		name     string
		relation string
		result   string
	}{
		{
			name:     "Must be changed",
			relation: ">",
			result:   ">>",
		},
		{
			name:     "Must be changed",
			relation: "<",
			result:   "<<",
		},
		{
			name:     "Must be changed",
			relation: "==",
			result:   "=",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			require.Equalf(t, testCase.result, getDebRelation(testCase.relation),
				"incorrect metamorphose: expected: %s, got: %s",
				testCase.result, getDebRelation(testCase.relation))
		})
	}
}

func TestInitScript(t *testing.T) {
	testDir := t.TempDir()
	testCases := []struct {
		name            string
		scriptName      string
		expectedToExist string
		args            map[string]interface{}
	}{
		{
			name:            "preinst script initialization",
			scriptName:      PreInstScriptName,
			expectedToExist: filepath.Join(testDir, PreInstScriptName),
			args:            map[string]interface{}{},
		},
		{
			name:            "postinst script initialization",
			scriptName:      PostInstScriptName,
			expectedToExist: filepath.Join(testDir, PostInstScriptName),
			args:            map[string]interface{}{},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			err := initScript(testDir, testCase.scriptName, testCase.args)
			require.NoErrorf(t, err, "Failed to init a script: %s", err)
			require.FileExists(t, testCase.expectedToExist, "Script file doesn't exist")
		})
	}
}
