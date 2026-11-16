package running

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/go-config/v2/validator"
	"github.com/tarantool/tt/v3/cli/cluster"
)

// ErrInvalidConfig is wrapped by the error for a cluster config the schema
// of Tarantool refuses.
var ErrInvalidConfig = errors.New("is invalid")

// falseSchemaMessage is what the JSON Schema validator says of a key the
// schema does not know: its schema there is false.
const falseSchemaMessage = "schema is set to 'false'"

// ValidateConfigs validates the cluster config of the Tarantool instances of
// insts that the cluster config configures against the JSON Schema of the
// Tarantool config, as tt cluster show --validate does: the whole cluster
// config of each application once, where a key on a level that does not
// take it is refused too, and, when that passes, the config of every such
// instance, folded from all levels with the environment. Tarantool refuses
// such a config when the instance starts.
//
// The error names each application whose config is refused, its config
// file, and every problem on a line of its own: a key the schema does not
// know as an unknown option, by its dotted path, and any other by its path
// and what the schema says of the value.
func ValidateConfigs(insts []InstanceCtx) error {
	var errs []error

	checked := map[string]bool{}

	for i := range insts {
		inst := &insts[i]
		if inst.clusterConfig == nil || checked[inst.ClusterConfigPath] {
			continue
		}

		checked[inst.ClusterConfigPath] = true

		problems := configProblems(cluster.Validate(inst.clusterConfig.Snapshot()))
		if len(problems) == 0 {
			problems = instanceProblems(insts, inst.ClusterConfigPath)
		}

		if len(problems) != 0 {
			errs = append(errs, fmt.Errorf("application %q: the cluster config %s %w:\n  %s",
				inst.AppName, inst.ClusterConfigPath, ErrInvalidConfig,
				strings.Join(problems, "\n  ")))
		}
	}

	return errors.Join(errs...)
}

// instanceProblems returns the problems of the configs of the instances of
// insts of the cluster config path, each folded from all levels, each
// problem once, named by the instance it was found for first.
func instanceProblems(insts []InstanceCtx, path string) []string {
	var problems []string

	seen := map[string]bool{}

	for i := range insts {
		inst := &insts[i]
		if inst.ClusterConfigPath != path || inst.Configuration == nil {
			continue
		}

		for _, problem := range configProblems(cluster.ValidateInstance(*inst.Configuration)) {
			if seen[problem] {
				continue
			}

			seen[problem] = true

			problems = append(problems, fmt.Sprintf("instance %s: %s", inst.InstName, problem))
		}
	}

	return problems
}

// configProblems returns what err, an error of cluster.Validate, says, a
// problem a line.
func configProblems(err error) []string {
	if err == nil {
		return nil
	}

	errs := []error{err}

	var joined interface{ Unwrap() []error }

	if errors.As(err, &joined) {
		errs = joined.Unwrap()
	}

	problems := make([]string, 0, len(errs))

	for _, err := range errs {
		var invalid *validator.ValidationError

		if !errors.As(err, &invalid) {
			problems = append(problems, err.Error())

			continue
		}

		problems = append(problems, describeProblem(invalid))
	}

	// The validator finds them in no fixed order.
	slices.Sort(problems)

	return problems
}

// describeProblem tells what is wrong with the config as invalid says: a key
// the schema does not know as an unknown option, by its dotted path, and
// anything else by its path and the message of the schema.
func describeProblem(invalid *validator.ValidationError) string {
	path := invalid.Path.MakeString(".")

	switch {
	case strings.Contains(invalid.Message, falseSchemaMessage):
		return "unknown option " + path
	case path == "":
		return invalid.Message
	default:
		return path + ": " + invalid.Message
	}
}
