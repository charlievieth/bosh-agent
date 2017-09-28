package windisk

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
)

func parseBool(s string, v reflect.Value) error {
	b, err := strconv.ParseBool(s)
	if err == nil {
		v.Set(reflect.ValueOf(b))
	}
	return err
}

func parseString(s string, v reflect.Value) error {
	s = strings.TrimFunc(s, func(r rune) bool {
		return r == ' ' || r == '"'
	})
	v.Set(reflect.ValueOf(s))
	return nil
}

func parseUint16(s string, v reflect.Value) error {
	n, err := strconv.ParseInt(s, 10, 16)
	if err == nil {
		v.Set(reflect.ValueOf(uint16(n)))
	}
	return err
}

func parseUint32(s string, v reflect.Value) error {
	n, err := strconv.ParseInt(s, 10, 32)
	if err == nil {
		v.Set(reflect.ValueOf(uint32(n)))
	}
	return err
}

func parseUint64(s string, v reflect.Value) error {
	n, err := strconv.ParseInt(s, 10, 64)
	if err == nil {
		v.Set(reflect.ValueOf(uint64(n)))
	}
	return err
}

func setSlice(str string, v reflect.Value, fn func(string, reflect.Value) error) error {
	str = strings.TrimFunc(str, func(r rune) bool {
		return r == '{' || r == '}' || r == ' '
	})
	elems := strings.Split(str, ",")
	slice := reflect.MakeSlice(v.Type(), len(elems), len(elems))
	for i, elem := range elems {
		if err := fn(elem, slice.Index(i)); err != nil {
			return err
		}
	}
	v.Set(slice)
	return nil
}

func parseStringSlice(str string, v reflect.Value) error {
	return setSlice(str, v, parseString)
}

func parseUint16Slice(str string, v reflect.Value) error {
	return setSlice(str, v, parseUint16)
}

func parseField(s string, v reflect.Value) error {
	switch v.Kind() {
	case reflect.Bool:
		if err := parseBool(s, v); err != nil {
			return err
		}
	case reflect.String:
		if err := parseString(s, v); err != nil {
			return err
		}
	case reflect.Uint16:
		if err := parseUint16(s, v); err != nil {
			return err
		}
	case reflect.Uint32:
		if err := parseUint32(s, v); err != nil {
			return err
		}
	case reflect.Uint64:
		if err := parseUint64(s, v); err != nil {
			return err
		}
	case reflect.Slice:
		switch v.Type().Elem().Kind() {
		case reflect.String:
			if err := parseStringSlice(s, v); err != nil {
				return err
			}
		case reflect.Uint16:
			if err := parseUint16Slice(s, v); err != nil {
				return err
			}
		default:
			return errors.New("invalid slice kind: " +
				v.Type().Elem().Kind().String())
		}
	default:
		return errors.New("invalid kind: " + v.Kind().String())
	}
	return nil
}

// Use reflection to parse m into Disk d.
//
// NOTE: Values not present in m are represented by their default value in
// d - so know whats populated before using.  Running the WMIC.exe command
// used to generate m would be a good first step.
//
func parseDisk(m map[string]string, d *Disk) error {
	v := reflect.Indirect(reflect.ValueOf(d))
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		s, ok := m[t.Field(i).Name]
		if !ok {
			continue
		}
		fv := v.Field(i)
		if err := parseField(s, fv); err != nil {
			return err
		}
	}
	return nil
}

func parseDiskDrive(drive string) (map[string]string, error) {
	var m map[string]string
	lines := strings.Split(drive, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		n := strings.IndexByte(line, '=')
		if n < 1 {
			continue
		}
		if n == len(line)-1 {
			continue
		}
		if m == nil {
			m = make(map[string]string, len(lines))
		}
		m[line[:n]] = line[n+1:]
	}
	if len(m) != 0 {
		return m, nil
	}
	return nil, nil
}

// Carriage returns are annoying - get rid of them!
func replaceCR(b []byte) []byte {
	n := 0
	for _, c := range b {
		if c != '\r' {
			b[n] = c
			n++
		}
	}
	return b[:n]
}

func listDiskDrives() ([]map[string]string, error) {
	cmd := exec.Command("WMIC.exe", "DISKDRIVE", "LIST", "FULL", "/FORMAT:textvaluelist.xsl")
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("WMIC.exe: %s", err)
	}

	a := strings.Split(string(replaceCR(stdout.Bytes())), "\n\n\n")
	if len(a) == 0 {
		return nil, errors.New("WMIC.exe: no output")
	}

	disks := make([]map[string]string, 0, len(a))
	for _, b := range a {
		b = strings.TrimSpace(b)
		if len(b) == 0 {
			continue
		}
		m, err := parseDiskDrive(b)
		if err != nil {
			return nil, err
		}
		if len(m) > 0 {
			disks = append(disks, m)
		}
	}
	return disks, nil
}

// List returns a list of the disks physically attached to the Windows machine.
// On non-Windows OSs List panics.
func List() ([]Disk, error) {
	// This could be improved by directly translating the output of WMIC.exe
	// to disk structures (skipping the []map[string]string intermediary step),
	// but would gain us little - as calling WMIC.exe accounts for almost all
	// of the overhead.

	list, err := listDiskDrives()
	if err != nil {
		return nil, err
	}
	disks := make([]Disk, 0, len(list))
	for _, m := range list {
		var d Disk
		if err := parseDisk(m, &d); err != nil {
			return nil, err
		}
		disks = append(disks, d)
	}
	return disks, nil
}
