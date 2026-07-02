package boxbackup

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/lib/backup"
)

type mockEvaler struct {
	exprs []string
	args  []any
	ret   []any
	queue [][]any
	err   error
	calls int
}

func (m *mockEvaler) Eval(expr string, args []any,
	opts connector.RequestOpts,
) ([]any, error) {
	m.exprs = append(m.exprs, expr)
	m.args = args
	m.calls++
	if m.queue != nil {
		return m.queue[m.calls-1], m.err
	}

	return m.ret, m.err
}

func (m *mockEvaler) Close() error { return nil }

// expr returns the expression of the last recorded Eval call.
func (m *mockEvaler) expr() string {
	if len(m.exprs) == 0 {
		return ""
	}

	return m.exprs[len(m.exprs)-1]
}

func TestStart_full(t *testing.T) {
	m := &mockEvaler{}

	require.NoError(t, Start(m, StartOpts{}))
	require.Equal(t, "box.backup.start(...)", m.expr())

	require.Len(t, m.args, 1)
	got := m.args[0].(map[string]any)
	require.Equal(t, defaultTTL.Seconds(), got["ttl"])
	_, hasFromVclock := got["from_vclock"]
	require.False(t, hasFromVclock, "full backup must not send from_vclock")
}

func TestStart_incremental(t *testing.T) {
	m := &mockEvaler{}

	err := Start(m, StartOpts{FromVclock: backup.Vclock{1: 42}})
	require.NoError(t, err)

	got := m.args[0].(map[string]any)
	require.Equal(t, map[uint32]uint64{1: 42}, got["from_vclock"])
}

func TestStart_customTTL(t *testing.T) {
	m := &mockEvaler{}

	require.NoError(t, Start(m, StartOpts{TTL: 30 * time.Minute}))
	got := m.args[0].(map[string]any)
	require.Equal(t, float64(1800), got["ttl"])
}

func TestStart_error(t *testing.T) {
	m := &mockEvaler{err: errors.New("boom")}

	err := Start(m, StartOpts{})
	require.ErrorContains(t, err, "failed to start backup")
	require.ErrorContains(t, err, "boom")
}

func TestGetInfo_noBackup(t *testing.T) {
	m := &mockEvaler{ret: []any{nil}}

	info, err := GetInfo(m)
	require.NoError(t, err)
	require.Nil(t, info)
}

func TestGetInfo_allFields(t *testing.T) {
	m := &mockEvaler{ret: []any{map[any]any{
		"files":        []any{"0.snap", "0.xlog"},
		"type":         "full",
		"vclock_begin": map[any]any{uint64(0): uint64(1)},
		"vclock_end":   map[any]any{uint64(0): uint64(5), uint64(1): uint64(9)},
		"recovery_points": []any{map[any]any{
			"uuid": "rp-1", "replica_id": uint64(1),
			"lsn": uint64(7), "timestamp": uint64(123),
		}},
	}}}

	info, err := GetInfo(m)
	require.NoError(t, err)
	require.Equal(t, []string{"0.snap", "0.xlog"}, info.Files)
	require.Equal(t, backup.BackupTypeFull, info.Type)
	require.Equal(t, backup.Vclock{0: 1}, info.VclockBegin)
	require.Equal(t, backup.Vclock{0: 5, 1: 9}, info.VclockEnd)
	require.NotNil(t, info.RecoveryPoints)
	require.Len(t, *info.RecoveryPoints, 1)
	rp := (*info.RecoveryPoints)[0]
	require.Equal(t, "rp-1", rp.UUID)
	require.Equal(t, uint32(1), rp.ReplicaID)
	require.Equal(t, uint64(7), rp.LSN)
	require.Equal(t, int64(123), rp.Timestamp)
}

func TestGetInfo_recoveryPointsStates(t *testing.T) {
	tests := []struct {
		name      string
		resp      map[any]any
		wantNil   bool
		wantLen   int
		wantFirst string
	}{
		{
			name:    "absent maps to nil",
			resp:    map[any]any{"type": "full"},
			wantNil: true,
		},
		{
			name:    "empty list is non-nil and empty",
			resp:    map[any]any{"type": "full", "recovery_points": []any{}},
			wantNil: false,
			wantLen: 0,
		},
		{
			name: "populated list is preserved",
			resp: map[any]any{
				"type":            "full",
				"recovery_points": []any{map[any]any{"uuid": "rp-1"}},
			},
			wantNil:   false,
			wantLen:   1,
			wantFirst: "rp-1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// map[any]any shape (native net.box msgpack decoding).
			infoAny, err := GetInfo(&mockEvaler{ret: []any{tc.resp}})
			require.NoError(t, err)

			// map[string]any shape (e.g. a decoder yielding string keys).
			infoStr, err := GetInfo(&mockEvaler{ret: []any{stringKeyed(tc.resp)}})
			require.NoError(t, err)

			for _, info := range []*Info{infoAny, infoStr} {
				if tc.wantNil {
					require.Nil(t, info.RecoveryPoints, "absent field must map to nil")
					continue
				}
				require.NotNil(t, info.RecoveryPoints, "present list must be non-nil")
				require.Len(t, *info.RecoveryPoints, tc.wantLen)
				if tc.wantLen > 0 {
					require.Equal(t, tc.wantFirst, (*info.RecoveryPoints)[0].UUID)
				}
			}
		})
	}
}

func stringKeyed(in map[any]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if nested, ok := v.(map[any]any); ok {
			v = stringKeyed(nested)
		}
		if list, ok := v.([]any); ok {
			converted := make([]any, len(list))
			for i, item := range list {
				if nested, ok := item.(map[any]any); ok {
					converted[i] = stringKeyed(nested)
				} else {
					converted[i] = item
				}
			}
			v = converted
		}
		key, ok := k.(string)
		if !ok {
			key = fmt.Sprintf("%v", k)
		}
		out[key] = v
	}
	return out
}

func TestGetInfo_error(t *testing.T) {
	m := &mockEvaler{err: errors.New("boom")}

	_, err := GetInfo(m)
	require.ErrorContains(t, err, "failed to get backup info")
}

func TestStop_ok(t *testing.T) {
	m := &mockEvaler{}

	require.NoError(t, Stop(m))
	require.Equal(t, "box.backup.stop()", m.expr())
}

func TestStop_error(t *testing.T) {
	m := &mockEvaler{err: errors.New("boom")}

	err := Stop(m)
	require.ErrorContains(t, err, "failed to stop backup")
	require.ErrorContains(t, err, "boom")
}

func TestStartInfoStop(t *testing.T) {
	m := &mockEvaler{queue: [][]any{
		nil, // start()
		{map[any]any{ // info()
			"files":           []any{"0.snap"},
			"type":            "full",
			"recovery_points": []any{},
		}},
		nil, // stop()
	}}

	require.NoError(t, Start(m, StartOpts{}))

	info, err := GetInfo(m)
	require.NoError(t, err)
	require.Equal(t, backup.BackupTypeFull, info.Type)
	require.NotNil(t, info.RecoveryPoints)

	require.NoError(t, Stop(m))
	require.Equal(t,
		[]string{"box.backup.start(...)", "return box.backup.info()", "box.backup.stop()"},
		m.exprs)
}
