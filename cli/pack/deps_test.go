package pack

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseDependenciesFromFile(t *testing.T) {
	testDir := t.TempDir()
	testCases := []struct {
		name            string
		depsFilePath    string
		depsFileContent string
		expectedDeps    []PackDependency
		expectedError   error
	}{
		{
			name:            "Correct file",
			depsFilePath:    filepath.Join(testDir, "deps_file.txt"),
			depsFileContent: "tarantool<=1.10\ntt==0.1.0\n",
			expectedDeps: []PackDependency{
				{
					Name: "tarantool",
					Relations: []DepRelation{
						{Relation: "<=", Version: "1.10"},
					},
				},
				{
					Name: "tt",
					Relations: []DepRelation{
						{Relation: "==", Version: "0.1.0"},
					},
				},
			},
			expectedError: nil,
		},
		{
			name:            "Bad file content",
			depsFilePath:    filepath.Join(testDir, "deps_file.txt"),
			depsFileContent: "tarantool<=1.10tt==0.1.0\n",
			expectedDeps:    nil,
			expectedError: fmt.Errorf("Failed to parse dependencies file: " +
				"Error during parse dependencies file:"),
		},
		{
			name:            "File doesn't exist",
			depsFilePath:    filepath.Join(testDir, "fake_deps_file.txt"),
			depsFileContent: "tarantool<=1.10tt==0.1.0\n",
			expectedDeps:    nil,
			expectedError: fmt.Errorf("Failed to parse dependencies file: " +
				"Error during parse dependencies file:"),
		},
	}

	for _, testCase := range testCases {
		depsFile, err := os.Create(testCase.depsFilePath)
		require.NoErrorf(t, err, "Failed to create a test file.")

		_, err = depsFile.WriteString(testCase.depsFileContent)
		require.NoErrorf(t, err, "Failed to write content to test file.")

		deps, err := parseDependenciesFromFile(testCase.depsFilePath)
		if testCase.expectedError == nil {
			require.NoErrorf(t, err, "Failed to write content to test file.")
		} else {
			require.Contains(t, err.Error(), testCase.expectedError.Error())
		}

		for i, _ := range testCase.expectedDeps {
			require.Equal(t, testCase.expectedDeps[i].Name, deps[i].Name)
			require.Equal(t, len(testCase.expectedDeps[i].Relations), len(deps[i].Relations))
			for j, _ := range testCase.expectedDeps[i].Relations {
				require.Equal(t, testCase.expectedDeps[i].Relations[j].Relation,
					deps[i].Relations[j].Relation)
				require.Equal(t, testCase.expectedDeps[i].Relations[j].Version,
					deps[i].Relations[j].Version)
			}
		}
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
