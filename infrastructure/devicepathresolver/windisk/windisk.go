package windisk

// I would rather this live in windisk_windows.go, as it is where it belongs,
// but that would require redefining it in windisk_unix.go, which only exists
// to make Ginkgo and other tools happy - so placing it here is the compromise.

// A Disk represents the parsed output of 'WMIC.exe DISKDRIVE LIST FULL' for a
// single listed disk.  The types come from the XML output of the previously
// mentioned WMIC.exe command.
type Disk struct {
	Availability                uint16
	BytesPerSector              uint32
	Capabilities                []uint16
	CapabilityDescriptions      []string
	CompressionMethod           string
	ConfigManagerErrorCode      uint32
	ConfigManagerUserConfig     bool
	DefaultBlockSize            uint64
	Description                 string
	DeviceID                    string
	ErrorCleared                bool
	ErrorDescription            string
	ErrorMethodology            string
	Index                       uint32
	InstallDate                 string // No idea what date format is used
	InterfaceType               string
	LastErrorCode               uint32
	Manufacturer                string
	MaxBlockSize                uint64
	MaxMediaSize                uint64
	MediaLoaded                 bool
	MediaType                   string
	MinBlockSize                uint64
	Model                       string
	Name                        string
	NeedsCleaning               bool
	NumberOfMediaSupported      uint32
	Partitions                  uint32
	PNPDeviceID                 string
	PowerManagementCapabilities uint16
	PowerManagementSupported    bool
	SCSIBus                     uint32
	SCSILogicalUnit             uint16
	SCSIPort                    uint16
	SCSITargetId                uint16
	SectorsPerTrack             uint32
	Signature                   uint32
	Size                        uint64
	Status                      string
	StatusInfo                  uint16
	SystemName                  string
	TotalCylinders              uint64
	TotalHeads                  uint32
	TotalSectors                uint64
	TotalTracks                 uint64
	TracksPerCylinder           uint32
}
