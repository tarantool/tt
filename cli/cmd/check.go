package cmd

import (
	"context"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/running"
)

// NewCheckCmd creates the check command of tt instance.
func NewCheckCmd() *cobra.Command {
	checkCmd := &cobra.Command{
		Use:   "check " + instanceTargetsUsage,
		Short: "Check the cluster config and the application file of instances",
		Long: "Validate the cluster config of the instances against the schema of the\n" +
			"Tarantool config, as tt instance start and tt cluster show --validate\n" +
			"validate it: an unknown option or a value of a wrong type fails the\n" +
			"check. Check the Lua file each instance runs, its script or app.file of its\n" +
			"config, for syntax errors, with the tarantool the instance runs on, and\n" +
			"that the binary of each external process is found. TCM, a record with\n" +
			"kind: tcm, is checked as its start checks it: its binary is found, its\n" +
			"config and password file are read, and the project runs on Tarantool\n" +
			"Enterprise.",
		RunE: internalRunE(internalCheckModule),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			return internal.ValidArgsFunction(&cmdCtx, cmd, toComplete, internal.Any)
		},
	}

	addInstanceFlags(checkCmd)

	return checkCmd
}

// validateConfigOf validates the cluster config of inst, one of instances,
// as tt instance start validates it (running.ValidateConfigs), once for all
// the instances of its config file: validated holds the outcome of each
// file. The first instance of a file reports what is wrong with it, and the
// others fail with it silently. An instance the cluster config does not
// configure has nothing to validate.
func validateConfigOf(instances []running.InstanceCtx, inst *running.InstanceCtx,
	validated map[string]error,
) error {
	if inst.Configuration == nil || inst.ClusterConfigPath == "" {
		return nil
	}

	path := inst.ClusterConfigPath
	if err, done := validated[path]; done {
		return exitcode.Silent(err)
	}

	ofPath := make([]running.InstanceCtx, 0, len(instances))

	for _, other := range instances {
		if other.ClusterConfigPath == path {
			ofPath = append(ofPath, other)
		}
	}

	err := running.ValidateConfigs(ofPath)

	validated[path] = err

	if err == nil {
		log.Infof("Result of check: the cluster config %s is valid", path)
	}

	return err
}

// internalCheckModule is a default check module: it checks the application
// file of every instance the targets select, each file once: the script of
// an instance that runs one, as tt2 did, or app.file of its config. An
// instance whose config names no app.file has nothing of its own to check.
// An external process has its binary found, as its start would find it, in
// .rocks/bin/ of the application or in PATH: tt knows nothing else about it.
//
// The cluster config of a Tarantool instance it configures is validated
// first, against the schema of Tarantool, as tt instance start validates it
// (validateConfigOf): an instance of a config that is invalid fails, the
// problems reported once.
//
// An instance whose file has a syntax error fails, and so does every other
// instance that runs the same file; the error is reported once. So does an
// instance its tarantool cannot run, as a config kept in a config storage
// on Tarantool Community Edition (running.CheckTarantool), which would only
// tell after the start.
func internalCheckModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	instances, err := resolveInstances(cmdCtx, args)
	if err != nil {
		return err
	}

	checked := map[string]error{}
	validated := map[string]error{}

	return actOnInstances("check", instances, func(inst *running.InstanceCtx) error {
		err := validateConfigOf(instances, inst, validated)
		if err != nil {
			return err
		}

		if running.IsTCM(inst) {
			path, warnings, err := running.CheckTCM(inst)
			if err != nil {
				return err
			}

			for _, warning := range warnings {
				log.Warnf("%s", warning)
			}

			log.Infof("%s: TCM, its binary %s is found", running.GetAppInstanceName(*inst), path)

			return nil
		}

		if running.IsExternal(inst) {
			path, err := running.ResolveBinary(inst)
			if err != nil {
				return err
			}

			log.Infof("%s: an external process, its binary %s is found",
				running.GetAppInstanceName(*inst), path)

			return nil
		}

		err = running.CheckTarantool(context.Background(), inst)
		if err != nil {
			return err
		}

		file, found, err := running.AppFile(inst)
		if err != nil {
			return err
		}

		if !found {
			log.Infof("%s: the config names no application file (app.file) to check",
				running.GetAppInstanceName(*inst))

			return nil
		}

		if err, done := checked[file]; done {
			return exitcode.Silent(err)
		}

		err = running.Check(context.Background(), inst, file)
		checked[file] = err

		if err != nil {
			return err
		}

		log.Infof("Result of check: syntax of file '%s' is OK", file)

		return nil
	})
}
