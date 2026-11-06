package fs

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"vestri-worker/internal/settings"
)

func TransferEntryHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveRequestBytes())
	var request struct {
		Source    string `json:"source"`
		Dest      string `json:"dest"`
		Operation string `json:"operation"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	err := transferEntry(settings.Get().FsBasePath, request.Source, request.Dest, request.Operation)
	if err != nil {
		statusCode := http.StatusBadRequest
		if errors.Is(err, os.ErrExist) {
			statusCode = http.StatusConflict
		} else if errors.Is(err, os.ErrNotExist) {
			statusCode = http.StatusNotFound
		}
		logPathOpError(r, request.Operation, request.Source, err)
		http.Error(w, err.Error(), statusCode)
		return
	}
	logPathOp(r, request.Operation, request.Dest)
	w.WriteHeader(http.StatusOK)
}

func transferEntry(base, source, dest, operation string) error {
	if source == "" || dest == "" || (operation != "copy" && operation != "move" && operation != "rename") {
		return errors.New("source, destination and a valid operation are required")
	}
	sourcePath, err := safePath(base, source)
	if err != nil {
		return err
	}
	destPath, err := safePath(base, dest)
	if err != nil {
		return err
	}
	basePath, err := filepath.Abs(base)
	if err != nil {
		return err
	}
	if sourcePath == basePath || destPath == basePath || sourcePath == destPath || strings.HasPrefix(destPath, sourcePath+string(filepath.Separator)) {
		return errors.New("cannot transfer the root, an entry onto itself, or a folder into itself")
	}
	if _, err := os.Lstat(destPath); err == nil {
		return os.ErrExist
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := filepath.Walk(sourcePath, func(current string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return errors.New("only regular files and directories can be transferred")
		}
		return validatePathNoSymlink(basePath, current)
	}); err != nil {
		return err
	}
	parentInfo, err := os.Stat(filepath.Dir(destPath))
	if err != nil {
		return err
	}
	if !parentInfo.IsDir() {
		return errors.New("destination parent must be a directory")
	}
	if operation != "copy" {
		return os.Rename(sourcePath, destPath)
	}
	return copyEntry(sourcePath, destPath)
}

func copyEntry(sourcePath, destPath string) error {
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.Mkdir(destPath, info.Mode().Perm()|0700); err != nil {
			return err
		}
		entries, err := os.ReadDir(sourcePath)
		if err == nil {
			for _, entry := range entries {
				err = copyEntry(filepath.Join(sourcePath, entry.Name()), filepath.Join(destPath, entry.Name()))
				if err != nil {
					break
				}
			}
		}
		if err == nil {
			err = os.Chmod(destPath, info.Mode().Perm())
		}
		if err != nil {
			_ = os.RemoveAll(destPath)
		}
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("only regular files can be copied")
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()
	dest, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(dest, source)
	closeErr := dest.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(destPath)
		return errors.Join(copyErr, closeErr)
	}
	return nil
}
