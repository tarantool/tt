package replicaset

import (
	"errors"
	"fmt"

	"github.com/tarantool/tt/sdk/dial"
	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/running"
)

var (
	errFailedToConnectToAnyInstance = errors.New("failed to connect to any instance")
	errNoInstancesToConnect         = errors.New("no instances to connect")
	errUnexpectedResponse           = errors.New("unexpected response")
)

// InstanceEvaler is the interface that wraps Eval method for an instance.
type InstanceEvaler interface {
	// Eval could return true or an error to stop execution.
	Eval(instance running.InstanceCtx, evaler connector.Evaler) (bool, error)
}

// InstanceEvalFunc helps to use a function as the InstanceEvaler interface.
type InstanceEvalFunc func(instance running.InstanceCtx,
	evaler connector.Evaler) (bool, error)

// Eval helps to satisfy the InstanceEvaler interface.
func (fun InstanceEvalFunc) Eval(instance running.InstanceCtx,
	evaler connector.Evaler,
) (bool, error) {
	return fun(instance, evaler)
}

// EvalForeach calls evaler for each instance.
func EvalForeach(instances []running.InstanceCtx, iEvaler InstanceEvaler) error {
	return evalForeach(instances, iEvaler, false)
}

// EvalForeachAlive calls evaler for each connectable instance.
func EvalForeachAlive(instances []running.InstanceCtx, iEvaler InstanceEvaler) error {
	return evalForeach(instances, iEvaler, true)
}

// EvalAny calls evaler once for one connectable instance.
func EvalAny(instances []running.InstanceCtx, iEvaler InstanceEvaler) error {
	return EvalForeachAlive(instances, InstanceEvalFunc(
		func(instance running.InstanceCtx, evaler connector.Evaler) (bool, error) {
			_, err := iEvaler.Eval(instance, evaler)
			// Always return true to stop execution on the first instance.
			return true, err
		}))
}

// evalForeach is an internal implementation of iteration over instances with
// an evaler object.
func evalForeach(instances []running.InstanceCtx,
	iEvaler InstanceEvaler, skipConnectError bool,
) error {
	if len(instances) == 0 {
		return errNoInstancesToConnect
	}

	connected := 0

	for _, instance := range instances {
		conn, err := connector.Connect(connector.ConnectOpts{
			Network:  "unix",
			Address:  instance.ConsoleSocket,
			Username: "",
			Password: "",
			Ssl: connector.SslOpts{
				KeyFile: "", CertFile: "", CaFile: "", Ciphers: "", Password: "", PasswordFile: "",
				Transport: dial.TransportDefault,
			},
			ConnectTimeout: 0,
		})
		if err != nil {
			if !skipConnectError {
				return fmt.Errorf("failed to connect to '%s:%s': %w",
					instance.AppName, instance.InstName, err)
			} else {
				log.Debugf("failed to connect to '%s:%s': %s",
					instance.AppName, instance.InstName, err)

				continue
			}
		}

		connected++

		done, err := iEvaler.Eval(instance, conn)

		_ = conn.Close()

		if err != nil {
			return err
		}

		if done {
			break
		}
	}

	if connected == 0 {
		return errFailedToConnectToAnyInstance
	}

	return nil
}
