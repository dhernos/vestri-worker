package fs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func safePath(base, userPath string) (string, error) {
	full, err := SafeSubPath(base, userPath)
	if err != nil {
		return "", err
	}
	if err := validatePathNoSymlink(base, full); err != nil {
		return "", err
	}
	return full, nil
}

func SafeSubPath(base, user string) (string, error) {
	cleanBase, err := filepath.Abs(base)
	if err != nil {
		return "", err
	}
	full := filepath.Clean(filepath.Join(cleanBase, user))

	if cleanBase == string(filepath.Separator) {
		return full, nil
	}

	if !strings.HasPrefix(full, cleanBase+string(filepath.Separator)) && full != cleanBase {
		return "", errors.New("path escape detected")
	}

	return full, nil
}

func validatePathNoSymlink(base, full string) error {
	cleanBase, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	cleanFull, err := filepath.Abs(full)
	if err != nil {
		return err
	}

	if cleanBase != string(filepath.Separator) {
		if !strings.HasPrefix(cleanFull, cleanBase+string(filepath.Separator)) && cleanFull != cleanBase {
			return errors.New("path escape detected")
		}
	}

	rel, err := filepath.Rel(cleanBase, cleanFull)
	if err != nil {
		return err
	}
	if rel == "." {
		return nil
	}

	parts := strings.Split(rel, string(filepath.Separator))
	cur := cleanBase
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("path contains symlink")
		}
	}

	return nil
}
