// Package cmakepolicy lets CMake 4 configure the old projects tt builds from
// source: the commits of Tarantool and the rocks their pinned code freezes.
//
// CMake 4.0 configures no project that asks for a CMake older than 3.5 with
// cmake_minimum_required(VERSION) or cmake_policy(VERSION), as a Tarantool of
// 2024 or older and many rocks do (VERSION 2.8), unless it is told the policy
// version to assume: by the CMAKE_POLICY_VERSION_MINIMUM cache variable, which
// the environment variable of the same name initializes in a new build tree.
// A CMake older than 4.0 does not know the variable and ignores it, so tt sets
// it for every cmake of such a build. It never sets it over the user's: a value
// the environment has, the empty one included, is kept.
//
// The builds of the components of a project are the user's code, which tt does
// not lower the policy of: a component of an old CMake fails to configure as
// CMake 4 says, and the user sets the variable for it if they mean to.
package cmakepolicy

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"sync/atomic"
)

const (
	// Variable is the environment variable CMake 4 takes the default of the
	// CMAKE_POLICY_VERSION_MINIMUM cache entry of a new build tree from.
	Variable = "CMAKE_POLICY_VERSION_MINIMUM"
	// Version is the policy version tt sets: the oldest one CMake 4 still
	// configures a project with, the one its error message suggests.
	Version = "3.5"
)

// defaulted says that Setenv has set Variable in the environment of tt.
var defaulted atomic.Bool

// isSet reports whether environ, as os.Environ has it, sets Variable.
func isSet(environ []string) bool {
	return slices.ContainsFunc(environ, isVariable)
}

// isVariable reports whether entry, key=value, sets Variable.
func isVariable(entry string) bool {
	return strings.HasPrefix(entry, Variable+"=")
}

// Environ returns the environment environ of a build of old code with
// Variable=Version added, unless environ sets Variable, to whatever value.
// environ itself is not changed.
func Environ(environ []string) []string {
	if isSet(environ) {
		return environ
	}

	return append(slices.Clip(environ), Variable+"="+Version)
}

// Setenv sets Variable to Version in the environment of tt, unless it is set
// there, for the builds tt does not start itself and that inherit it: those
// of rocks, which go-luarocks runs with the environment of the process.
// Strip takes it out of the environment of the builds of the user's code.
func Setenv() error {
	if _, ok := os.LookupEnv(Variable); ok {
		return nil
	}

	err := os.Setenv(Variable, Version)
	if err != nil {
		return fmt.Errorf("setting %s: %w", Variable, err)
	}

	defaulted.Store(true)

	return nil
}

// Strip returns environ, the environment of tt as os.Environ has it, without
// the Variable=Version Setenv has set there rather than the user: the
// environment of a build of the user's code. environ itself is not changed.
func Strip(environ []string) []string {
	entry := Variable + "=" + Version

	if !defaulted.Load() || !slices.Contains(environ, entry) {
		return environ
	}

	return slices.DeleteFunc(slices.Clone(environ), func(e string) bool { return e == entry })
}
