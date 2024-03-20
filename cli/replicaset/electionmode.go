package replicaset

import "strings"

type ElectionMode int

//go:generate stringer -type=ElectionMode -trimprefix ElectionMode -linecomment

const (
	// ElectionModeUnknown is unknown election mode type.
	ElectionModeUnknown ElectionMode = iota // unknown
	// ElectionModeOff is a "off" election_mode for centralized config.
	ElectionModeOff // off
	// ElectionModeVoter is a "voter" election_mode for centralized config.
	ElectionModeVoter // voter
	// ElectionModeCandidate is a "candidate" election_mode for centrlalized config.
	ElectionModeCandidate // candidate
	// ElectionModeManual is a "manual" election_mode for centralized config.
	ElectionModeManual // manual
)

func ParseElectionMode(str string) ElectionMode {
	switch strings.ToLower(str) {
	case "off":
		return ElectionModeOff
	case "voter":
		return ElectionModeVoter
	case "candidate":
		return ElectionModeCandidate
	case "manual":
		return ElectionModeManual
	default:
		return ElectionModeUnknown
	}
}
