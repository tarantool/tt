package pack

const (
	defaultFileUser   = "root"
	defaultFileGroup  = "root"
	defaultFileLang   = ""
	defaultFileLinkTo = ""
	emptyDigest       = ""

	headerSignatures = 62
	headerImmutable  = 63

	hashAlgoSHA256 = 8

	// XXX.
	fileFlag = 1 << 4
	dirFlag  = 0

	rpmTypeNull        = 0
	rpmTypeChar        = 1
	rpmTypeInt8        = 2
	rpmTypeInt16       = 3
	rpmTypeInt32       = 4
	rpmTypeInt64       = 5
	rpmTypeString      = 6
	rpmTypeBin         = 7
	rpmTypeStringArray = 8
	rpmTypeI18nString  = 9

	signatureTagSize        = 1000
	signatureTagMD5         = 1004
	signatureTagPayloadSize = 1007
	signatureTagSHA1        = 269

	tagName              = 1000
	tagVersion           = 1001
	tagRelease           = 1002
	tagEpoch             = 1003
	tagSummary           = 1004
	tagDescription       = 1005
	tagBuildtime         = 1006
	tagSize              = 1009
	tagOs                = 1021
	tagArch              = 1022
	tagLicense           = 1014
	tagGroup             = 1016
	tagPayloadFormat     = 1124
	tagPayloadCompressor = 1125
	tagPayloadFlags      = 1126
	tagPreIn             = 1023
	tagPostIn            = 1024
	tagPreInProg         = 1085
	tagPostInProg        = 1086
	tagDirNames          = 1118
	tagBaseNames         = 1117
	tagDirIndexes        = 1116
	tagFileUserNames     = 1039
	tagFileGroupNames    = 1040
	tagFileSizes         = 1028
	tagFileModes         = 1030
	tagFileINodes        = 1096
	tagFileDevices       = 1095
	tagRpmVersion        = 1064
	tagFileMTimes        = 1034
	tagFileFlags         = 1037
	tagFileLangs         = 1097
	tagFileRDevs         = 1033
	tagFileDigests       = 1035
	tagFileLinkTos       = 1036
	tagRequireFlags      = 1048
	tagRequireName       = 1049
	tagRequireVersion    = 1050
	tagPayloadDigest     = 5092
	tagPayloadDigestAlgo = 5093

	rpmSenseLess         = 0x02
	rpmSenseGreater      = 0x04
	rpmSenseEqual        = 0x08
	rpmSensePreReq       = 0x40
	rpmSenseInterp       = 0x100
	rpmSenseScriptPre    = 0x200
	rpmSenseScriptPost   = 0x400
	rpmSenseScriptPreUn  = 0x800
	rpmSenseScriptPostUn = 0x1000
)

// spell-checker:ignore tmpfiles

var (
	headerMagic   = []byte{0x8e, 0xad, 0xe8}
	versionByte   = 0x01
	reservedBytes = 0

	systemDirs = map[string]struct{}{
		// in fact, all this dirs has leading '/'
		".":                      {},
		"bin":                    {},
		"usr":                    {},
		"usr/bin":                {},
		"usr/local":              {},
		"usr/local/bin":          {},
		"usr/share":              {},
		"usr/share/tarantool":    {},
		"usr/lib":                {},
		"usr/lib/tmpfiles.d":     {},
		"var":                    {},
		"var/lib":                {},
		"var/run":                {},
		"var/log":                {},
		"etc":                    {},
		"etc/tarantool":          {},
		"etc/tarantool/conf.d":   {},
		"etc/systemd":            {},
		"etc/systemd/system":     {},
		"usr/lib/systemd":        {},
		"usr/lib/systemd/system": {},
	}

	boundariesByType = map[rpmValueType]int{
		rpmTypeNull:        1,
		rpmTypeBin:         1,
		rpmTypeChar:        1,
		rpmTypeString:      1,
		rpmTypeStringArray: 1,
		rpmTypeInt8:        1,
		rpmTypeInt16:       2,
		rpmTypeInt32:       4,
		rpmTypeInt64:       8,
	}
)
