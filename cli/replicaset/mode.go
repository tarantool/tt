package replicaset

// Mode defines an enumeration of read-write modes for an instance.
type Mode int

//go:generate stringer -type Mode -trimprefix Mode -linecomment

const (
	// ModeUnknown is used when read-write mode is unknown.
	ModeUnknown Mode = iota // unknown
	// MOdeRead is used when box.cfg.read_only = true.
	ModeRead // read
	// ModeRW is used when box.cfg.read_only = false.
	ModeRW // rw
)
