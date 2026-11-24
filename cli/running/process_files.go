package running

import (
	"path/filepath"
	"strings"

	"github.com/tarantool/tt/v3/cli/running/internal/layout"
)

// WorkDir returns the working directory of the process of inst, where a
// core the kernel dumps by a relative kernel.core_pattern goes. An external
// process and TCM run in their workdir. tarantool runs in the application
// directory, and moves to the work_dir of box.cfg once it is configured:
// process.work_dir of the config of an instance the cluster config
// configures, or TT_WORK_DIR in the env of the record of one that runs its
// script, relative to the application directory. What a script passes to
// box.cfg itself is beyond tt.
func WorkDir(inst *InstanceCtx) string {
	if IsExternal(inst) {
		return inst.Workdir
	}

	key := "process.work_dir"
	if IsScript(inst) {
		key = "work_dir"
	}

	dir, found, err := effectiveString(inst, key)
	if err != nil || !found {
		return inst.AppDir
	}

	return absoluteIn(inst.AppDir, dir)
}

// TarantoolLog returns the file the Tarantool instance inst logs into, when
// it logs into a file of its own rather than to its standard error, which
// the watchdog writes into tt.log (inst.Log): an instance the cluster config
// configures with log.to file logs into log.file, tarantool.log next to
// tt.log by default, and one that runs its script into the file TT_LOG of
// the env of its record names, file:PATH or PATH. A relative path is
// relative to the working directory of tarantool (WorkDir). It is empty for
// an instance that logs to its standard error, as by default, or elsewhere,
// to a pipe or syslog, and for an external process.
func TarantoolLog(inst *InstanceCtx) string {
	if IsExternal(inst) {
		return ""
	}

	if IsScript(inst) {
		return scriptLog(inst)
	}

	to, found, err := effectiveString(inst, "log.to")
	if err != nil || !found || to != "file" {
		return ""
	}

	file, found, err := effectiveString(inst, "log.file")
	if err != nil || !found {
		return filepath.Join(inst.LogDir, layout.TarantoolLogFileName)
	}

	return absoluteIn(WorkDir(inst), file)
}

// scriptLog is TarantoolLog of an instance that runs its script: the file of
// the option log of box.cfg as TT_LOG of the env of its record gives it.
func scriptLog(inst *InstanceCtx) string {
	value, found, err := effectiveString(inst, "log")
	if err != nil || !found {
		return ""
	}

	kind, path, typed := strings.Cut(value, ":")

	switch {
	case typed && kind == "file":
		value = path
	case typed && (kind == "pipe" || kind == "syslog"):
		return ""
	}

	// A pipe of box.cfg starts with "|" as well.
	if strings.HasPrefix(value, "|") || value == "" {
		return ""
	}

	return absoluteIn(WorkDir(inst), value)
}

// absoluteIn returns path, made absolute against dir when it is relative.
func absoluteIn(dir, path string) string {
	if filepath.IsAbs(path) {
		return filepath.Clean(path)
	}

	return filepath.Join(dir, path)
}
