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

	// XXX
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
	rpmTypeI18nstring  = 9

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
	tagPrein             = 1023
	tagPostin            = 1024
	tagPreinProg         = 1085
	tagPostinProg        = 1086
	tagDirNames          = 1118
	tagBaseNames         = 1117
	tagDirIndexes        = 1116
	tagFileUsernames     = 1039
	tagFileGroupnames    = 1040
	tagFileSizes         = 1028
	tagFileModes         = 1030
	tagFileInodes        = 1096
	tagFileDevices       = 1095
	tagRpmVersion        = 1064
	tagFileMtimes        = 1034
	tagFileFlags         = 1037
	tagFileLangs         = 1097
	tagFileRdevs         = 1033
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
	rpmSensePrereq       = 0x40
	rpmSenseInterp       = 0x100
	rpmSenseScriptPre    = 0x200
	rpmSenseScriptPost   = 0x400
	rpmSenseScriptPreun  = 0x800
	rpmSenseScriptPostun = 0x1000
)

var (
	headerMagic   = []byte{0x8e, 0xad, 0xe8}
	versionByte   = 0x01
	reservedBytes = 0

	systemDirs = map[string]struct{}{
		// in fact, all this dirs has leading '/'
		".":                      struct{}{},
		"bin":                    struct{}{},
		"usr":                    struct{}{},
		"usr/bin":                struct{}{},
		"usr/local":              struct{}{},
		"usr/local/bin":          struct{}{},
		"usr/share":              struct{}{},
		"usr/share/tarantool":    struct{}{},
		"usr/lib":                struct{}{},
		"usr/lib/tmpfiles.d":     struct{}{},
		"var":                    struct{}{},
		"var/lib":                struct{}{},
		"var/run":                struct{}{},
		"var/log":                struct{}{},
		"etc":                    struct{}{},
		"etc/tarantool":          struct{}{},
		"etc/tarantool/conf.d":   struct{}{},
		"etc/systemd":            struct{}{},
		"etc/systemd/system":     struct{}{},
		"usr/lib/systemd":        struct{}{},
		"usr/lib/systemd/system": struct{}{},
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
