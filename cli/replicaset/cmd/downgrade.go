package replicasetcmd

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

// DowngradeOpts contains options used for the downgrade process.
type DowngradeOpts struct {
	// ChosenReplicasetAliases is a list of replicaset names specified by
	// the user for the downgrade.
	ChosenReplicasetAliases []string
	// Timeout period (in seconds) for waiting on LSN synchronization.
	Timeout int
	// DowngradeVersion is a version to downgrade the schema to.
	DowngradeVersion string
}

//go:embed lua/downgrade.lua
var downgradeMasterLua string

func filterComments(script string) string {
	var filteredLines []string
	lines := strings.Split(script, "\n")
	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmedLine, "--") {
			filteredLines = append(filteredLines, line)
		}
	}
	return strings.Join(filteredLines, "\n")
}

// Downgrade downgrades tarantool schema.
func Downgrade(discoveryCtx DiscoveryCtx, opts DowngradeOpts,
	connOpts connector.ConnectOpts,
) error {
	replicasets, err := getReplicasets(discoveryCtx)
	if err != nil {
		return err
	}

	replicasets = fillAliases(replicasets)
	replicasetsToDowngrade, err := filterReplicasetsByAliases(replicasets,
		opts.ChosenReplicasetAliases)
	if err != nil {
		return err
	}

	return internalDowngrade(replicasetsToDowngrade, opts.Timeout,
		opts.DowngradeVersion, connOpts)
}

func internalDowngrade(replicasets []replicaset.Replicaset, lsnTimeout int, version string,
	connOpts connector.ConnectOpts,
) error {
	for _, replicaset := range replicasets {
		err := downgradeReplicaset(replicaset, lsnTimeout, version, connOpts)
		if err != nil {
			fmt.Printf("• %s: error\n", replicaset.Alias)
			return fmt.Errorf("replicaset %s: %w", replicaset.Alias, err)
		}
		fmt.Printf("• %s: ok\n", replicaset.Alias)
	}
	return nil
}

func downgradeMaster(master *instanceMeta, version string) (syncInfo, error) {
	var downgradeInfo syncInfo
	fullMasterName := running.GetAppInstanceName(master.run)
	res, err := master.conn.Eval(filterComments(downgradeMasterLua),
		[]interface{}{version}, connector.RequestOpts{})
	if err != nil {
		return downgradeInfo, fmt.Errorf(
			"failed to execute downgrade script on master instance - %s: %w",
			fullMasterName, err)
	}

	if err := mapstructure.Decode(res[0], &downgradeInfo); err != nil {
		return downgradeInfo, fmt.Errorf(
			"failed to decode response from master instance - %s: %w",
			fullMasterName, err)
	}

	if downgradeInfo.Err != nil {
		return downgradeInfo, fmt.Errorf(
			"master instance downgrade failed - %s: %s",
			fullMasterName, *downgradeInfo.Err)
	}
	return downgradeInfo, nil
}

func downgradeReplicaset(replicaset replicaset.Replicaset, lsnTimeout int, version string,
	connOpts connector.ConnectOpts,
) error {
	master, replicas, err := collectRWROInfo(replicaset, connOpts)
	if err != nil {
		return err
	}

	defer closeConnectors(master, replicas)

	// Downgrade master instance, collect LSN and IID from master instance.
	downgradeInfo, err := downgradeMaster(master, version)
	if err != nil {
		return err
	}

	// Downgrade replica instances.
	masterLSN := downgradeInfo.LSN
	masterIID := downgradeInfo.IID

	for _, replica := range replicas {
		fullReplicaName := running.GetAppInstanceName(replica.run)
		err := waitLSN(replica.conn, masterIID, masterLSN, lsnTimeout)
		if err != nil {
			return fmt.Errorf("can't ensure that downgrade operations performed on "+
				"%s are replicated to %s to perform snapshotting on it: error "+
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
