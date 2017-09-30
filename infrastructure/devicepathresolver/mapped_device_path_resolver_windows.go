package devicepathresolver

type volume struct {
	BusNumber int
	TargetID  int
	LUN       int
}

// Notes:
//
// - Looks like we can use the TargetID to map disks (WMIC.exe DISKDRIVE LIST FULL /FORMAT:textvaluelist.xsl)

// Initialize Disk
//
// select disk 00
// attributes disk clear readonly
// online disk
// convert mbr
// create partition primary
// assign letter=XXX

// Disk Device to Device Name Mapping
//
// http://docs.aws.amazon.com/AWSEC2/latest/WindowsGuide/ec2-windows-volumes.html#windows-volume-mapping
var awsDeviceMapping = map[string]volume{
	// non-NVMe instance store volumes
	"xvdca": {BusNumber: 0, TargetID: 78, LUN: 0},
	"xvdcb": {BusNumber: 0, TargetID: 79, LUN: 0},
	"xvdcc": {BusNumber: 0, TargetID: 80, LUN: 0},
	"xvdcd": {BusNumber: 0, TargetID: 81, LUN: 0},
	"xvdce": {BusNumber: 0, TargetID: 82, LUN: 0},
	"xvdcf": {BusNumber: 0, TargetID: 83, LUN: 0},
	"xvdcg": {BusNumber: 0, TargetID: 84, LUN: 0},
	"xvdch": {BusNumber: 0, TargetID: 85, LUN: 0},
	"xvdci": {BusNumber: 0, TargetID: 86, LUN: 0},
	"xvdcj": {BusNumber: 0, TargetID: 87, LUN: 0},
	"xvdck": {BusNumber: 0, TargetID: 88, LUN: 0},
	"xvdcl": {BusNumber: 0, TargetID: 89, LUN: 0},

	// EBS volumes
	"/dev/sda1": {BusNumber: 0, TargetID: 0, LUN: 0}, // reserved root device
	"xvdb":      {BusNumber: 0, TargetID: 1, LUN: 0},
	"xvdc":      {BusNumber: 0, TargetID: 2, LUN: 0},
	"xvdd":      {BusNumber: 0, TargetID: 3, LUN: 0},
	"xvde":      {BusNumber: 0, TargetID: 4, LUN: 0},
	"xvdf":      {BusNumber: 0, TargetID: 5, LUN: 0},
	"xvdg":      {BusNumber: 0, TargetID: 6, LUN: 0},
	"xvdh":      {BusNumber: 0, TargetID: 7, LUN: 0},
	"xvdi":      {BusNumber: 0, TargetID: 8, LUN: 0},
	"xvdj":      {BusNumber: 0, TargetID: 9, LUN: 0},
	"xvdk":      {BusNumber: 0, TargetID: 10, LUN: 0},
	"xvdl":      {BusNumber: 0, TargetID: 11, LUN: 0},
	"xvdm":      {BusNumber: 0, TargetID: 12, LUN: 0},
	"xvdn":      {BusNumber: 0, TargetID: 13, LUN: 0},
	"xvdo":      {BusNumber: 0, TargetID: 14, LUN: 0},
	"xvdp":      {BusNumber: 0, TargetID: 15, LUN: 0},
	"xvdq":      {BusNumber: 0, TargetID: 16, LUN: 0},
	"xvdr":      {BusNumber: 0, TargetID: 17, LUN: 0},
	"xvds":      {BusNumber: 0, TargetID: 18, LUN: 0},
	"xvdt":      {BusNumber: 0, TargetID: 19, LUN: 0},
	"xvdu":      {BusNumber: 0, TargetID: 20, LUN: 0},
	"xvdv":      {BusNumber: 0, TargetID: 21, LUN: 0},
	"xvdw":      {BusNumber: 0, TargetID: 22, LUN: 0},
	"xvdx":      {BusNumber: 0, TargetID: 23, LUN: 0},
	"xvdy":      {BusNumber: 0, TargetID: 24, LUN: 0},
	"xvdz":      {BusNumber: 0, TargetID: 25, LUN: 0},
}
