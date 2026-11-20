package status

import (
	"context"
	"errors"
	"fmt"

	"github.com/mitchellh/mapstructure"
	"github.com/tarantool/tt/sdk/dial/v3"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/running"
)

// errNoProbeAnswer is the error for an instance that answers nothing.
var errNoProbeAnswer = errors.New("no answer")

// probeLua asks a starting instance how far it is: box.info.status, and
// config:info().status of the config module of Tarantool, if it has one.
//
//nolint:dupword // Lua closes its blocks with end.
const probeLua = `local got_box, box_status = pcall(function() return box.info.status end)
local config_status = ''
local has, config = pcall(require, 'config')
if has and type(config) == 'table' and type(config.info) == 'function' then
    local got_info, info = pcall(config.info, config)
    if got_info and type(info) == 'table' then
        config_status = info.status or ''
    end
end
return {box = got_box and box_status or '', config = config_status}`

// startState is what a starting Tarantool instance tells of itself: the BOX
// and CONFIG of tt instance status.
type startState struct {
	Box    string `mapstructure:"box"`
	Config string `mapstructure:"config"`
}

// Probe tells whether the Tarantool instance inst, whose process runs, is up,
// for the wait of tt instance start (running.Prober), and otherwise how far
// it is, as tt instance status shows it. It is up once its box runs,
// box.info.status running or orphan, and, configured by the cluster config,
// its config is applied, config:info().status ready or check_warnings, or
// check_errors, which is up with a warning: what tt instance status shows
// as BOX and CONFIG, whose alerts tt instance status --details shows. An
// instance that runs its script needs the box alone.
func Probe(ctx context.Context, inst *running.InstanceCtx) (bool, string, error) {
	state, err := probeInstance(ctx, inst.ConsoleSocket)
	if err != nil {
		return false, "", err
	}

	isUp, described := state.isUp(running.IsScript(inst))

	return isUp, described, nil
}

// isUp reports whether a Tarantool instance in state is up, one that runs
// its script by its box alone, and the state as tt instance status shows it.
func (state startState) isUp(script bool) (bool, string) {
	described := "BOX " + orDash(state.Box)
	boxUp := state.Box == "running" || state.Box == "orphan"

	if script {
		return boxUp, described
	}

	described = fmt.Sprintf("CONFIG %s, %s", orDash(state.Config), described)

	switch state.Config {
	case "ready", "check_warnings", "check_errors":
		return boxUp, described
	}

	return false, described
}

// orDash returns text, or what tt instance status shows for a value it has
// not got.
func orDash(text string) string {
	if text == "" {
		return defaultModuleStatus
	}

	return text
}

// probeInstance asks the instance behind the console socket how far it is.
func probeInstance(ctx context.Context, socket string) (startState, error) {
	var state startState

	conn, err := connector.ConnectContext(ctx, connector.ConnectOpts{
		Network:  "unix",
		Address:  socket,
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
		return state, fmt.Errorf("connecting: %w", err)
	}

	// Closing interrupts the request when ctx is over.
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })

	defer func() {
		stop()

		_ = conn.Close()
	}()

	res, err := conn.Eval(probeLua, []any{},
		connector.RequestOpts{PushCallback: nil, ReadTimeout: 0, ResData: nil})
	if err != nil {
		return state, fmt.Errorf("asking: %w", err)
	}

	if len(res) == 0 {
		return state, errNoProbeAnswer
	}

	err = mapstructure.Decode(res[0], &state)
	if err != nil {
		return state, fmt.Errorf("decoding: %w", err)
	}

	return state, nil
}
