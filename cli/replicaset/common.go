package replicaset

import (
	_ "embed"
	"fmt"

	"github.com/tarantool/tt/cli/connector"
)

var (
	//go:embed lua/wait_rw.lua
	waitRWBody string

	//go:embed lua/wait_ro.lua
	waitROBody string
)

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

// waitRO waits until the instance becomes ro.
func waitRO(eval connector.Evaler, timeout int) error {
	var opts connector.RequestOpts
	args := []any{timeout}
	_, err := eval.Eval(waitROBody, args, opts)
	if err != nil {
		return fmt.Errorf("failed to wait ro: %w", err)
	}
	return nil
}
