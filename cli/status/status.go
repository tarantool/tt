package status

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
)

// InstanceStatusPrinter interface defines methods to output instance status information.
type InstanceStatusPrinter interface {
	Print(instances map[string]*instanceStatus) error
}

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
	instStatus *instanceStatus,
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

	res, err := conn.Eval(filterComments(instanceInfoLuaScript), []any{},
		connector.RequestOpts{})
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

// Status writes the status as a table.
func Status(runningCtx running.RunningCtx, printer InstanceStatusPrinter) error {
	instances := make(instanceStatusMap)
	uuid2name := map[string]string{}
	for _, run := range runningCtx.Instances {
		fullInstanceName := running.GetAppInstanceName(run)
		instStatus := newInstanceStatus()
		instStatus.procStatus = running.Status(&run)
		instStatus.Status = instStatus.procStatus.Status
		instances[fullInstanceName] = &instStatus

		if instStatus.procStatus.Code == process_utils.ProcessRunningCode {
			instStatus.PID = &instStatus.procStatus.PID
		}

		instanceState, err := collectInstanceState(run, fullInstanceName, &instStatus)
		if err != nil {
			continue
		}

		// Since Tarantool 2.x doesn't support instance names, only UUIDs are available.
		// To make the alerts more readable, we map the UUIDs to instance names.
		uuid2name[instanceState.UUID] = fullInstanceName

		processConfigInfo(&instStatus, instanceState)
		instStatus.Mode = instanceState.ReadOnly
		instStatus.Config = instanceState.ConfigInfo.Status
		instStatus.Box = instanceState.BoxStatus
	}

	for _, instStatus := range instances {
		processReplicationInfo(instStatus, uuid2name)
	}

	printer.Print(instances)

	return nil
}
