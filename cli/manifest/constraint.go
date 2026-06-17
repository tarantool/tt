package manifest

import "strings"

// Constraint is a platform version requirement parsed into its semver part and
// an optional flavor. It is stored as "<semver-constraint>[<flavor>]" in TOML,
// where <flavor> is [ce] or [ee].
//
// The semver constraint itself is kept verbatim - validating the range is the
// resolver's job, not this layer's. Only the flavor suffix is parsed here.
//
//nolint:recvcheck // TextMarshaler needs a value receiver, UnmarshalText a pointer.
type Constraint struct {
	// Version is the semver constraint part, e.g. ">=3.0.0,<4.0.0". Empty for
	// an unset (omitted) constraint.
	Version string
	// Flavor is "ce", "ee" or "" (unspecified). For tarantool/tt an empty
	// flavor means [ce]; see EffectiveFlavor.
	Flavor string
}

// IsZero reports whether the constraint is unset.
func (c Constraint) IsZero() bool {
	return c.Version == "" && c.Flavor == ""
}

// EffectiveFlavor returns the flavor with the [ce] default applied to an
// unspecified flavor.
func (c Constraint) EffectiveFlavor() string {
	if c.Flavor == "" {
		return "ce"
	}

	return c.Flavor
}

// String renders the constraint back to its "<constraint>[<flavor>]" form.
func (c Constraint) String() string {
	if c.Flavor == "" {
		return c.Version
	}

	return c.Version + "[" + c.Flavor + "]"
}

// MarshalText implements encoding.TextMarshaler so the constraint serializes as
// a TOML string.
func (c Constraint) MarshalText() ([]byte, error) {
	return []byte(c.String()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler. It splits off a trailing
// [flavor] suffix and validates the flavor token; an unknown flavor or a
// bracket suffix with no version is a parse error. The semver part is stored
// verbatim.
func (c *Constraint) UnmarshalText(text []byte) error {
	version, flavor, err := splitFlavor(string(text))
	if err != nil {
		return err
	}

	c.Version = version
	c.Flavor = flavor

	return nil
}

// splitFlavor separates a "<version>[<flavor>]" string into its parts. A string
// with no trailing [flavor] suffix is returned verbatim as the version with an
// empty flavor. An unbalanced bracket, an unknown flavor token, or a suffix
// with no version before it is a parse error.
func splitFlavor(raw string) (string, string, error) {
	if !strings.HasSuffix(raw, "]") {
		return raw, "", nil
	}

	open := strings.LastIndex(raw, "[")
	if open < 0 {
		return "", "", invalid("", "invalid version constraint %q: unbalanced %q", raw, "[")
	}

	version := raw[:open]
	flavor := raw[open+1 : len(raw)-1]

	switch flavor {
	case "ce", "ee":
	default:
		return "", "", invalid("",
			"invalid version constraint %q: unknown flavor %q (want [ce] or [ee])", raw, flavor)
	}

	if version == "" {
		return "", "", invalid("",
			"invalid version constraint %q: missing version before flavor", raw)
	}

	return version, flavor, nil
}
