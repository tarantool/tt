package running

import (
	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/util/regexputil"
)

// configVars returns the variables tarantool substitutes into the config of
// the instance name of the cluster config cfg: instance_name, and
// replicaset_name and group_name when cfg declares the instance.
func configVars(cfg *goconfig.MutableConfig, name string) map[string]string {
	vars := map[string]string{"instance_name": name}

	if cfg == nil {
		return vars
	}

	group, replicaset, found := cluster.FindInstance(cfg.Snapshot(), name)
	if found {
		vars["replicaset_name"] = replicaset
		vars["group_name"] = group
	}

	return vars
}

// applyConfigVars substitutes vars into value, as tarantool substitutes them
// into a string of the config of an instance. A variable vars does not have
// is left as it is: renderInstCtxMembers reports it for the paths tt keeps,
// and tarantool refuses it when the instance starts.
func applyConfigVars(value string, vars map[string]string) string {
	if len(vars) == 0 {
		return value
	}

	rendered, _ := regexputil.ApplyVars(value, vars)

	return rendered
}
