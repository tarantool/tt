// Code generated by "stringer -type Mode -trimprefix Mode -linecomment"; DO NOT EDIT.

package replicaset

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[ModeUnknown-0]
	_ = x[ModeRead-1]
	_ = x[ModeRW-2]
}

const _Mode_name = "unknownreadrw"

var _Mode_index = [...]uint8{0, 7, 11, 13}

func (i Mode) String() string {
	if i < 0 || i >= Mode(len(_Mode_index)-1) {
		return "Mode(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _Mode_name[_Mode_index[i]:_Mode_index[i+1]]
}
