package pack

import (
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/config"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateControlDir(t *testing.T) {
	testCases := []struct {
		name         string
		ctx          *cmdcontext.PackCtx
		destPath     string
		correctError func(err error) bool
		correctDir   func(controlPath string) bool
	}{
		{
			name: "All correct parameters",
			ctx: &cmdcontext.PackCtx{
				Name:    "test",
				Version: "1.0.0",
				RpmDeb: cmdcontext.RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
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
			name: "Default case",
			ctx: &cmdcontext.PackCtx{
				Name:    "",
				Version: "",
				RpmDeb: cmdcontext.RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
				},
				App: &config.AppOpts{
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
			ctx: &cmdcontext.PackCtx{
				Name:    "",
				Version: "",
				RpmDeb: cmdcontext.RpmDebCtx{
					Deps:              []string{"tarantool==master"},
					WithTarantoolDeps: false,
				},
				App: &config.AppOpts{
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
			ctx: &cmdcontext.PackCtx{
				Name:    "test",
				Version: "1.0.0",
				RpmDeb: cmdcontext.RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
					PostInst:          "nothing",
				},
				App: &config.AppOpts{
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
			ctx: &cmdcontext.PackCtx{
				Name:    "test",
				Version: "1.0.0",
				RpmDeb: cmdcontext.RpmDebCtx{
					Deps:              []string{"tarantool>=1.10"},
					WithTarantoolDeps: false,
					PreInst:           "nothing",
				},
				App: &config.AppOpts{
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
			err := createControlDir(testCase.ctx, path)
			require.Truef(t, testCase.correctError(err), "wrong error caught: %v", err)
			require.Truef(t, testCase.correctDir(path), "wrong directory structure")
		})
	}
}

func TestParseDependencies(t *testing.T) {
	testCases := []struct {
		name         string
		deps         []string
		expectedDeps PackDependencies
		correctError func(err error) bool
	}{
		{
			name: "Correct dependencies",
			deps: []string{
				"tarantool<=1.10",
				"tarantool==1.10",
				"tarantool>=1.10",
				"tarantool=1.10",
				"tarantool>1.10",
				"tarantool<1.10",
			},
			expectedDeps: []PackDependency{
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: "<=", Version: "1.10"},
					},
				},
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: "==", Version: "1.10"},
					},
				},
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: ">=", Version: "1.10"},
					},
				},
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: "=", Version: "1.10"},
					},
				},
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: ">", Version: "1.10"},
					},
				},
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: "<", Version: "1.10"},
					},
				},
			},
			correctError: func(err error) bool {
				return err == nil
			},
		},
		{
			name: "Incorrect dependencies",
			deps: []string{
				"tt=master",
				"tt<<1.10",
			},
			expectedDeps: []PackDependency{
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: "<=", Version: "1.10"},
					},
				},
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: "==", Version: "1.10"},
					},
				},
			},
			correctError: func(err error) bool {
				return strings.Contains(err.Error(), "unexpected token \"master\"")
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			deps, err := parseDependencies(testCase.deps)
			require.Truef(t, testCase.correctError(err), "wrong error caught: %v", err)

			for i, _ := range deps {
				require.Equalf(t, testCase.expectedDeps[i].Name, deps[i].Name,
					"wrong dependency name, expected: %s, got: %s",
					testCase.expectedDeps[i].Name, deps[i].Name)
				require.Equalf(t, testCase.expectedDeps[i].Relations[0], deps[i].Relations[0],
					"wrong relation, expected: %s, got: %s",
					testCase.expectedDeps[i].Name, deps[i].Name)
			}
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
			}},
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
