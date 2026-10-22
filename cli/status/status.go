package status

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running"
)

var (
	errNoDataReturnedFromLuaScript = errors.New("no data returned from Lua script")
)

// InstanceStatusPrinter interface defines methods to output instance status information.
type InstanceStatusPrinter interface {
	Print(instances map[string]*instanceStatus) error
}

// maxParallelStatusRequests limits concurrent instance status checks.
const maxParallelStatusRequests = 128

// DefaultInstanceTimeout bounds collection of one instance's status by default.
const DefaultInstanceTimeout = 5 * time.Second

//go:embed lua/instance_state.lua
var instanceInfoLuaScript string

var defaultModuleStatus = "--"

func filterComments(script string) string {
	var filteredLines []string

	for line := range strings.SplitSeq(script, "\n") {
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
	Message  string   `json:"message"  yaml:"message"`
	Severity severity `json:"severity" yaml:"severity"`
}

type instanceStatus struct {
	Status             string                     `json:"status"   yaml:"status"`
	PID                *int                       `json:"pid"      yaml:"pid"`
	Mode               string                     `json:"mode"     yaml:"mode"`
	Config             string                     `json:"config"   yaml:"config"`
	Box                string                     `json:"box"      yaml:"box"`
	Upstream           string                     `json:"upstream" yaml:"upstream"`
	Alerts             []instanceAlert            `json:"alerts"   yaml:"alerts"`
	rawReplicationInfo []rawReplicationInfo       `json:"-"        yaml:"-"`
	procStatus         process_utils.ProcessState `json:"-"        yaml:"-"`
}

func (is *instanceStatus) addAlert(message string, severity severity) {
	is.Alerts = append(is.Alerts, instanceAlert{
		Message:  message,
		Severity: severity,
	})
}

func newInstanceStatus() instanceStatus {
	return instanceStatus{
		Status:             "",
		PID:                nil,
		Mode:               "",
		Config:             defaultModuleStatus,
		Box:                defaultModuleStatus,
		Upstream:           defaultModuleStatus,
		Alerts:             nil,
		rawReplicationInfo: nil,
		procStatus:         process_utils.ProcessState{Code: 0, Status: "", PID: 0},
	}
}

type instanceStatusMap = map[string]*instanceStatus

func processReplicationInfo(instStatus *instanceStatus, uuid2name map[string]string) {
	for _, repl := range instStatus.rawReplicationInfo {
		fullInstanceUpstreamName, ok := uuid2name[repl.UUID]
		// Use repl.Name if available, otherwise fallback to repl.UUID.
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
			upstreamInstanceDesc = "instance with name " + strconv.Quote(fullInstanceUpstreamName)
		} else {
			upstreamInstanceDesc = "instance with UUID " + fullInstanceUpstreamName
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

	ctx := context.Background()

	if instanceTimeout > 0 {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, instanceTimeout)

		defer cancel()
	}

	conn, err := connector.ConnectContext(ctx, connector.ConnectOpts{
		Network:  "unix",
		Address:  run.ConsoleSocket,
		Username: "",
		Password: "",
		Ssl: connector.SslOpts{
			KeyFile: "", CertFile: "", CaFile: "", Ciphers: "", Password: "", PasswordFile: "",
		},
		MaxOutputHistoryLen: 0,
		ConnectTimeout:      0,
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

	defer func() { _ = conn.Close() }()

	// Closing interrupts every I/O operation, including writes and repeated pushes.
	cancelled := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.Close()

		close(cancelled)
	})

	defer func() {
		if !stop() {
			<-cancelled
		}
	}()

	res, err := conn.Eval(filterComments(instanceInfoLuaScript), []any{},
		connector.RequestOpts{PushCallback: nil, ReadTimeout: 0, ResData: nil})
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}

		instStatus.addAlert(fmt.Sprintf(
			"Error while executing Lua script on instance %s: %v",
			fullInstanceName, err), severityError)

		return instanceState, fmt.Errorf("failed to execute Lua script on instance %s: %w",
			fullInstanceName, err)
	}

	if len(res) == 0 {
		instStatus.addAlert("No data returned from Lua script on instance "+fullInstanceName,
			severityError)

		return instanceState, errNoDataReturnedFromLuaScript
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

// statusResult holds a collected status and its UUID for replication name lookup.
type statusResult struct {
	name   string
	uuid   string
	status *instanceStatus
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

	name := running.GetAppInstanceName(instance)
	result := statusResult{name: name, uuid: "", status: &instStatus}

	instanceState, err := collectInstanceState(instance, name, &instStatus, instanceTimeout)
	if err != nil {
		return result
	}

	result.uuid = instanceState.UUID
	processConfigInfo(&instStatus, instanceState)

	instStatus.Mode = instanceState.ReadOnly
	instStatus.Config = instanceState.ConfigInfo.Status
	instStatus.Box = instanceState.BoxStatus
	instStatus.rawReplicationInfo = instanceState.ReplicationInfo

	return result
}

func collectStatuses(instances []running.InstanceCtx,
	instanceTimeout time.Duration,
) <-chan statusResult {
	statuses := make(chan statusResult, len(instances))
	jobs := make(chan running.InstanceCtx)

	var workers sync.WaitGroup

	for range min(len(instances), maxParallelStatusRequests) {
		workers.Go(func() {
			for instance := range jobs {
				statuses <- processStatusForInstance(instance, instanceTimeout)
			}
		})
	}

	go func() {
		for _, instance := range instances {
			jobs <- instance
		}

		close(jobs)
		workers.Wait()
		close(statuses)
	}()

	return statuses
}

// Status writes the instance statuses. instanceTimeout bounds collection of a
// single instance's state, including connection setup; zero disables the timeout.
func Status(runningCtx running.RunningCtx, printer InstanceStatusPrinter,
	instanceTimeout time.Duration,
) error {
	instances := make(instanceStatusMap)
	uuid2name := map[string]string{}

	for result := range collectStatuses(runningCtx.Instances, instanceTimeout) {
		if result.uuid != "" {
			uuid2name[result.uuid] = result.name
		}

		instances[result.name] = result.status
	}

	for _, instStatus := range instances {
		processReplicationInfo(instStatus, uuid2name)
	}

	return printer.Print(instances)
}
