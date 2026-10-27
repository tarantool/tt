package running

import (
	"context"
	"errors"
	"fmt"

	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/instances"
)

// ErrConfigStorageCE is wrapped by the error for a Tarantool instance of the
// cluster config whose config is kept in a config storage, to be run on
// Tarantool Community Edition, which does not read one.
var ErrConfigStorageCE = errors.New("a config storage needs Tarantool Enterprise Edition")

// storageReference returns the section of the config of inst, a Tarantool
// instance of the cluster config, that points to a config storage,
// config.etcd or config.storage, and false when it has none or inst is no
// such instance.
func storageReference(inst *InstanceCtx) (string, bool) {
	if inst.Kind != instances.KindCluster || inst.Configuration == nil {
		return "", false
	}

	return cluster.StorageReference(*inst.Configuration)
}

// checkConfigStorage refuses to run inst on exe, whose flavor is flavor, when
// the config of inst points to a config storage and exe is Tarantool
// Community Edition: Tarantool takes config.etcd and config.storage in
// Enterprise Edition alone, and the instance would stop right after it
// starts, saying why in its tt.log only. tt reads the storage itself in
// either edition, so the rest of the commands are not concerned. A flavor
// that cannot be told lets the instance through, as before.
func checkConfigStorage(inst *InstanceCtx, exe tarantool.Executable, flavor string) error {
	key, stored := storageReference(inst)
	if !stored || flavor != tarantool.FlavorCE {
		return nil
	}

	return fmt.Errorf("instance %q of %q: %w: %s of its cluster config points to a config "+
		"storage, which Tarantool reads in Enterprise Edition alone, and the instance "+
		"runs on %s, Tarantool Community Edition; run it on Tarantool Enterprise Edition, "+
		"or keep the whole cluster config in %s", inst.InstName, inst.AppName,
		ErrConfigStorageCE, key, exe.Path, inst.ClusterConfigPath)
}

// CheckTarantool checks that the tarantool inst runs on can run it, for
// tt instance check: a Tarantool instance of the cluster config whose config
// points to a config storage needs Tarantool Enterprise Edition
// (checkConfigStorage). The other instances are not checked.
func CheckTarantool(ctx context.Context, inst *InstanceCtx) error {
	_, stored := storageReference(inst)
	if !stored {
		return nil
	}

	//nolint:contextcheck // The scope roots ask uname and take no context.
	exe, err := resolveTarantool(inst)
	if err != nil {
		return err
	}

	return checkConfigStorage(inst, exe, exe.Flavor(ctx))
}
