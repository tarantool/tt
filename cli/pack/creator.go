package pack

func CreatePacker(packCtx *PackCtx) Packer {
	packType := PackageType(packCtx.Type)
	switch packType {
	case Tgz:
		return &archivePacker{}
	case Deb:
		return &debPacker{}
	case Rpm:
		return nil
	case Docker:
		return nil
	default:
		return nil
	}
}
