package pack

import "github.com/tarantool/tt/cli/cmdcontext"

func CreatePacker(packCtx *cmdcontext.PackCtx) Packer {
	packType := PackageType(packCtx.Type)
	switch packType {
	case Tgz:
		return &archivePacker{}
	case Deb:
		return nil
	case Rpm:
		return nil
	case Docker:
		return nil
	default:
		return nil
	}
}
