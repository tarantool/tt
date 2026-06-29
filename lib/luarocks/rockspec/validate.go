package rockspec

import (
	"errors"
	"fmt"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// allowedBuildTypes mirrors the upstream backend table for the subset we
// implement. Empty string defaults to "builtin" per upstream rockspecs.lua.
var allowedBuildTypes = map[string]bool{
	"":        true, // implies builtin
	"builtin": true,
	"cmake":   true,
	"make":    true,
	"command": true,
	"none":    true,
}

// Validate performs presence and feature-allowlist checks on a harvested
// Rockspec. It deliberately does NOT re-do every type assertion the upstream
// `type_check.lua` schema enforces — the harvest in Eval already routes each
// field through a typed path. Fail loud on unknown build types.
func Validate(spec *rocks.Rockspec) error {
	if spec == nil {
		return errors.New("rockspec: nil spec")
	}

	if spec.Package == "" {
		return errors.New("rockspec: missing required field 'package'")
	}

	if spec.Version == "" {
		return errors.New("rockspec: missing required field 'version'")
	}

	if spec.Source.URL == "" {
		return errors.New("rockspec: missing required field 'source.url'")
	}

	if !allowedBuildTypes[spec.Build.Type] {
		return fmt.Errorf("%w: build.type=%q (supported: builtin, cmake, make, command, none)",
			rocks.ErrUnsupportedRockspecFeature, spec.Build.Type)
	}

	return nil
}
