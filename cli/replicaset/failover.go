package replicaset

import (
	"strings"
)

// Failover defines an enumeration of failover types.
type Failover int

//go:generate stringer -type=Failover -trimprefix Failover -linecomment

const (
	// FailoverUnknown is unknown type of a failover.
	FailoverUnknown Failover = iota // unknown
	// FailoverOff is a disabled failover.
	// Is is a "off" failover type for the centralized config.
	FailoverOff // off
	// FailoverManual is a "manual" failover type for the centralized config.
	FailoverManual // manual
	// FailoverElection uses a RAFT based algorithm for the leader election.
	// Is is a "election" failover type for the centralized config.
	FailoverElection // election
	// FailoverSupervised is a "supervised" failover type for the centralized config.
	FailoverSupervised // supervised
)

// ParseFailover returns a failover type from a string representation.
func ParseFailover(str string) Failover {
	switch strings.ToLower(str) {
	case "off", "disabled":
		return FailoverOff
	case "manual":
		return FailoverManual
	case "election", "raft":
		return FailoverElection
	case "supervised":
		return FailoverSupervised
	default:
		return FailoverUnknown
	}
}
