package windisk

import (
	"reflect"
	"testing"
)

const TestWMICDiskOutput = `
Availability=1
BytesPerSector=512
Capabilities={3,4}
CapabilityDescriptions={"Random Access","Supports Writing"}
CompressionMethod=Something
ConfigManagerErrorCode=0
ConfigManagerUserConfig=FALSE
DefaultBlockSize=102
Description=Disk drive
DeviceID=\\.\PHYSICALDRIVE0
ErrorCleared=FALSE
ErrorDescription="The Description"
ErrorMethodology="The Methodology"
Index=0
InstallDate="WAT FORMAT"
InterfaceType=SCSI
LastErrorCode=102014
Manufacturer=(Standard disk drives)
MaxBlockSize=1234
MaxMediaSize=5678
MediaLoaded=TRUE
MediaType=Fixed hard disk media
MinBlockSize=1
Model=VMware, VMware Virtual S SCSI Disk Device
Name=\\.\PHYSICALDRIVE0
NeedsCleaning=FALSE
NumberOfMediaSupported=2
Partitions=2
PNPDeviceID=SCSI\DISK&amp;VEN_VMWARE_&amp;PROD_VMWARE_VIRTUAL_S\5&amp;1EC51BF7&amp;0&amp;000000
PowerManagementCapabilities=123
PowerManagementSupported=TRUE
SCSIBus=0
SCSILogicalUnit=0
SCSIPort=0
SCSITargetId=0
SectorsPerTrack=63
Signature=1623773652
Size=64420392960
Status=OK
StatusInfo=2
SystemName=WIN-HLBC3JFV4AV
TotalCylinders=7832
TotalHeads=255
TotalSectors=125821080
TotalTracks=1997160
TracksPerCylinder=255
`

var ExpDiskMap = map[string]string{
	"Availability":                `1`,
	"BytesPerSector":              `512`,
	"Capabilities":                `{3,4}`,
	"CapabilityDescriptions":      `{"Random Access","Supports Writing"}`,
	"CompressionMethod":           `Something`,
	"ConfigManagerErrorCode":      `0`,
	"ConfigManagerUserConfig":     `FALSE`,
	"DefaultBlockSize":            `102`,
	"Description":                 `Disk drive`,
	"DeviceID":                    `\\.\PHYSICALDRIVE0`,
	"ErrorCleared":                `FALSE`,
	"ErrorDescription":            `"The Description"`,
	"ErrorMethodology":            `"The Methodology"`,
	"Index":                       `0`,
	"InstallDate":                 `"WAT FORMAT"`,
	"InterfaceType":               `SCSI`,
	"LastErrorCode":               `102014`,
	"Manufacturer":                `(Standard disk drives)`,
	"MaxBlockSize":                `1234`,
	"MaxMediaSize":                `5678`,
	"MediaLoaded":                 `TRUE`,
	"MediaType":                   `Fixed hard disk media`,
	"MinBlockSize":                `1`,
	"Model":                       `VMware, VMware Virtual S SCSI Disk Device`,
	"Name":                        `\\.\PHYSICALDRIVE0`,
	"NeedsCleaning":               `FALSE`,
	"NumberOfMediaSupported":      `2`,
	"Partitions":                  `2`,
	"PNPDeviceID":                 `SCSI\DISK&amp;VEN_VMWARE_&amp;PROD_VMWARE_VIRTUAL_S\5&amp;1EC51BF7&amp;0&amp;000000`,
	"PowerManagementCapabilities": `123`,
	"PowerManagementSupported":    `TRUE`,
	"SCSIBus":                     `0`,
	"SCSILogicalUnit":             `0`,
	"SCSIPort":                    `0`,
	"SCSITargetId":                `0`,
	"SectorsPerTrack":             `63`,
	"Signature":                   `1623773652`,
	"Size":                        `64420392960`,
	"Status":                      `OK`,
	"StatusInfo":                  `2`,
	"SystemName":                  `WIN-HLBC3JFV4AV`,
	"TotalCylinders":              `7832`,
	"TotalHeads":                  `255`,
	"TotalSectors":                `125821080`,
	"TotalTracks":                 `1997160`,
	"TracksPerCylinder":           `255`,
}

var ExpDisk = Disk{
	Availability:                1,
	BytesPerSector:              512,
	Capabilities:                []uint16{3, 4},
	CapabilityDescriptions:      []string{"Random Access", "Supports Writing"},
	CompressionMethod:           "Something",
	ConfigManagerErrorCode:      0,
	ConfigManagerUserConfig:     false,
	DefaultBlockSize:            102,
	Description:                 "Disk drive",
	DeviceID:                    `\\.\PHYSICALDRIVE0`,
	ErrorCleared:                false,
	ErrorDescription:            "The Description",
	ErrorMethodology:            "The Methodology",
	Index:                       0,
	InstallDate:                 "WAT FORMAT",
	InterfaceType:               "SCSI",
	LastErrorCode:               102014,
	Manufacturer:                "(Standard disk drives)",
	MaxBlockSize:                1234,
	MaxMediaSize:                5678,
	MediaLoaded:                 true,
	MediaType:                   "Fixed hard disk media",
	MinBlockSize:                1,
	Model:                       "VMware, VMware Virtual S SCSI Disk Device",
	Name:                        `\\.\PHYSICALDRIVE0`,
	NeedsCleaning:               false,
	NumberOfMediaSupported:      2,
	Partitions:                  2,
	PNPDeviceID:                 `SCSI\DISK&amp;VEN_VMWARE_&amp;PROD_VMWARE_VIRTUAL_S\5&amp;1EC51BF7&amp;0&amp;000000`,
	PowerManagementCapabilities: 123,
	PowerManagementSupported:    true,
	SCSIBus:                     0,
	SCSILogicalUnit:             0,
	SCSIPort:                    0,
	SCSITargetId:                0,
	SectorsPerTrack:             63,
	Signature:                   1623773652,
	Size:                        64420392960,
	Status:                      "OK",
	StatusInfo:                  2,
	SystemName:                  "WIN-HLBC3JFV4AV",
	TotalCylinders:              7832,
	TotalHeads:                  255,
	TotalSectors:                125821080,
	TotalTracks:                 1997160,
	TracksPerCylinder:           255,
}

func TestParseDiskDrive(t *testing.T) {
	m, err := parseDiskDrive(TestWMICDiskOutput)
	if err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(m, ExpDiskMap) {
		t.Errorf("parseDiskDrive: failed to match")
	}
}

func TestParseDisk(t *testing.T) {
	var d Disk
	if err := parseDisk(ExpDiskMap, &d); err != nil {
		t.Error(err)
	}
	if !reflect.DeepEqual(ExpDisk, d) {
		t.Errorf("ParseDisk: failed to match")
	}
}

func TestList(t *testing.T) {
	disks, err := List()
	if err != nil {
		t.Error(err)
	}
	if len(disks) == 0 {
		t.Error("List: returned 0 disks")
	}
}

func BenchmarkList(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, err := List(); err != nil {
			b.Fatal(err)
		}
	}
}
