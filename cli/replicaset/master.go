package replicaset

// Master defines an enumeration of master types.
type Master int

//go:generate stringer -type=Master -trimprefix Master -linecomment

const (
	// MasterUnknown is unknown type of a replicaset.
	MasterUnknown Master = iota // unknown
	// MasterNo is used when no master in a replicaset.
	MasterNo // no
	// MasterSingle is used when there is a single master in a replicaset.
	MasterSingle // single
	// MasterMulti is used when there are > 1 master in a replicaset.
	MasterMulti // multi
)
