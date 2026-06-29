package version

import (
	"fmt"
	"strings"
	"time"
)

// GenerateVersionLua renders the version.lua descriptor for ver, stamped with
// builtAt. The build drops the result at
// .rocks/share/tarantool/<package>/version.lua; this function only produces the
// text and never touches the filesystem, so detecting a pre-existing
// version.lua is the build's job.
//
// Generation is on by default and disabled per-package via
// [package].generate_version_lua = false; that gate lives in the build, which
// consults manifest.Package.GenerateVersionLuaValue before calling here.
func GenerateVersionLua(ver Version, builtAt time.Time) string {
	var buf strings.Builder

	buf.WriteString("return {\n")
	fmt.Fprintf(&buf, "    version  = %s,\n", luaString(ver.SemVer))
	fmt.Fprintf(&buf, "    commit   = %s,\n", luaString(ver.Commit))
	fmt.Fprintf(&buf, "    dirty    = %t,\n", ver.Dirty)
	fmt.Fprintf(&buf, "    flavor   = %s,\n", luaString(ver.Flavor))
	fmt.Fprintf(&buf, "    built_at = %s,\n", luaString(builtAt.UTC().Format(time.RFC3339)))
	buf.WriteString("}\n")

	return buf.String()
}

// luaString renders s as a double-quoted Lua string literal, escaping the two
// characters that would otherwise break out of it.
func luaString(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)

	return `"` + s + `"`
}
