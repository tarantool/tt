package pack

import (
	"fmt"
	"os"

	"github.com/tarantool/tt/cli/util"
)

// genSignature generates the signature for rpm.
func genSignature(rpmBodyFilePath, rpmHeaderFilePath, cpioPath string) (*rpmTagSetType, error) {
	// SHA1
	sha1, err := util.FileSHA1Hex(rpmHeaderFilePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to get header sha1: %s", err)
	}

	// SIG_SIZE
	rpmBodyFileInfo, err := os.Stat(rpmBodyFilePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to get RPM body size: %s", err)
	}
	rpmBodyFileSize := rpmBodyFileInfo.Size()

	// PAYLOADSIZE
	cpioFileInfo, err := os.Stat(cpioPath)
	if err != nil {
		return nil, fmt.Errorf("Failed to get CPIO payload size: %s", err)
	}
	cpioSize := cpioFileInfo.Size()

	// MD5
	md5, err := util.FileMD5(rpmBodyFilePath)
	if err != nil {
		return nil, fmt.Errorf("Failed to get RPM body MD5: %s", err)
	}

	signature := rpmTagSetType{
		{ID: signatureTagSHA1, Type: rpmTypeString, Value: sha1},
		{ID: signatureTagSize, Type: rpmTypeInt32, Value: []int32{int32(rpmBodyFileSize)}},
		{ID: signatureTagPayloadSize, Type: rpmTypeInt32, Value: []int32{int32(cpioSize)}},
		{ID: signatureTagMD5, Type: rpmTypeBin, Value: md5},
	}

	return &signature, nil
}
