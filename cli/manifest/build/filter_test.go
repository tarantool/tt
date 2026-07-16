package build

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/manifest"
)

func TestFileFilter_defaultsKeepLuaAndSo(t *testing.T) {
	t.Parallel()

	f := newFileFilter(manifest.Component{})

	assert.True(t, f.keepFile([]string{"init.lua"}))
	assert.True(t, f.keepFile([]string{"lib", "foo.lua"}))
	assert.True(t, f.keepFile([]string{"fast_hash.so"}))
	assert.False(t, f.keepFile([]string{"README.md"}))
	assert.False(t, f.keepFile([]string{"fast_hash.c"}))
}

func TestFileFilter_defaultExcludesAlwaysApply(t *testing.T) {
	t.Parallel()

	// A component whose include list would otherwise match everything still
	// cannot ship the hazardous defaults.
	f := newFileFilter(manifest.Component{Include: []string{"*"}})

	assert.False(t, f.keepFile([]string{".hidden"}))
	assert.False(t, f.keepFile([]string{manifestFileName}))
	assert.True(t, f.pruneDir([]string{"test"}))
	assert.True(t, f.pruneDir([]string{"tests"}))
	assert.True(t, f.pruneDir([]string{"_build"}))
	assert.True(t, f.pruneDir([]string{".rocks"}))
	assert.True(t, f.pruneDir([]string{"_runtime"}))
	assert.True(t, f.pruneDir([]string{"vendor"}))
	assert.True(t, f.pruneDir([]string{".git"}))
}

func TestFileFilter_customIncludeReplacesDefault(t *testing.T) {
	t.Parallel()

	// A component narrowing to these two patterns no longer picks up a *.so at
	// the root. An unanchored *.lua matches .lua files at any depth (gitignore
	// basename matching).
	f := newFileFilter(manifest.Component{Include: []string{"*.lua", "lib/*.lua"}})

	assert.True(t, f.keepFile([]string{"init.lua"}))
	assert.True(t, f.keepFile([]string{"lib", "helper.lua"}))
	assert.True(t, f.keepFile([]string{"lib", "inner", "deep.lua"}))
	assert.False(t, f.keepFile([]string{"prebuilt.so"}))
}

func TestFileFilter_anchoredIncludeIsDepthLimited(t *testing.T) {
	t.Parallel()

	// A slashed pattern is anchored to the component root: lib/*.lua matches
	// files directly under lib/, not deeper and not at the root.
	f := newFileFilter(manifest.Component{Include: []string{"lib/*.lua"}})

	assert.True(t, f.keepFile([]string{"lib", "helper.lua"}))
	assert.False(t, f.keepFile([]string{"lib", "inner", "deep.lua"}))
	assert.False(t, f.keepFile([]string{"init.lua"}))
}

func TestFileFilter_customExcludeExtendsDefault(t *testing.T) {
	t.Parallel()

	f := newFileFilter(manifest.Component{Exclude: []string{"*.bak"}})

	assert.False(t, f.keepFile([]string{"old.lua.bak"}))
	// The defaults are still in force alongside the custom pattern.
	assert.False(t, f.keepFile([]string{".hidden.lua"}))
	assert.True(t, f.keepFile([]string{"init.lua"}))
}
