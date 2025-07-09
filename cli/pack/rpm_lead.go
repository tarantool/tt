package pack

import (
	"bytes"
)

// genRpmLead generates the lead header into the buffer and returns it.
func genRpmLead(name string) *bytes.Buffer {
	// The Lead is a legacy structure that used to describe RPM files
	// before header sections were introduced.
	//
	// struct rpm_lead {
	//   unsigned char magic[4];
	//   unsigned char major, minor;
	//   short type;
	//   short arch_num;
	//   char name[66];
	//   short os_num;
	//   short signature_type;
	//   char reserved[16];
	// } ;

	var rpmLeadName [66]byte
	for i, nameByte := range []uint8(name) {
		rpmLeadName[i] = nameByte
	}

	rpmLead := packValues(
		[4]byte{0xed, 0xab, 0xee, 0xdb}, // magic
		uint8(3),                        // major
		uint8(0),                        // minor
		int16(0),                        // type
		int16(1),                        // arch_num
		rpmLeadName,                     // name
		int16(1),                        // os_num
		int16(5),                        // signature_type
		[16]int8{},                      // reserved
	)

	return rpmLead
}
