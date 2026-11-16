package app

import (
	"context"
	"maps"
	"slices"

	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/v3/cli/cluster"
)

// AppFiles lists the app.file values the instances of the cluster config
// data, a config.yaml, run with, each once, in the order of the names of the
// instances: the value of an instance is the one of its own level, or of its
// replica set, group or the global one, as the instance gets it. The file
// alone is read, without the TT_ environment, as CheckTemplate reads it. A
// config that does not build lists none: the check of the template says what
// is wrong with it.
//
// tt package pack reads them to tell an app.file the archive does not carry,
// which every installed copy of the package would fail to load.
func AppFiles(ctx context.Context, data []byte) []string {
	cfg, err := cluster.BuildGoConfigFromBytes(ctx, data)
	if err != nil {
		return nil
	}

	all, err := cfg.EffectiveAll()
	if err != nil {
		return nil
	}

	var files []string

	for _, name := range slices.Sorted(maps.Keys(all)) {
		var file string

		instance := all[name]

		_, err := instance.Get(goconfig.NewKeyPath("app/file"), &file)
		if err == nil && file != "" && !slices.Contains(files, file) {
			files = append(files, file)
		}
	}

	return files
}
