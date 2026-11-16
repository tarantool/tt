package cmd

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/cli/manifest/inventory"
	"github.com/tarantool/tt/v3/cli/manifest/state"
)

// TestRestartHints: after an install or a switch, the running instances that
// do not run the active version are named, with tt instance restart for those
// tt runs and systemctl restart for those under the units of
// tt service systemd, which tt instance restart refuses.
func TestRestartHints(t *testing.T) {
	units := map[string]string{"kv-a": "kv-api@kv-a.service", "kv-b": "kv-api@kv-b.service"}
	hints := func(running ...inventory.RunningInstance) []string {
		return restartHints("kv-api", state.ScopeSystem, "1.0.0", running, nil)
	}

	assert.Empty(t, hints(), "nothing runs, nothing to tell")
	assert.Empty(t, hints(inventory.RunningInstance{Name: "kv-a", Pid: 10, Version: "1.0.0"}),
		"an instance on the active version is not to be moved")

	assert.Equal(t, []string{
		"2 instances of kv-api run 1.1.0: kv-a, kv-b",
		"to move them to 1.0.0: tt instance restart kv-api:kv-a,kv-api:kv-b --scope system",
	}, hints(
		inventory.RunningInstance{Name: "kv-a", Pid: 10, Version: "1.1.0"},
		inventory.RunningInstance{Name: "kv-b", Pid: 11, Version: "1.1.0"},
		inventory.RunningInstance{Name: "kv-c", Pid: 12, Version: "1.0.0"},
	))

	assert.Equal(t, []string{
		"1 instance of kv-api runs 1.1.0: kv-a",
		"to move them to 1.0.0: tt instance restart kv-api:kv-a --scope system",
	}, hints(inventory.RunningInstance{Name: "kv-a", Pid: 10, Version: "1.1.0"}))

	assert.Equal(t, []string{
		"3 instances of kv-api run another version than 1.0.0: " +
			"kv-a (1.1.0), kv-b (0.9.0), kv-c (version unknown)",
		"to move them to 1.0.0: tt instance restart kv-api:kv-c --scope system",
		"to move those under systemd to 1.0.0: " +
			"systemctl restart kv-api@kv-a.service kv-api@kv-b.service",
	}, restartHints("kv-api", state.ScopeSystem, "1.0.0", []inventory.RunningInstance{
		{Name: "kv-a", Pid: 10, Version: "1.1.0"},
		{Name: "kv-b", Pid: 11, Version: "0.9.0"},
		{Name: "kv-c", Pid: 12, Version: ""},
	}, units))
}
