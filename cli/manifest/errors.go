package manifest

import (
	"errors"
	"fmt"
)

// ErrUnsupportedVersion is returned when a manifest_version or lock_version
// declares a major component this build of tt does not support. It is wrapped
// with the concrete versions for context; callers match it with errors.Is and
// typically respond by asking the user to upgrade tt.
var ErrUnsupportedVersion = errors.New("not supported by this tt")

// ValidationError is a structural problem with a manifest or lock, tied to a
// specific field. Field is a dotted path ("package.name",
// "components.api.build.make_target", "dependencies.luasocket"); it is empty
// for errors found while decoding, where the TOML layer supplies the location.
//
// Callers match it with errors.As to locate the offending field rather than
// scraping the message text.
type ValidationError struct {
	Field string
	Msg   string
}

// Error renders the error as "<field>: <msg>", or just the message when no
// field is attached.
func (e *ValidationError) Error() string {
	if e.Field == "" {
		return e.Msg
	}

	return e.Field + ": " + e.Msg
}

// invalid builds a *ValidationError with a printf-style message. The returned
// error is the interface type so callers can return it directly without
// tripping the typed-nil trap.
func invalid(field, format string, args ...any) error {
	return &ValidationError{Field: field, Msg: fmt.Sprintf(format, args...)}
}
