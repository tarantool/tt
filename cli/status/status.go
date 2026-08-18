package status

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
	"golang.org/x/sync/semaphore"
)

// InstanceStatusPrinter interface defines methods to output instance status information.
type InstanceStatusPrinter interface {
	Print(instances map[string]*instanceStatus) error
}

// maxParallelStatusRequests limits the number of concurrent instance status checks.
const maxParallelStatusRequests = 128

// DefaultInstanceTimeout is the default timeout for collecting a single instance's
// status, used when the caller does not override it (e.g. via --instance-timeout).
const DefaultInstanceTimeout = 5 * time.Second

//go:embed lua/instance_state.lua
var instanceInfoLuaScript string

var defaultModuleStatus = "--"

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

type alert struct {
	Type    string `mapstructure:"type"`
	Message string `mapstructure:"message"`
}

type configInfo struct {
	Status string  `mapstructure:"status"`
	Alerts []alert `mapstructure:"alerts"`
}

type upstream struct {
	Status  string `mapstructure:"status"`
	Message string `mapstructure:"message"`
}

type rawReplicationInfo struct {
	UUID     string   `mapstructure:"uuid"`
	Name     *string  `mapstructure:"name"`
	Upstream upstream `mapstructure:"upstream"`
}

type rawInstanceState struct {
	ReplicationInfo []rawReplicationInfo `mapstructure:"replication_info"`
	ConfigInfo      configInfo           `mapstructure:"config_info"`
	ReadOnly        string               `mapstructure:"read_only"`
	BoxStatus       string               `mapstructure:"box_status"`
	UUID            string               `mapstructure:"uuid"`
}

type severity string

const (
	severityError   severity = "error"
	severityWarning severity = "warning"
)

type instanceAlert struct {
	Message  string   `json:"message"`
	Severity severity `json:"severity"`
}

type instanceStatus struct {
	Status             string                     `json:"status"`
	PID                *int                       `json:"pid"`
	Mode               string                     `json:"mode"`
	Config             string                     `json:"config"`
	Box                string                     `json:"box"`
	Upstream           string                     `json:"upstream"`
	Alerts             []instanceAlert            `json:"alerts"`
	rawReplicationInfo []rawReplicationInfo       `json:"-" yaml:"-"`
	procStatus         process_utils.ProcessState `json:"-" yaml:"-"`
}

func (is *instanceStatus) addAlert(message string, severity severity) {
	is.Alerts = append(is.Alerts, instanceAlert{
		Message:  message,
		Severity: severity,
	})
}

func newInstanceStatus() instanceStatus {
	return instanceStatus{
		Config:   defaultModuleStatus,
		Box:      defaultModuleStatus,
		Upstream: defaultModuleStatus,
	}
}

type instanceStatusMap = map[string]*instanceStatus

func processReplicationInfo(instStatus *instanceStatus, uuid2name map[string]string) {
	for _, repl := range instStatus.rawReplicationInfo {
		fullInstanceUpstreamName, ok := uuid2name[repl.UUID]
		// Use repl.Name if available, otherwise fallback to repl.UUID
		if !ok {
			if repl.Name != nil {
				fullInstanceUpstreamName = *repl.Name
			} else {
				fullInstanceUpstreamName = repl.UUID
			}
		}
		if repl.Upstream.Status == "follow" || len(repl.Upstream.Message) == 0 {
			continue
		}
		instStatus.Upstream = repl.Upstream.Status

		var upstreamInstanceDesc string
		if ok || repl.Name != nil {
			upstreamInstanceDesc = fmt.Sprintf("instance with name %q",
				fullInstanceUpstreamName)
		} else {
			upstreamInstanceDesc = fmt.Sprintf("instance with UUID %s",
				fullInstanceUpstreamName)
		}
		instStatus.addAlert(fmt.Sprintf(
			"[upstream][warning]: replication from %s is in %q status: %q",
			upstreamInstanceDesc, repl.Upstream.Status,
			repl.Upstream.Message), severityWarning)
	}
}

func processConfigInfo(instStatus *instanceStatus, instanceState rawInstanceState) {
	if len(instanceState.ConfigInfo.Alerts) == 0 {
		return
	}

	for _, alert := range instanceState.ConfigInfo.Alerts {
		severity := severityWarning
		if alert.Type == "error" {
			severity = severityError
		}
		instStatus.addAlert(fmt.Sprintf("[config][%s]: %s", alert.Type, alert.Message), severity)
	}
}

// collectInstanceState connects to an instance and collects its state.
func collectInstanceState(run running.InstanceCtx, fullInstanceName string,
	instStatus *instanceStatus, instanceTimeout time.Duration,
) (rawInstanceState, error) {
	var instanceState rawInstanceState

	conn, err := connector.Connect(connector.ConnectOpts{
		Network: "unix",
		Address: run.ConsoleSocket,
	})
	if err != nil {
		if instStatus.procStatus.Code == process_utils.ProcessRunningCode {
			instStatus.addAlert(fmt.Sprintf(
				"Error while connecting to instance %s via socket %s: %v",
				fullInstanceName, run.ConsoleSocket, err), severityError)
		}
		return instanceState, fmt.Errorf("failed to connect to instance %s: %w",
			fullInstanceName, err)
	}

	defer conn.Close()

	res, err := conn.Eval(filterComments(instanceInfoLuaScript), []any{},
		connector.RequestOpts{ReadTimeout: instanceTimeout})
	if err != nil {
		instStatus.addAlert(fmt.Sprintf(
			"Error while executing Lua script on instance %s: %v",
			fullInstanceName, err), severityError)
		return instanceState, fmt.Errorf("failed to execute Lua script on instance %s: %w",
			fullInstanceName, err)
	}

	if len(res) == 0 {
		instStatus.addAlert(fmt.Sprintf(
			"No data returned from Lua script on instance %s",
			fullInstanceName), severityError)
		return instanceState, fmt.Errorf("no data returned from Lua script")
	}

	err = mapstructure.Decode(res[0], &instanceState)
	if err != nil {
		instStatus.addAlert(fmt.Sprintf("Error while decoding data from "+
			"instance %s: %v", fullInstanceName, err), severityError)
		return instanceState, fmt.Errorf("failed to decode data from instance %s: %w",
			fullInstanceName, err)
	}

	return instanceState, nil
}

// statusResult holds the result of a status check for an instance.
type statusResult struct {
	name string
	// Since Tarantool 2.x doesn't support instance names, only UUIDs are available.
	// To make the alerts more readable, we map the UUIDs to instance names.
	uuid   string
	status *instanceStatus
}

func collectStatuses(instances []running.InstanceCtx,
	instanceTimeout time.Duration,
) <-chan statusResult {
	statuses := make(chan statusResult, len(instances))
	sem := semaphore.NewWeighted(maxParallelStatusRequests)
	ctx := context.Background()

	var wg sync.WaitGroup
	for _, instance := range instances {
		if err := sem.Acquire(ctx, 1); err != nil {
			break
		}

		wg.Go(func() {
			defer sem.Release(1)
			statuses <- processStatusForInstance(instance, instanceTimeout)
		})
	}

	go func() {
		wg.Wait()
		close(statuses)
	}()

	return statuses
}

func applyInstanceState(instStatus *instanceStatus, instanceState rawInstanceState) {
	processConfigInfo(instStatus, instanceState)
	instStatus.Mode = instanceState.ReadOnly
	instStatus.Config = instanceState.ConfigInfo.Status
	instStatus.Box = instanceState.BoxStatus
	instStatus.rawReplicationInfo = instanceState.ReplicationInfo
}

func processStatusForInstance(instance running.InstanceCtx,
	instanceTimeout time.Duration,
) statusResult {
	instStatus := newInstanceStatus()
	instStatus.procStatus = running.Status(&instance)
	instStatus.Status = instStatus.procStatus.Status
	if instStatus.procStatus.Code == process_utils.ProcessRunningCode {
		instStatus.PID = &instStatus.procStatus.PID
	}

	fullInstanceName := running.GetAppInstanceName(instance)

	instanceState, err := collectInstanceState(instance, fullInstanceName, &instStatus,
		instanceTimeout)
	if err != nil {
		return statusResult{name: fullInstanceName, status: &instStatus}
	}

	applyInstanceState(&instStatus, instanceState)

	return statusResult{name: fullInstanceName, uuid: instanceState.UUID, status: &instStatus}
}

// Status writes the status as a table. instanceTimeout bounds how long collecting
// a single instance's status may take (e.g. an instance stuck processing requests);
// zero means no timeout.
func Status(runningCtx running.RunningCtx, printer InstanceStatusPrinter,
	instanceTimeout time.Duration,
) error {
	instances := make(instanceStatusMap)
	uuid2name := map[string]string{}
	statuses := collectStatuses(runningCtx.Instances, instanceTimeout)

	for instStatus := range statuses {
		if instStatus.uuid != "" {
			uuid2name[instStatus.uuid] = instStatus.name
		}
		instances[instStatus.name] = instStatus.status
	}

	for _, instStatus := range instances {
		processReplicationInfo(instStatus, uuid2name)
	}

	return printer.Print(instances)
}
