// +build !windows

package windisk

// This file exists solely to make Ginkgo and other tools happy.

// List returns a list of the disks physically attached to the Windows machine.
// On non-Windows OSs List panics.
func List() ([]Disk, error) {
	panic("NOT IMPLEMENTED") // That's right
}
