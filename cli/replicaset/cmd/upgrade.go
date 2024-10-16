package replicasetcmd

import (
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

var (
	ChosenReplicasetAliases []string
	LsnTimeout              int
)

type InstanceMeta struct {
	run  running.InstanceCtx
	conn connector.Connector
}

//go:embed lua/upgrade.lua
var upgradeMasterLua string

type SyncInfo struct {
	LSN uint64  `mapstructure:"lsn"`
	IID uint32  `mapstructure:"iid"`
	Err *string `mapstructure:"err"`
}

// "FilterReplicasetsByAliases" filters the given replicaset list by chosen aliases
// and returns the chosen replicasets. If a non-existent alias is found, it returns an error.
func FilterReplicasetsByAliases(replicasets replicaset.Replicasets) ([]replicaset.Replicaset,
	error) {
	// If no aliases are provided, return all replicasets.
	if len(ChosenReplicasetAliases) == 0 {
		return replicasets.Replicasets, nil
	}

	// Create a map for fast lookup of replicasets by alias
	replicasetMap := make(map[string]replicaset.Replicaset)
	for _, rs := range replicasets.Replicasets {
		replicasetMap[rs.Alias] = rs
	}

	var chosenReplicasets []replicaset.Replicaset
	for _, alias := range ChosenReplicasetAliases {
		rs, exists := replicasetMap[alias]
		if !exists {
			return nil, fmt.Errorf("replicaset with alias %q doesn't exist", alias)
		}
		chosenReplicasets = append(chosenReplicasets, rs)
	}

	return chosenReplicasets, nil
}

func Upgrade(discoveryCtx DiscoveryCtx) error {
	replicasets, err := GetReplicasets(discoveryCtx)
	if err != nil {
		// This may be a single-instance application without Tarantool-3 config
		// or instances.yml file.
		if len(discoveryCtx.RunningCtx.Instances) == 1 {
			// Create a dummy replicaset
			var replicasetList []replicaset.Replicaset
			var dummyReplicaset replicaset.Replicaset
			var instance replicaset.Instance

			instance.InstanceCtx = discoveryCtx.RunningCtx.Instances[0]
			instance.Alias = running.GetAppInstanceName(instance.InstanceCtx)
			instance.InstanceCtxFound = true

			dummyReplicaset.Alias = instance.Alias
			dummyReplicaset.Instances = append(dummyReplicaset.Instances, instance)
			replicasetList = append(replicasetList, dummyReplicaset)

			return internalUpgrade(replicasetList)
		}
		return err
	}

	replicasets = FillAliases(replicasets)
	replicasetsToUpgrade, err := FilterReplicasetsByAliases(replicasets)
	if err != nil {
		return err
	}

	return internalUpgrade(replicasetsToUpgrade)
}

func internalUpgrade(replicasets []replicaset.Replicaset) error {
	for _, replicaset := range replicasets {
		err := upgradeReplicaset(replicaset)
		if err != nil {
			fmt.Printf("• %s: error\n", replicaset.Alias)
			return fmt.Errorf("replicaset %s: %w", replicaset.Alias, err)
		}
		fmt.Printf("• %s: ok\n", replicaset.Alias)
	}
	return nil
}

func getInstanceConnector(instance replicaset.Instance) (connector.Connector, error) {
	run := instance.InstanceCtx
	fullInstanceName := running.GetAppInstanceName(run)
	if fullInstanceName == "" {
		fullInstanceName = instance.Alias
	}
	if fullInstanceName == "" {
		fullInstanceName = "unknown"
	}

	// Try to connect via unix socket
	conn, err := connector.Connect(connector.ConnectOpts{
		Network: "unix",
		Address: run.ConsoleSocket,
	})

	if err != nil {
		// try to connect via TCP [experimental]
		conn, err = connector.Connect(connector.ConnectOpts{
			Network:  "tcp",
			Address:  instance.URI,
			Username: "client", // should be opt
			Password: "secret", // should be opt
		})
		if err != nil {
			return nil, fmt.Errorf("instance %s failed to connect via both TCP "+
				"and UNIX socket [%s]: %w", fullInstanceName, instance.URI, err)
		}
	}
	return conn, nil
}

func СollectRWROInfo(replicaset replicaset.Replicaset, master **InstanceMeta,
	replicas *[]InstanceMeta) error {
	for _, instance := range replicaset.Instances {
		run := instance.InstanceCtx
		fullInstanceName := running.GetAppInstanceName(run)
		conn, err := getInstanceConnector(instance)

		if err != nil {
			return err
		}

		var isRW bool
		if instance.Mode.String() != "unknown" {
			isRW = instance.Mode.String() == "rw"
		} else {
			res, err := conn.Eval(
				"return (type(box.cfg) == 'function') or box.info.ro",
				[]any{}, connector.RequestOpts{})
			if err != nil {
				return fmt.Errorf("[%s]: %w", fullInstanceName, err)
			}
			isRW = !res[0].(bool)
		}

		if isRW && *master != nil {
			return fmt.Errorf("%s and %s are both masters",
				running.GetAppInstanceName((*master).run), fullInstanceName)
		} else if isRW {
			*master = &InstanceMeta{run, conn}
		} else {
			*replicas = append(*replicas, InstanceMeta{run, conn})
		}
	}
	return nil
}

func WaitLSN(conn connector.Connector, masterIID uint32, masterLSN uint64) error {
	var lastError error
	query := fmt.Sprintf("return box.info.vclock[%d]", masterIID)

	deadline := time.Now().Add(time.Duration(LsnTimeout) * time.Second)
	for {
		res, err := conn.Eval(query, []any{}, connector.RequestOpts{})
		if err != nil {
			lastError = fmt.Errorf("failed to evaluate LSN query: %w", err)
		} else if len(res) == 0 {
			lastError = errors.New("empty result from LSN query")
		} else {
			var lsn uint64
			if err := mapstructure.Decode(res[0], &lsn); err != nil {
				lastError = fmt.Errorf("failed to decode LSN: %w", err)
			} else if lsn >= masterLSN {
				return nil
			} else {
				lastError = fmt.Errorf("current LSN %d is behind required "+
					"master LSN %d", lsn, masterLSN)
			}
		}

		if time.Now().After(deadline) {
			break
		}

		time.Sleep(1 * time.Second)
	}

	return lastError
}

func upgradeMaster(master *InstanceMeta) (SyncInfo, error) {
	var upgradeInfo SyncInfo
	fullMasterName := running.GetAppInstanceName(master.run)
	res, err := master.conn.Eval(upgradeMasterLua, []any{}, connector.RequestOpts{})
	if err != nil {
		return upgradeInfo, fmt.Errorf(
			"failed to execute upgrade script on master instance - %s: %w",
			fullMasterName, err)
	}

	if err := mapstructure.Decode(res[0], &upgradeInfo); err != nil {
		return upgradeInfo, fmt.Errorf(
			"failed to decode response from master instance - %s: %w",
			fullMasterName, err)
	}

	if upgradeInfo.Err != nil {
		return upgradeInfo, fmt.Errorf(
			"master instance upgrade failed - %s: %w",
			fullMasterName, err)
	}
	return upgradeInfo, nil
}

func Snapshot(instance *InstanceMeta) error {
	res, err := instance.conn.Eval("return box.snapshot()", []any{},
		connector.RequestOpts{})
	if err != nil {
		return fmt.Errorf("failed to execute snapshot on replica: %w", err)
	}
	if len(res) == 0 {
		return fmt.Errorf("snapshot command on %s returned an empty result, "+
			"'ok' - expected", running.GetAppInstanceName(instance.run))
	}
	return nil
}

func upgradeReplicaset(replicaset replicaset.Replicaset) error {
	var master *InstanceMeta = nil
	replicas := []InstanceMeta{}

	err := СollectRWROInfo(replicaset, &master, &replicas)
	if err != nil {
		return err
	}

	// upgrade master instance, collect LSN and IID from master instance
	upgradeInfo, err := upgradeMaster(master)
	if err != nil {
		return err
	}

	// upgrade replica instances
	masterLSN := upgradeInfo.LSN
	masterIID := upgradeInfo.IID

	for _, replica := range replicas {
		fullReplicaName := running.GetAppInstanceName(replica.run)
		err := WaitLSN(replica.conn, masterIID, masterLSN)
		if err != nil {
			return fmt.Errorf("can't ensure that upgrade operations performed on %s "+
				"are replicated to %s to perform snapshotting on it: error "+
				"waiting LSN %d in vclock component %d: %w",
				running.GetAppInstanceName(master.run), fullReplicaName,
				masterLSN, masterIID, err)
		}
		err = Snapshot(&replica)
		if err != nil {
			return err
		}
	}
	return nil
}
