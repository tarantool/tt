package replicaset

import (
	_ "embed"
	"fmt"

	"github.com/tarantool/tt/cli/connector"
)

//go:embed lua/wait_rw.lua
var waitRWBody string

// waitRW waits until the instance becomes rw.
func waitRW(eval connector.Evaler, timeout int) error {
	var opts connector.RequestOpts
	args := []any{timeout}
	_, err := eval.Eval(waitRWBody, args, opts)
	if err != nil {
		return fmt.Errorf("failed to wait rw: %w", err)
	}
	return nil
}
