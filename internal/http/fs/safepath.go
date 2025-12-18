package fs

import (
	"errors"
	"path/filepath"
        "strings"
)

func safePath(base, userPath string) (string, error) {
	clean := filepath.Clean("/" + userPath)

	full := filepath.Join(base, clean)

	if !strings.HasPrefix(full, base) {
		return "", errors.New("invalid path")
	}

	return full, nil
}

func SafeSubPath(base, user string) (string, error) {
	clean := filepath.Clean("/" + user)
	full := filepath.Join(base, clean)

	if !filepath.HasPrefix(full, base+string(filepath.Separator)) &&
		full != base {
		return "", errors.New("path escape detected")
	}

	return full, nil
}
