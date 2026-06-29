package deps

// Re-export note: the user-facing types VersionedRock, InstallStep and
// the RemoteIndex interface live in the root rocks package (interfaces.go)
// so that the rocks facade can refer to them without importing deps —
// keeping rocks → deps → rocks from forming an import cycle.
//
// This file remains as a stable home for any deps-only types we add in
// the future.
