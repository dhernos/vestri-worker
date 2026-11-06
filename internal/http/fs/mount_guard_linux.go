//go:build linux

package fs

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

func pathCrossesMountPoint(base, full string) (bool, error) {
	file, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return false, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		sepIdx := strings.Index(line, " - ")
		if sepIdx < 0 {
			continue
		}

		fields := strings.Fields(line[:sepIdx])
		if len(fields) < 5 {
			continue
		}

		mountPoint := filepath.Clean(unescapeMountInfoPath(fields[4]))
		if mountPoint == base {
			continue
		}
		if !isPathWithin(base, mountPoint) {
			continue
		}
		if isPathWithin(mountPoint, full) {
			return true, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return false, err
	}

	return false, nil
}

func unescapeMountInfoPath(value string) string {
	value = strings.ReplaceAll(value, `\040`, " ")
	value = strings.ReplaceAll(value, `\011`, "\t")
	value = strings.ReplaceAll(value, `\012`, "\n")
	value = strings.ReplaceAll(value, `\134`, `\`)
	return value
}

func isPathWithin(base, target string) bool {
	if base == string(filepath.Separator) {
		return strings.HasPrefix(target, base)
	}
	return target == base || strings.HasPrefix(target, base+string(filepath.Separator))
}
