package running

import "path/filepath"

// artifactsPathBuilder is a builder pattern implementation for app artifacts paths generation.
type artifactsPathBuilder struct {
	// baseDir is base directory. Relative path is related to this dir.
	baseDir string
	// path is a path for using in conjunction with baseDir.
	path string
	// appName is an application name.
	appName string
	// instanceName is an instanceName.
	instanceName string
	// tarantoolctlLayout enables/disables tarantoolctl compatible path generation.
	tarantoolctlLayout bool
}

// WithPath sets path in builder.
func (builder *artifactsPathBuilder) WithPath(path string) *artifactsPathBuilder {
	builder.path = path
	return builder
}

// ForInstance sets instance name in builder.
func (builder *artifactsPathBuilder) ForInstance(instanceName string) *artifactsPathBuilder {
	builder.instanceName = instanceName
	return builder
}

// WithTarantoolctlLayout enables/disables tarantoolctl layout flag in builder.
func (builder *artifactsPathBuilder) WithTarantoolctlLayout(ctlLayout bool) *artifactsPathBuilder {
	builder.tarantoolctlLayout = ctlLayout
	return builder
}

// makePath make application path with rules:
// * if path is not set:
//   - if single instance application: baseBath + application name.
//   - else : baseBath + application name + instance name.
//
// * if path is set and it is absolute:
//   - if single instance application: path + application name
//   - else: path + application name + instance name.
//
// * if path is set and it is relative:
//   - if single instance application: basePath + path + application name.
//   - else: basePath + path + application name + instance name.
//
// * if tarantoolctlLayout flag is set, application subdirectory is not created for single
// instance applications for runtime aftifacts.
func (builder *artifactsPathBuilder) Make() string {
	if builder.path == "" {
		if builder.instanceName == "" {
			if builder.tarantoolctlLayout {
				return builder.baseDir
			}
			return filepath.Join(builder.baseDir, builder.appName)
		} else {
			return filepath.Join(builder.baseDir, builder.appName, builder.instanceName)
		}
	}

	if filepath.IsAbs(builder.path) {
		if builder.instanceName == "" {
			if builder.tarantoolctlLayout {
				return builder.path
			}
			return filepath.Join(builder.path, builder.appName)
		} else {
			return filepath.Join(builder.path, builder.appName, builder.instanceName)
		}
	}

	if builder.instanceName == "" {
		if builder.tarantoolctlLayout {
			return filepath.Join(builder.baseDir, builder.path)
		}
		return filepath.Join(builder.baseDir, builder.path, builder.appName)
	}
	return filepath.Join(builder.baseDir, builder.path, builder.appName, builder.instanceName)
}

// NewArtifactsPathBuilder creates new builder for paths generation.
func NewArtifactsPathBuilder(baseDir, appName string) *artifactsPathBuilder {
	return &artifactsPathBuilder{
		baseDir: baseDir,
		appName: appName,
	}
}
