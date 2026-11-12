package rockindex

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/tarantool/go-luarocks/remote"
)

// ServerOptions tunes MakeServer.
type ServerOptions struct {
	// HTML also writes index.html, the browsable page of the server.
	HTML bool
	// IfIndexed writes the index only when the directory has one already,
	// keeping it current; a directory without one stays a server of
	// LuaRocks manifests alone.
	IfIndexed bool
	// Warn receives what remote.MakeManifest warns of.
	Warn func(msg string)
}

// MakeServer makes dir a rock server of the rocks it holds: the LuaRocks
// manifests, as `luarocks-admin make_manifest` writes them
// (remote.MakeManifest), which LuaRocks reads, and the index beside them
// (Write), which tt reads first, so that both list the same rocks. The
// Result is that of Write, the zero one when IfIndexed wrote no index.
func MakeServer(ctx context.Context, dir string, opts ServerOptions) (Result, error) {
	err := remote.MakeManifest(ctx, dir, remote.MakeManifestOpts{
		LuaVersions: nil, Index: opts.HTML, Zip: false, Warn: opts.Warn,
	})
	if err != nil {
		return Result{}, fmt.Errorf("indexing %s: %w", dir, err)
	}

	if opts.IfIndexed {
		_, err = os.Stat(filepath.Join(dir, File))
		if errors.Is(err, fs.ErrNotExist) {
			return Result{}, nil
		}
	}

	result, err := Write(ctx, Options{Dir: dir, Index: "", BaseURL: ""})
	if err != nil {
		return Result{}, fmt.Errorf("indexing %s: %w", dir, err)
	}

	return result, nil
}
