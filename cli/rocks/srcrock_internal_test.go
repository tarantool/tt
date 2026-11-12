package rocks

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/rockspec"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

func TestRockTree(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"default", []string{"install", "a"}, "/work/.rocks"},
		{"flag and value", []string{"install", "a", "--tree", "t"}, "/work/t"},
		{"flag=value", []string{"--tree=/abs", "install", "a"}, "/abs"},
		{"last wins", []string{"--tree=a", "--tree", "b"}, "/work/b"},
		{"flag without a value", []string{"install", "--tree"}, "/work/.rocks"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, rockTree("/work", tc.args))
		})
	}
}

// installRock installs the rock name 1.0.0-1 into root with the files of
// stage, as paths of an unpacked rock to their contents, and the rockspec
// src, or the one tt packs when src is nil.
func installRock(t *testing.T, root, name string, files map[string]string, src []byte) {
	t.Helper()

	var (
		spec *luarocks.Rockspec
		err  error
	)

	if src == nil {
		spec, err = rocktree.ReleaseRockspec(rocktree.Release{
			Name: name, Version: "1.0.0", Revision: 1, Summary: name,
		})
	} else {
		spec, err = rockspec.EvalSource(name+"-1.0.0-1.rockspec", src, luarocks.RockspecConfig{})
	}

	require.NoError(t, err)

	stage := t.TempDir()

	for rel, body := range files {
		path := filepath.Join(stage, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	}

	rocksTree, err := tree.Open(luarocks.Config{Tree: root})
	require.NoError(t, err)

	err = rocksTree.Update(context.Background(), func(change *tree.Tx) error {
		_, installErr := change.InstallStaged(spec, stage, tree.StagedOpts{})

		return installErr
	})
	require.NoError(t, err)
}

// versions lists the versions of the rock name in root.
func versions(t *testing.T, root, name string) []string {
	t.Helper()

	view, err := tree.Inspect(context.Background(), root)
	require.NoError(t, err)

	return view.Versions(name)
}

func TestCheckSrcRocks(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), ".rocks")
	docOnly := map[string]string{"doc/LICENSE": "license\n"}

	// A rock installed before the command is not the command's.
	installRock(t, root, "old", docOnly, nil)

	before := installedRocks(ctx, root)

	// A binary rock of a tt package holds its modules.
	installRock(t, root, "binary", map[string]string{
		"lua/binary/init.lua": "return {}\n", "doc/LICENSE": "license\n",
	}, nil)
	// A rock of a rockspec tt did not write may hold nothing on purpose.
	installRock(t, root, "meta", nil, []byte(
		"package = \"meta\"\nversion = \"1.0.0-1\"\nsource = { url = \"file://.\" }\n"+
			"build = { type = \"none\" }\n"))
	// The source rock of a tt package, which LuaRocks built nothing of.
	installRock(t, root, "hashkit", docOnly, nil)

	err := checkSrcRocks(ctx, root, before)
	require.ErrorIs(t, err, ErrEmptySrcRock)
	assert.Contains(t, err.Error(), "hashkit 1.0.0-1;")
	assert.NotContains(t, err.Error(), "old")

	assert.Empty(t, versions(t, root, "hashkit"))
	assert.Equal(t, []string{"1.0.0-1"}, versions(t, root, "old"))
	assert.Equal(t, []string{"1.0.0-1"}, versions(t, root, "binary"))
	assert.Equal(t, []string{"1.0.0-1"}, versions(t, root, "meta"))

	// Nothing new: nothing to refuse.
	require.NoError(t, checkSrcRocks(ctx, root, installedRocks(ctx, root)))
}

func TestCheckSrcRocksNoTree(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), ".rocks")

	require.NoError(t, checkSrcRocks(ctx, root, installedRocks(ctx, root)))
	assert.NoDirExists(t, root)
}
