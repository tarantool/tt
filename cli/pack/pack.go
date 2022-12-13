package pack

// PackCtx contains all flags for tt pack command.
type PackCtx struct {
	// Type contains a type of packing.
	Type string
	// Name contains the name of packing bundle.
	Name string
	// Version contains the version of packing bundle.
	Version string
	// AppList contains applications to be packed.
	AppList []string
	// FileName contains the name of file of result package.
	FileName string
	// WithBinaries put binaries into the package regardless if tarantool is system or not.
	WithBinaries bool
	// WithoutBinaries ignores binaries regardless if tarantool is system or not.
	WithoutBinaries bool
	// TarantoolExecutable is a path to tarantool executable path
	TarantoolExecutable string
	// TarantoolIsSystem shows if tarantool is system.
	TarantoolIsSystem bool
	// ArchiveCtx contains flags specific for tgz type.
	Archive ArchiveCtx
	// RpmDeb contains all information about rpm and deb type of packing.
	RpmDeb RpmDebCtx
	// UseDocker is set if a package must be built in docker container.
	UseDocker bool
}

// ArchiveCtx contains flags specific for tgz type.
type ArchiveCtx struct {
	// All means pack all artifacts from bundle, including pid files etc.
	All bool
}

// RpmDebCtx contains flags specific for RPM/DEB type.
type RpmDebCtx struct {
	// WithTarantoolDeps means to add to package dependencies versions
	// of tt and tarantool from the current environment.
	WithTarantoolDeps bool
	// PreInst is a path to pre-install script.
	PreInst string
	// PostInst is a path to post-install script.
	PostInst string
	// Deps is dependencies list. Format:
	// dependency_06>=4
	Deps []string
	// DepsFile is a path to a file of dependencies.
	DepsFile string
	// SystemdUnitParamsFile is a path to file with systemd unit parameters.
	SystemdUnitParamsFile string
}
