package build

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/cli/manifest"
)

func TestFileFilter_defaultsKeepLuaAndSo(t *testing.T) {
	t.Parallel()

	pathFilter := newFileFilter(manifest.Component{})

	assert.True(t, pathFilter.keepFile([]string{"init.lua"}))
	assert.True(t, pathFilter.keepFile([]string{"lib", "foo.lua"}))
	assert.True(t, pathFilter.keepFile([]string{"fast_hash.so"}))
	assert.False(t, pathFilter.keepFile([]string{"README.md"}))
	assert.False(t, pathFilter.keepFile([]string{"fast_hash.c"}))
}

func TestFileFilter_defaultExcludesAlwaysApply(t *testing.T) {
	t.Parallel()

	// A component whose include list would otherwise match everything still
	// cannot ship the hazardous defaults.
	pathFilter := newFileFilter(manifest.Component{Include: []string{"*"}})

	assert.False(t, pathFilter.keepFile([]string{".hidden"}))
	assert.False(t, pathFilter.keepFile([]string{manifestFileName}))
	assert.True(t, pathFilter.pruneDir([]string{"test"}))
	assert.True(t, pathFilter.pruneDir([]string{"tests"}))
	assert.True(t, pathFilter.pruneDir([]string{"_build"}))
	assert.True(t, pathFilter.pruneDir([]string{".rocks"}))
	assert.True(t, pathFilter.pruneDir([]string{"_runtime"}))
	assert.True(t, pathFilter.pruneDir([]string{"vendor"}))
	assert.True(t, pathFilter.pruneDir([]string{".git"}))
}

func TestFileFilter_customIncludeReplacesDefault(t *testing.T) {
	t.Parallel()

	// A component narrowing to these two patterns no longer picks up a *.so at
	// the root. An unanchored *.lua matches .lua files at any depth (gitignore
	// basename matching).
	pathFilter := newFileFilter(manifest.Component{Include: []string{"*.lua", "lib/*.lua"}})

	assert.True(t, pathFilter.keepFile([]string{"init.lua"}))
	assert.True(t, pathFilter.keepFile([]string{"lib", "helper.lua"}))
	assert.True(t, pathFilter.keepFile([]string{"lib", "inner", "deep.lua"}))
	assert.False(t, pathFilter.keepFile([]string{"prebuilt.so"}))
}

func TestFileFilter_anchoredIncludeIsDepthLimited(t *testing.T) {
	t.Parallel()

	// A slashed pattern is anchored to the component root: lib/*.lua matches
	// files directly under lib/, not deeper and not at the root.
	pathFilter := newFileFilter(manifest.Component{Include: []string{"lib/*.lua"}})

	assert.True(t, pathFilter.keepFile([]string{"lib", "helper.lua"}))
	assert.False(t, pathFilter.keepFile([]string{"lib", "inner", "deep.lua"}))
	assert.False(t, pathFilter.keepFile([]string{"init.lua"}))
}

func TestFileFilter_customExcludeExtendsDefault(t *testing.T) {
	t.Parallel()

	pathFilter := newFileFilter(manifest.Component{Exclude: []string{"*.bak"}})

	assert.False(t, pathFilter.keepFile([]string{"old.lua.bak"}))
	// The defaults are still in force alongside the custom pattern.
	assert.False(t, pathFilter.keepFile([]string{".hidden.lua"}))
	assert.True(t, pathFilter.keepFile([]string{"init.lua"}))
}
