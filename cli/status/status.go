package status

import (
	_ "embed"
	"fmt"
	"os"
	"strings"

	"github.com/fatih/color"
	"github.com/jedib0t/go-pretty/v6/table"
	"github.com/jedib0t/go-pretty/v6/text"
	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/running"
)

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

// StatusOpts contains options for tt status.
type StatusOpts struct {
	// Option for pretty-formatted table output.
	Pretty bool
	// Option for detailed alerts output for each instance, such as warnings and errors.
	Details bool
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

type replicationInfo struct {
	UUID     string   `mapstructure:"uuid"`
	Name     *string  `mapstructure:"name"`
	Upstream upstream `mapstructure:"upstream"`
}

type instanceState struct {
	ReplicationInfo []replicationInfo `mapstructure:"replication_info"`
	ConfigInfo      configInfo        `mapstructure:"config_info"`
	ReadOnly        string            `mapstructure:"read_only"`
	BoxStatus       string            `mapstructure:"box_status"`
	UUID            string            `mapstructure:"uuid"`
}

func newInstanceStatusMap() map[string]interface{} {
	return map[string]interface{}{
		"STATUS":   "",
		"PID":      nil,
		"MODE":     "",
		"CONFIG":   defaultModuleStatus,
		"BOX":      defaultModuleStatus,
		"UPSTREAM": defaultModuleStatus,
	}
}

var instances = map[string]map[string]interface{}{}
var instancesAlerts = map[string][]string{}
var uuid2name = map[string]string{}

var printYellow = color.New(color.FgYellow).SprintFunc()
var printRed = color.New(color.FgRed).SprintFunc()

func processReplicationInfo(fullInstanceName string, instanceState instanceState) {
	for _, repl := range instanceState.ReplicationInfo {
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
		instances[fullInstanceName]["UPSTREAM"] = repl.Upstream.Status

		var upstreamInstanceDesc string
		if ok || repl.Name != nil {
			upstreamInstanceDesc = fmt.Sprintf("instance with name %q",
				fullInstanceUpstreamName)
		} else {
			upstreamInstanceDesc = fmt.Sprintf("instance with UUID %s",
				fullInstanceUpstreamName)
		}
		instancesAlerts[fullInstanceName] = append(instancesAlerts[fullInstanceName],
			printYellow(fmt.Sprintf(
				"[upstream][warning]: replication from %s is in %q status: %q",
				upstreamInstanceDesc, repl.Upstream.Status,
				repl.Upstream.Message)))
	}
}

func processConfigInfo(fullInstanceName string, instanceState instanceState) {
	if len(instanceState.ConfigInfo.Alerts) == 0 {
		return
	}
	for _, alert := range instanceState.ConfigInfo.Alerts {
		msg := ""
		if alert.Type == "error" {
			msg = printRed(fmt.Sprintf("[config][error]: %s", alert.Message))
		} else {
			msg = printYellow(fmt.Sprintf("[config][warning]: %s", alert.Message))
		}
		instancesAlerts[fullInstanceName] = append(instancesAlerts[fullInstanceName], msg)
	}
}

// Status writes the status as a table.
func Status(runningCtx running.RunningCtx, opts StatusOpts) error {
	ts := table.NewWriter()
	ts.SetOutputMirror(os.Stdout)
	ts.AppendHeader(
		table.Row{"INSTANCE", "STATUS", "PID", "MODE", "CONFIG", "BOX", "UPSTREAM"})

	instanceRawState := map[string]instanceState{}

	for _, run := range runningCtx.Instances {
		fullInstanceName := running.GetAppInstanceName(run)
		procStatus := running.Status(&run)
		instances[fullInstanceName] = newInstanceStatusMap()
		instances[fullInstanceName]["STATUS"] = procStatus.ColorSprint(procStatus.Status)

		if procStatus.Code == process_utils.ProcessRunningCode {
			instances[fullInstanceName]["PID"] = procStatus.PID
		}

		conn, err := connector.Connect(connector.ConnectOpts{
			Network: "unix",
			Address: run.ConsoleSocket,
		})

		if err != nil && procStatus.Code == process_utils.ProcessRunningCode {
			instancesAlerts[fullInstanceName] = append(
				instancesAlerts[fullInstanceName],
				printRed(fmt.Sprintf(
					"Error while connecting to instance %s via socket %s: %v",
					fullInstanceName, run.ConsoleSocket, err)))
			continue
		} else if err != nil {
			continue
		}

		var instanceState instanceState
		res, err := conn.Eval(filterComments(instanceInfoLuaScript), []any{},
			connector.RequestOpts{})

		if err != nil {
			instancesAlerts[fullInstanceName] = append(
				instancesAlerts[fullInstanceName],
				printRed(fmt.Sprintf(
					"Error while executing Lua script on instance %s: %v",
					fullInstanceName, err)))
			continue
		}

		if len(res) == 0 {
			instancesAlerts[fullInstanceName] = append(
				instancesAlerts[fullInstanceName],
				printRed(fmt.Sprintf(
					"No data returned from Lua script on instance %s",
					fullInstanceName)))
			continue
		}

		err = mapstructure.Decode(res[0], &instanceState)
		if err != nil {
			instancesAlerts[fullInstanceName] = append(
				instancesAlerts[fullInstanceName],
				printRed(fmt.Sprintf("Error while decoding data from "+
					"instance %s: %v", fullInstanceName, err)))
			continue
		}

		// Since Tarantool 2.x doesn't support instance names, only UUIDs are available.
		// To make the alerts more readable, we map the UUIDs to instance names.
		uuid2name[instanceState.UUID] = fullInstanceName

		instanceRawState[fullInstanceName] = instanceState
		instances[fullInstanceName]["MODE"] = instanceState.ReadOnly
		instances[fullInstanceName]["CONFIG"] = instanceState.ConfigInfo.Status
		instances[fullInstanceName]["BOX"] = instanceState.BoxStatus
	}

	// Alert handling placed later because we need to know the mapping of instance UUIDs
	// to their names for a more user-friendly output.
	for fullInstanceName, instanceState := range instanceRawState {
		processReplicationInfo(fullInstanceName, instanceState)
		processConfigInfo(fullInstanceName, instanceState)
	}

	for instName, instData := range instances {
		row := []interface{}{}
		row = append(row, instName)
		row = append(row, instData["STATUS"])
		if instData["PID"] == nil {
			ts.AppendRow(row)
			continue
		}
		row = append(row, instData["PID"])
		row = append(row, instData["MODE"])
		row = append(row, instData["CONFIG"])
		row = append(row, instData["BOX"])
		row = append(row, instData["UPSTREAM"])
		ts.AppendRow(row)
	}
	ts.SortBy([]table.SortBy{{Name: "INSTANCE", Mode: table.Asc}})

	if opts.Details {
		for instance, alerts := range instancesAlerts {
			fmt.Printf("Alerts for %s:\n", instance)
			for _, alert := range alerts {
				fmt.Printf("  • %s\n", alert)
			}
			fmt.Println()
		}
	}
	if opts.Pretty {
		ts.SetStyle(table.StyleRounded)
	} else {
		ts.Style().Options.DrawBorder = false
		ts.Style().Options.SeparateColumns = false
		ts.Style().Options.SeparateHeader = false
	}
	ts.SetColumnConfigs([]table.ColumnConfig{
		{Number: 1, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 2, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 3, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
		{Number: 4, Align: text.AlignLeft, AlignHeader: text.AlignLeft},
	})
	ts.Render()

	if !opts.Details {
		msg := "\nThe status of some instances requires attention.\n" +
			"Please rerun the command with the --details flag to see " +
			"more information"
		for _, alerts := range instancesAlerts {
			if len(alerts) > 0 {
				fmt.Println(msg)
				break
			}
		}
	}

	return nil
}
