package layout

import (
	"fmt"

	"github.com/tarantool/tt/cli/util"
)

// TntCtlLayout implements Layout for tarantoolctl.
type TntCtlLayout struct {
	baseDir string
	appName string
}

// NewTntCtlLayout creates new layout for tarantoolctl.
func NewTntCtlLayout(baseDir, appName string) (*TntCtlLayout, error) {
	if appName == "" {
		return nil, fmt.Errorf("application name cannot be empty")
	}
	if baseDir == "" {
		return nil, fmt.Errorf("base directory cannot be empty")
	}

	return &TntCtlLayout{
		baseDir: baseDir,
		appName: appName,
	}, nil
}

// genFilePath generate file path.
func (layout TntCtlLayout) genRuntimeFilePath(subdir, fileName string) string {
	return util.JoinPaths(layout.baseDir, subdir, fileName)
}

// PidFile returns pid file path.
func (layout TntCtlLayout) PidFile(dir string) string {
	return layout.genRuntimeFilePath(dir, layout.appName+".pid")
}

// LogFile returns log file path.
func (layout TntCtlLayout) LogFile(dir string) string {
	return layout.genRuntimeFilePath(dir, layout.appName+".log")
}

// ConsoleSocket returns console file path.
func (layout TntCtlLayout) ConsoleSocket(dir string) string {
	return layout.genRuntimeFilePath(dir, layout.appName+".control")
}

// BinaryPort returns binary port file path.
func (layout TntCtlLayout) BinaryPort(dir string) string {
	return layout.genRuntimeFilePath(dir, layout.appName+".sock")
}

// DataDir returns data directory path.
func (layout TntCtlLayout) DataDir(dir string) string {
	return util.JoinPaths(layout.baseDir, dir, layout.appName)
}
