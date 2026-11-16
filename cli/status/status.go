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
	"github.com/tarantool/tt/sdk/dial"
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

// instanceStatus is the status of an instance. Unit is the systemd unit that
// manages the instance, left out when none does.
type instanceStatus struct {
	Status             string                     `json:"status"         yaml:"status"`
	PID                *int                       `json:"pid"            yaml:"pid"`
	Unit               string                     `json:"unit,omitempty" yaml:"unit,omitempty"`
	Mode               string                     `json:"mode"           yaml:"mode"`
	Config             string                     `json:"config"         yaml:"config"`
	Box                string                     `json:"box"            yaml:"box"`
	Upstream           string                     `json:"upstream"       yaml:"upstream"`
	Alerts             []instanceAlert            `json:"alerts"         yaml:"alerts"`
	rawReplicationInfo []rawReplicationInfo       `json:"-"              yaml:"-"`
	procStatus         process_utils.ProcessState `json:"-"              yaml:"-"`
	// Version is the version of the application of the user or system
	// scope a running instance runs, left out when it is not known
	// (setVersion), and RestartPending tells that another version is
	// active: the instance runs that one once it is restarted.
	Version        string `json:"version,omitempty"         yaml:"version,omitempty"`
	RestartPending bool   `json:"restart_pending,omitempty" yaml:"restart_pending,omitempty"`
	// tarantool names the tarantool a Tarantool instance starts on and where
	// it comes from, or why none can be chosen, and tarantoolWarnings are the
	// warnings of the choice. They are for a printer that shows the details
	// (detailsPrinter), and empty for an external process.
	tarantool         string   `json:"-" yaml:"-"`
	tarantoolWarnings []string `json:"-" yaml:"-"`
	// activeVersion is the version active now, for the details to tell
	// what a restart pending moves the instance to.
	activeVersion string `json:"-" yaml:"-"`
	// TCM is how TCM is reached and what it works with, for TCM alone
	// (running.DescribeTCM): where its password is, never the password.
	TCM *running.TCMAccess `json:"tcm,omitempty" yaml:"tcm,omitempty"`
	// LastExit is how the last process of an instance that does not run
	// ended, as its watchdog recorded it, nil for none (running.ReadLastExit).
	LastExit *running.LastExit `json:"last_exit,omitempty" yaml:"last_exit,omitempty"`
	// log is the tt.log of an instance that does not run, whose last lines
	// the details quote.
	log string `json:"-" yaml:"-"`
	// NotInConfig tells that the instance runs while the config no longer
	// has it (running.InstanceCtx.NotInConfig), left out when it has.
	NotInConfig bool `json:"not_in_config,omitempty" yaml:"not_in_config,omitempty"`
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
		Unit:               "",
		Version:            "",
		RestartPending:     false,
		Mode:               "",
		Config:             defaultModuleStatus,
		Box:                defaultModuleStatus,
		Upstream:           defaultModuleStatus,
		Alerts:             nil,
		rawReplicationInfo: nil,
		procStatus:         process_utils.ProcessState{Code: 0, Status: "", PID: 0},
		tarantool:          "",
		tarantoolWarnings:  nil,
		activeVersion:      "",
		TCM:                nil,
		LastExit:           nil,
		log:                "",
		NotInConfig:        false,
	}
}

// setLastExit sets how the last process of the instance run, which does not
// run, ended, and its log, in instStatus.
func setLastExit(instStatus *instanceStatus, run *running.InstanceCtx) {
	last, ok := running.ReadLastExit(run)
	if ok {
		instStatus.LastExit = &last
	}

	instStatus.log = run.Log
}

// setTCM sets how TCM run is reached in instStatus, or an alert that says
// why it cannot be told.
func setTCM(instStatus *instanceStatus, run *running.InstanceCtx) {
	access, err := running.DescribeTCM(run)
	if err != nil {
		instStatus.addAlert(err.Error(), severityError)

		return
	}

	instStatus.TCM = &access
}

// setVersion sets the version of the application the running instance run
// runs, and whether a restart is pending, in instStatus, for an application of
// the user or system scope: the version the watchdog recorded the instance
// was started from, against the version active now (running.RunningVersion).
func setVersion(instStatus *instanceStatus, run *running.InstanceCtx) {
	runs, active, ok := running.RunningVersion(run)
	if !ok {
		return
	}

	instStatus.Version = runs
	instStatus.activeVersion = active
	instStatus.RestartPending = active != "" && runs != active
}

// detailsPrinter is a printer that shows the details of the instances, the
// table with --details: Status collects for it what only the details show,
// the tarantool of every Tarantool instance.
type detailsPrinter interface {
	withDetails() bool
}

// showsDetails reports whether printer shows the details of the instances.
func showsDetails(printer InstanceStatusPrinter) bool {
	details, ok := printer.(detailsPrinter)

	return ok && details.withDetails()
}

// setTarantool sets the tarantool the Tarantool instance run starts on, and
// where it comes from, in instStatus (running.DescribeTarantool). For an
// instance that runs, it is the tarantool its watchdog runs it on, as
// tt instance start resolved it (running.RunningLaunch): the watchdog starts
// the instance on it again after a crash, whatever the next start would
// choose.
func setTarantool(instStatus *instanceStatus, run *running.InstanceCtx) {
	launch := running.RunningLaunch(run)
	if launch != nil && launch.Tarantool != "" {
		instStatus.tarantool = launch.Tarantool + "; the watchdog runs it as it was started"
		instStatus.tarantoolWarnings = launch.TarantoolWarnings

		return
	}

	described, warnings, err := running.DescribeTarantool(run)
	if err != nil {
		instStatus.tarantool = "none can be chosen: " + err.Error()

		return
	}

	instStatus.tarantool = described
	instStatus.tarantoolWarnings = warnings
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

// configStatus is the CONFIG of the Tarantool instance inst whose config
// module tells status. An instance that runs its script configures itself
// with box.cfg and never uses the config module, which says "uninitialized"
// then: that is no state of the instance, and it shows "--", what an external
// process shows, as for a value that does not apply.
func configStatus(inst *running.InstanceCtx, status string) string {
	if running.IsScript(inst) && (status == "" || status == "uninitialized") {
		return defaultModuleStatus
	}

	return status
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
		Auth:     dial.AuthAuto,
		Ssl: connector.SslOpts{
			KeyFile: "", CertFile: "", CaFile: "", Ciphers: "", Password: "", PasswordFile: "",
			Transport: dial.TransportDefault,
		},
		ConnectTimeout: 0,
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
	instStatus.Unit = instance.Unit

	if instStatus.procStatus.Code == process_utils.ProcessRunningCode {
		instStatus.PID = &instStatus.procStatus.PID

		setVersion(&instStatus, &instance)
	} else {
		setLastExit(&instStatus, &instance)
	}

	name := running.GetAppInstanceName(instance)
	result := statusResult{name: name, uuid: "", status: &instStatus}

	if instance.NotInConfig {
		instStatus.NotInConfig = true
		instStatus.addAlert(notInConfigAlert(name), severityWarning)
	}

	// External processes have no Tarantool console to query.
	if running.IsExternal(&instance) {
		orphan := running.Orphan(&instance)
		if orphan != 0 {
			instStatus.addAlert(orphanAlert(&instance, name, orphan), severityError)
		}

		if running.IsTCM(&instance) {
			setTCM(&instStatus, &instance)
		}

		return result
	}

	instanceState, err := collectInstanceState(instance, name, &instStatus, instanceTimeout)
	if err != nil {
		return result
	}

	result.uuid = instanceState.UUID
	processConfigInfo(&instStatus, instanceState)

	instStatus.Mode = instanceState.ReadOnly
	instStatus.Config = configStatus(&instance, instanceState.ConfigInfo.Status)
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
// single instance's state, including connection setup; zero disables the
// timeout. An external process gets the state of its process alone: it has no
// console to ask about the rest.
//
// The state is the state of the watchdog, as for a Tarantool instance: an
// external process whose watchdog is gone while its process runs on
// (running.Orphan) is not running, since nothing supervises it, and gets an
// alert that names the process and how to stop it.
//
// An instance that a systemd unit manages is read from the same pid file as
// any, that of the watchdog its unit runs, and shows its unit.
//
// A printer that shows the details gets the tarantool every Tarantool
// instance starts on as well, chosen without running it.
func Status(runningCtx running.RunningCtx, printer InstanceStatusPrinter,
	instanceTimeout time.Duration,
) error {
	instances := make(instanceStatusMap)
	uuid2name := map[string]string{}
	details := showsDetails(printer)

	byName := make(map[string]running.InstanceCtx, len(runningCtx.Instances))
	for _, run := range runningCtx.Instances {
		byName[running.GetAppInstanceName(run)] = run
	}

	for result := range collectStatuses(runningCtx.Instances, instanceTimeout) {
		if result.uuid != "" {
			uuid2name[result.uuid] = result.name
		}

		instances[result.name] = result.status
		if details {
			run := byName[result.name]
			if !running.IsExternal(&run) {
				setTarantool(result.status, &run)
			}
		}
	}

	for _, instStatus := range instances {
		processReplicationInfo(instStatus, uuid2name)
	}

	return printer.Print(instances)
}

// notInConfigAlert is the alert for the instance name that runs while the
// config no longer has it: it names how to stop it.
func notInConfigAlert(name string) string {
	return fmt.Sprintf("%s is not in the config: it runs on from before it was removed "+
		"from the topology; stop it with tt instance stop %s", name, name)
}

// orphanAlert is the alert for the process orphan of the external process
// run, named name, that runs without its watchdog: it names how to stop it,
// through the unit that manages the instance, if one does.
func orphanAlert(run *running.InstanceCtx, name string, orphan int) string {
	stop := fmt.Sprintf("tt instance stop %s or tt instance kill %s", name, name)
	if run.Unit != "" {
		stop = "systemctl stop " + run.Unit
	}

	return fmt.Sprintf("the process of %s (PID = %d) runs without its watchdog: stop it with %s",
		name, orphan, stop)
}
