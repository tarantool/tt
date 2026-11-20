package replicaset

import (
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mitchellh/mapstructure"
	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/sdk/v3/log"

	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/running"
)

// Expel removes an instance from its replicaset in the order of the
// Tarantool documentation:
// https://www.tarantool.io/en/doc/latest/platform/replication/
// replication_tutorials/repl_bootstrap/#removing-instances
//
// The instance is to be stopped first, on its host, which only its own tt
// manages: expel may run anywhere, so it does not stop the instance, and
// never asks it anything. It checks instead, on the other members of the
// replicaset that tt reaches, that none is still connected to it
// (checkExpelPeers), then removes the instance from the config, reloads the
// config of those members and deletes the instance from _cluster on the
// writable one.

var (
	errExpelConnected = errors.New("is still connected to the replicaset")
	errNoExpelPeer    = errors.New("no other instance of the replicaset is reached")

	// cconfigExpelPeerBody returns what a member of a replicaset knows of
	// the member of the name it is given: its UUID, by box.info.replication
	// or _cluster, and the statuses of the replication with it (expelPeer).
	// The body has no comments: the text console gets it as one line.
	//
	//go:embed lua/cconfig/expel_peer_body.lua
	cconfigExpelPeerBody string
)

// idleUpstreams are the statuses of an upstream, box.info.replication[n].
// upstream.status, without a connection to the peer: off, connect before
// the connection is made, disconnected and loading after it failed and
// before it is tried again (loading for an error the peer answered with,
// such as ER_LOADING of an instance that shuts down), and stopped on a
// replication error (enum applier_state in src/box/applier.h of Tarantool,
// and applier_f in src/box/applier.cc). Every other one, from connected and
// auth through the join stages to sync and follow, is of a connection that
// is up. A downstream is follow or stopped (enum relay_state in
// src/box/relay.h), and is absent while off.
var idleUpstreams = []string{upstreamOff, "connect", "disconnected", "loading", "stopped"}

// upstreamOff is the status of an upstream that is off, as Tarantool names
// it: another off than that of a failover or an election mode.
const upstreamOff = "off"

// expelPeer is what a member of the replicaset of an instance to expel, one
// that stays in it, tells of that instance (cconfigExpelPeerBody).
type expelPeer struct {
	// Name and Replicaset are the names of the member and of its
	// replicaset, as it runs.
	Name       string `mapstructure:"name"`
	Replicaset string `mapstructure:"replicaset"`
	// UUID is the UUID of the instance to expel as the member knows it by
	// its name, empty when it does not.
	UUID string `mapstructure:"uuid"`
	// Upstream and Downstream are the statuses of the replication of the
	// member from the instance and to it, empty for none.
	Upstream   string `mapstructure:"upstream"`
	Downstream string `mapstructure:"downstream"`
	// Inst is the context of the member.
	Inst running.InstanceCtx `mapstructure:"-"`
}

// String describes the replication of the member with the instance.
func (peer expelPeer) String() string {
	streams := []string{}

	if peer.Upstream != "" {
		streams = append(streams, "upstream "+peer.Upstream)
	}

	if peer.Downstream != "" {
		streams = append(streams, "downstream "+peer.Downstream)
	}

	return peer.Name + " has " + strings.Join(streams, ", ")
}

// connected reports whether the member is connected to the instance: its
// upstream from it is up, or its downstream to it follows.
func (peer expelPeer) connected() bool {
	upstream := peer.Upstream != "" && !slices.Contains(idleUpstreams, peer.Upstream)

	return upstream || peer.Downstream == "follow"
}

// checkExpelPeers checks that the instance name of the application app can
// be expelled, by what peers, the members that stay in its replicaset, tell
// of it: none of them may be connected to it, which tells that it runs.
// It returns the UUID of the instance, empty when none of them knows it.
//
// force skips the check. With no member at all there is nothing to check on
// and nothing to delete the instance from _cluster on, which is an error,
// unless force, which removes it from the config alone.
func checkExpelPeers(app, name string, peers []expelPeer, force bool) (string, error) {
	if len(peers) == 0 {
		if !force {
			return "", fmt.Errorf("%w: there is nothing to check that %q is stopped on and "+
				"to delete it from _cluster on; run the command where tt manages another "+
				"instance of the replicaset, or pass --force to remove %q from the config alone",
				errNoExpelPeer, name, name)
		}

		log.Warnf("No other instance of the replicaset is reached: %q is removed from the "+
			"config alone. Delete it from box.space._cluster on the writable instance of "+
			"the replicaset by hand", name)

		return "", nil
	}

	uuid := ""

	var connected []string

	for _, peer := range peers {
		if uuid == "" {
			uuid = peer.UUID
		}

		if peer.connected() {
			connected = append(connected, peer.String())
		}
	}

	if len(connected) > 0 {
		if !force {
			return "", fmt.Errorf("instance %q %w: stop it on its host "+
				"(tt instance stop %s:%s) or pass --force (%s)",
				name, errExpelConnected, app, name, strings.Join(connected, "; "))
		}

		log.Warnf("The instance %s %s, expelled by --force: %s", name, errExpelConnected,
			strings.Join(connected, "; "))
	}

	return uuid, nil
}

// expelPlan is what an expel finds out before it changes the config: the
// members that stay in the replicaset and that tt reaches, and the UUID of
// the instance to expel, empty when none of them knows it.
type expelPlan struct {
	survivors []running.InstanceCtx
	uuid      string
}

// planExpel looks at the replicaset of inst, as cfg has it, from its other
// members among candidates, the instances tt can reach, and checks that
// inst can be expelled (checkExpelLeader, checkExpelPeers). A member it
// does not reach is an error with strict, unless force, as it would be left
// with the config it has; without strict it is left to reload the config by
// itself, as an instance of a config storage does.
func planExpel(cfg goconfig.Config, inst cconfigInstance, candidates []running.InstanceCtx,
	force, strict bool,
) (expelPlan, error) {
	err := checkExpelLeader(&cfg, inst)
	if err != nil {
		return expelPlan{}, err
	}

	members := slices.DeleteFunc(
		cluster.ReplicasetInstances(cfg, inst.groupName, inst.replicasetName),
		func(member string) bool { return member == inst.name })

	asked := slices.DeleteFunc(slices.Clone(candidates), func(candidate running.InstanceCtx) bool {
		return !slices.Contains(members, candidate.InstName)
	})

	peers, err := askExpelPeers(asked, inst)
	if err != nil {
		return expelPlan{}, err
	}

	missing := slices.DeleteFunc(slices.Clone(members), func(member string) bool {
		return slices.ContainsFunc(peers, func(peer expelPeer) bool { return peer.Name == member })
	})

	if len(missing) > 0 {
		msg := "could not connect to: " + strings.Join(missing, ",")

		switch {
		case strict && !force:
			return expelPlan{}, fmt.Errorf("%w%s",
				errAllOtherInstancesInTheTargetReplicasetShouldBeOnline, msg)
		case strict:
			log.Warn(msg)
		case len(peers) > 0:
			log.Infof("Not reached, left to reload the config by themselves: %s",
				strings.Join(missing, ", "))
		}
	}

	app := ""
	if len(peers) > 0 {
		app = peers[0].Inst.AppName
	}

	uuid, err := checkExpelPeers(app, inst.name, peers, force)
	if err != nil {
		return expelPlan{}, err
	}

	if uuid == "" && len(peers) > 0 {
		log.Infof("No instance of %s knows %s by its name: there is nothing to delete "+
			"from _cluster", inst.replicasetName, inst.name)
	}

	plan := expelPlan{survivors: nil, uuid: uuid}
	for _, peer := range peers {
		plan.survivors = append(plan.survivors, peer.Inst)
	}

	return plan, nil
}

// askExpelPeers asks the instances that are alive among instances what they
// know of inst (cconfigExpelPeerBody). An instance that runs as another
// instance, or in another replicaset, than the config has it is left out.
func askExpelPeers(instances []running.InstanceCtx, inst cconfigInstance) ([]expelPeer, error) {
	if len(instances) == 0 {
		return nil, nil
	}

	var peers []expelPeer

	err := EvalForeachAlive(instances, InstanceEvalFunc(
		func(ictx running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			opts := connector.RequestOpts{PushCallback: nil, ReadTimeout: 0, ResData: nil}

			data, err := evaler.Eval(cconfigExpelPeerBody, []any{inst.name}, opts)
			if err != nil {
				return true, fmt.Errorf("failed to ask %s of %s: %w", ictx.InstName, inst.name, err)
			}

			if len(data) != 1 {
				return true, fmt.Errorf("%w: %v", errUnexpectedResponse, data)
			}

			var peer expelPeer

			err = mapstructure.Decode(data[0], &peer)
			if err != nil {
				return true, fmt.Errorf("failed to parse a response: %w", err)
			}

			if peer.Name != ictx.InstName || peer.Replicaset != inst.replicasetName {
				log.Warnf("%s runs as %q of the replicaset %q, not as a member of %q: "+
					"left out", ictx.InstName, peer.Name, peer.Replicaset, inst.replicasetName)

				return false, nil
			}

			peer.Inst = ictx
			peers = append(peers, peer)

			return false, nil
		}))
	// None alive is no peer, which checkExpelPeers judges.
	if err != nil && !errors.Is(err, errFailedToConnectToAnyInstance) {
		return nil, err
	}

	return peers, nil
}

// finishExpel completes the expel of plan, the instance name has left the
// config: it reloads the config of the members that stay in the replicaset
// and deletes the instance from _cluster on the writable one, waiting up to
// timeout seconds for one, as an election may yet have to give one.
func finishExpel(plan expelPlan, name string, timeout int) error {
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)

	if len(plan.survivors) == 0 {
		return nil
	}

	err := reloadExpelSurvivors(plan.survivors, deadline)
	if err != nil {
		return err
	}

	if plan.uuid == "" {
		return nil
	}

	for {
		err = deleteFromCluster(plan.survivors, plan.uuid)
		if !errors.Is(err, errNoWritableInstance) || time.Now().After(deadline) {
			break
		}

		time.Sleep(expelRetryInterval)
	}

	if err != nil {
		return fmt.Errorf("failed to delete %q from _cluster: %w", name, err)
	}

	return nil
}

// errReloadInProgress is the text of the error of config:reload() while the
// config reloads already, as it does by itself on a change in a config
// storage with config.reload: auto.
const errReloadInProgress = "already in progress"

// reloadExpelSurvivors reloads the config of instances. A config from a
// storage reloads by itself, with config.reload: auto, the default; the
// explicit reload makes the change applied before the instance is deleted
// from _cluster, and is the only one with config.reload: manual. A reload
// that finds one in progress is tried again until deadline.
func reloadExpelSurvivors(instances []running.InstanceCtx, deadline time.Time) error {
	var failed []string

	err := EvalForeach(instances, InstanceEvalFunc(
		func(ictx running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			opts := connector.RequestOpts{PushCallback: nil, ReadTimeout: 0, ResData: nil}

			for {
				_, err := evaler.Eval("require('config'):reload()", []any{}, opts)
				if err != nil && strings.Contains(err.Error(), errReloadInProgress) &&
					time.Now().Before(deadline) {
					time.Sleep(expelRetryInterval)

					continue
				}

				if err != nil {
					log.Warnf("%s: %s", ictx.InstName, err)

					failed = append(failed, ictx.InstName)
				}

				return false, nil
			}
		}))
	if err != nil {
		return fmt.Errorf("failed to reload instances configuration"+
			", please try to do it manually with `require('config'):reload()`: %w", err)
	}

	if len(failed) > 0 {
		return fmt.Errorf("%w%s, "+
			"please try to do it manually with `require('config'):reload()`",
			errFailedToReloadInstanceConfiguration, strings.Join(failed, ", "))
	}

	return nil
}
