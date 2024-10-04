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

// UpgradeOpts contains options used for the upgrade process.
type UpgradeOpts struct {
	// List of replicaset names specified by the user for the upgrade.
	ChosenReplicasetAliases []string
	// Timeout period (in seconds) for waiting on LSN synchronization.
	LsnTimeout int
}

type instanceMeta struct {
	run  running.InstanceCtx
	conn connector.Connector
}

//go:embed lua/upgrade.lua
var upgradeMasterLua string

type syncInfo struct {
	LSN uint64  `mapstructure:"lsn"`
	IID uint32  `mapstructure:"iid"`
	Err *string `mapstructure:"err"`
}

// filterReplicasetsByAliases filters the given replicaset list by chosen aliases and
// returns chosen replicasets. If a non-existent alias is found, it returns an error.
func filterReplicasetsByAliases(replicasets replicaset.Replicasets,
	chosenReplicasetAliases []string) ([]replicaset.Replicaset, error) {
	// If no aliases are provided, return all replicasets.
	if len(chosenReplicasetAliases) == 0 {
		return replicasets.Replicasets, nil
	}

	// Create a map for fast lookup of replicasets by alias.
	replicasetMap := make(map[string]replicaset.Replicaset)
	for _, rs := range replicasets.Replicasets {
		replicasetMap[rs.Alias] = rs
	}

	var chosenReplicasets []replicaset.Replicaset
	for _, alias := range chosenReplicasetAliases {
		rs, exists := replicasetMap[alias]
		if !exists {
			return nil, fmt.Errorf("replicaset with alias %q doesn't exist", alias)
		}
		chosenReplicasets = append(chosenReplicasets, rs)
	}

	return chosenReplicasets, nil
}

// Upgrade upgrades tarantool schema.
func Upgrade(discoveryCtx DiscoveryCtx, opts UpgradeOpts) error {
	replicasets, err := getReplicasets(discoveryCtx)
	if err != nil {
		return err
	}

	replicasets = fillAliases(replicasets)
	replicasetsToUpgrade, err := filterReplicasetsByAliases(replicasets,
		opts.ChosenReplicasetAliases)
	if err != nil {
		return err
	}

	return internalUpgrade(replicasetsToUpgrade, opts.LsnTimeout)
}

func internalUpgrade(replicasets []replicaset.Replicaset, lsnTimeout int) error {
	for _, replicaset := range replicasets {
		err := upgradeReplicaset(replicaset, lsnTimeout)
		if err != nil {
			fmt.Printf("• %s: error\n", replicaset.Alias)
			return fmt.Errorf("replicaset %s: %w", replicaset.Alias, err)
		}
		fmt.Printf("• %s: ok\n", replicaset.Alias)
	}
	return nil
}

func closeConnectors(master *instanceMeta, replicas []instanceMeta) {
	if master != nil {
		master.conn.Close()
	}
	for _, replica := range replicas {
		replica.conn.Close()
	}
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

	// Try to connect via unix socket.
	conn, err := connector.Connect(connector.ConnectOpts{
		Network: "unix",
		Address: run.ConsoleSocket,
	})

	if err != nil {
		return nil, fmt.Errorf("instance %s failed to connect via UNIX socket "+
			": %w", fullInstanceName, err)
	}
	return conn, nil
}

func collectRWROInfo(replset replicaset.Replicaset) (*instanceMeta, []instanceMeta,
	error) {
	var master *instanceMeta = nil
	var replicas []instanceMeta
	for _, instance := range replset.Instances {
		run := instance.InstanceCtx
		fullInstanceName := running.GetAppInstanceName(run)
		conn, err := getInstanceConnector(instance)

		if err != nil {
			return nil, nil, err
		}

		if instance.Mode == replicaset.ModeUnknown {
			closeConnectors(master, replicas)
			return nil, nil, fmt.Errorf(
				"can't determine RO/RW mode on instance: %s", fullInstanceName)
		}

		isRW := instance.Mode.String() == "rw"

		if isRW && master != nil {
			closeConnectors(master, replicas)
			return nil, nil, fmt.Errorf("%s and %s are both masters",
				running.GetAppInstanceName((*master).run), fullInstanceName)
		} else if isRW {
			master = &instanceMeta{run, conn}
		} else {
			replicas = append(replicas, instanceMeta{run, conn})
		}
	}
	return master, replicas, nil
}

func waitLSN(conn connector.Connector, masterIID uint32, masterLSN uint64, lsnTimeout int) error {
	var lastError error
	query := fmt.Sprintf("return box.info.vclock[%d]", masterIID)

	deadline := time.Now().Add(time.Duration(lsnTimeout) * time.Second)
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

func upgradeMaster(master *instanceMeta) (syncInfo, error) {
	var upgradeInfo syncInfo
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

func snapshot(instance *instanceMeta) error {
	res, err := instance.conn.Eval("return box.snapshot()", []any{},
		connector.RequestOpts{})
	if err != nil {
		return fmt.Errorf("failed to execute snapshot on replica: %w", err)
	}
	if len(res) == 0 {
		return fmt.Errorf("snapshot command on %s returned an empty result, "+
			"'ok' expected", running.GetAppInstanceName(instance.run))
	}

	if result, ok := res[0].(string); !ok || result != "ok" {
		return fmt.Errorf("snapshot command on %s returned unexpected result: '%v', "+
			"'ok' expected", running.GetAppInstanceName(instance.run), res[0])
	}
	return nil
}

func upgradeReplicaset(replicaset replicaset.Replicaset, lsnTimeout int) error {
	master, replicas, err := collectRWROInfo(replicaset)
	if err != nil {
		return err
	}

	defer closeConnectors(master, replicas)

	// Upgrade master instance, collect LSN and IID from master instance.
	upgradeInfo, err := upgradeMaster(master)
	if err != nil {
		return err
	}

	// Upgrade replica instances.
	masterLSN := upgradeInfo.LSN
	masterIID := upgradeInfo.IID

	for _, replica := range replicas {
		fullReplicaName := running.GetAppInstanceName(replica.run)
		err := waitLSN(replica.conn, masterIID, masterLSN, lsnTimeout)
		if err != nil {
			return fmt.Errorf("can't ensure that upgrade operations performed on %s "+
				"are replicated to %s to perform snapshotting on it: error "+
				"waiting LSN %d in vclock component %d: %w",
				running.GetAppInstanceName(master.run), fullReplicaName,
				masterLSN, masterIID, err)
		}
		err = snapshot(&replica)
		if err != nil {
			return err
		}
	}
	return nil
}
