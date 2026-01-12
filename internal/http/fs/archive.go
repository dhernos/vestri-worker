package fs

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"vestri-worker/internal/settings"
)

type archiveRequest struct {
	Source string `json:"source"`
	Dest   string `json:"dest"`
}

func ZipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveRequestBytes())
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Source == "" || req.Dest == "" {
		http.Error(w, "missing source or dest", http.StatusBadRequest)
		return
	}

	base := settings.Get().FsBasePath
	sourcePath, err := safePath(base, req.Source)
	if err != nil {
		logArchiveOpError(r, "zip", req.Source, req.Dest, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	destPath, err := safePath(base, req.Dest)
	if err != nil {
		logArchiveOpError(r, "zip", req.Source, req.Dest, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		logArchiveOpError(r, "zip", req.Source, req.Dest, err)
		http.Error(w, "source not found", http.StatusNotFound)
		return
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 {
		logArchiveOpError(r, "zip", req.Source, req.Dest, fmt.Errorf("source is a symlink"))
		http.Error(w, "source is a symlink", http.StatusBadRequest)
		return
	}
	cleanSource := filepath.Clean(sourcePath)
	cleanDest := filepath.Clean(destPath)
	if cleanDest == cleanSource {
		logArchiveOpError(r, "zip", req.Source, req.Dest, fmt.Errorf("destination equals source"))
		http.Error(w, "destination must differ from source", http.StatusBadRequest)
		return
	}
	if sourceInfo.IsDir() && strings.HasPrefix(cleanDest, cleanSource+string(filepath.Separator)) {
		logArchiveOpError(r, "zip", req.Source, req.Dest, fmt.Errorf("destination inside source"))
		http.Error(w, "destination must be outside the source directory", http.StatusBadRequest)
		return
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		logArchiveOpError(r, "zip", req.Source, req.Dest, err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return
	}

	if err := zipPath(sourcePath, destPath, sourceInfo); err != nil {
		logArchiveOpError(r, "zip", req.Source, req.Dest, err)
		http.Error(w, "cannot create zip", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	logArchiveOp(r, "zip", req.Source, req.Dest)
}

func UnzipHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxArchiveRequestBytes())
	var req archiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}
	if req.Source == "" || req.Dest == "" {
		http.Error(w, "missing source or dest", http.StatusBadRequest)
		return
	}

	base := settings.Get().FsBasePath
	sourcePath, err := safePath(base, req.Source)
	if err != nil {
		logArchiveOpError(r, "unzip", req.Source, req.Dest, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	destPath, err := safePath(base, req.Dest)
	if err != nil {
		logArchiveOpError(r, "unzip", req.Source, req.Dest, err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	sourceInfo, err := os.Lstat(sourcePath)
	if err != nil {
		logArchiveOpError(r, "unzip", req.Source, req.Dest, err)
		http.Error(w, "source not found", http.StatusNotFound)
		return
	}
	if sourceInfo.Mode()&os.ModeSymlink != 0 {
		logArchiveOpError(r, "unzip", req.Source, req.Dest, fmt.Errorf("source is a symlink"))
		http.Error(w, "source is a symlink", http.StatusBadRequest)
		return
	}
	if sourceInfo.IsDir() {
		logArchiveOpError(r, "unzip", req.Source, req.Dest, fmt.Errorf("source is a directory"))
		http.Error(w, "source is a directory", http.StatusBadRequest)
		return
	}

	if err := ensureDir(destPath); err != nil {
		logArchiveOpError(r, "unzip", req.Source, req.Dest, err)
		http.Error(w, "cannot create directories", http.StatusInternalServerError)
		return
	}

	if err := unzipPath(sourcePath, destPath); err != nil {
		logArchiveOpError(r, "unzip", req.Source, req.Dest, err)
		http.Error(w, "cannot unzip archive", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	logArchiveOp(r, "unzip", req.Source, req.Dest)
}

func ensureDir(path string) error {
	info, err := os.Stat(path)
	if err == nil {
		if !info.IsDir() {
			return fmt.Errorf("destination is not a directory")
		}
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	return os.MkdirAll(path, 0755)
}

func zipPath(sourcePath, destPath string, sourceInfo os.FileInfo) error {
	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	zw := zip.NewWriter(out)
	defer zw.Close()

	if sourceInfo.IsDir() {
		return zipDir(zw, sourcePath, sourceInfo)
	}

	return zipFile(zw, sourcePath, filepath.Base(sourcePath), sourceInfo)
}

func zipDir(zw *zip.Writer, dirPath string, dirInfo os.FileInfo) error {
	baseName := filepath.Base(dirPath)
	if err := addZipDir(zw, baseName, dirInfo); err != nil {
		return err
	}

	return filepath.WalkDir(dirPath, func(entryPath string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entryPath == dirPath {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks not supported")
		}

		rel, err := filepath.Rel(dirPath, entryPath)
		if err != nil {
			return err
		}
		zipName := filepath.ToSlash(filepath.Join(baseName, rel))

		info, err := entry.Info()
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return addZipDir(zw, zipName, info)
		}
		return zipFile(zw, entryPath, zipName, info)
	})
}

func zipFile(zw *zip.Writer, filePath, zipName string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("symlinks not supported")
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = filepath.ToSlash(zipName)
	header.Method = zip.Deflate

	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	_, err = io.Copy(writer, file)
	return err
}

func addZipDir(zw *zip.Writer, zipName string, info os.FileInfo) error {
	name := filepath.ToSlash(zipName)
	if !strings.HasSuffix(name, "/") {
		name += "/"
	}

	header, err := zip.FileInfoHeader(info)
	if err != nil {
		return err
	}
	header.Name = name
	header.SetMode(info.Mode() | os.ModeDir)

	_, err = zw.CreateHeader(header)
	return err
}

func copyWithLimit(dst io.Writer, src io.Reader, limit int64) (int64, error) {
	if limit <= 0 {
		return 0, fmt.Errorf("archive exceeds size limit")
	}

	limited := io.LimitReader(src, limit)
	written, err := io.Copy(dst, limited)
	if err != nil {
		return written, err
	}
	if written == limit {
		var buf [1]byte
		if n, readErr := src.Read(buf[:]); readErr != io.EOF || n > 0 {
			return written, fmt.Errorf("archive exceeds size limit")
		}
	}

	return written, nil
}

func unzipPath(zipPath, destDir string) error {
	reader, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer reader.Close()

	var total uint64
	entries := 0

	for _, file := range reader.File {
		entries++
		if entries > maxZipEntries() {
			return fmt.Errorf("zip has too many entries")
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlinks not supported")
		}

		cleanName := path.Clean(file.Name)
		if cleanName == "." {
			continue
		}
		if strings.HasPrefix(cleanName, "../") || cleanName == ".." || path.IsAbs(cleanName) {
			return fmt.Errorf("invalid zip entry: %s", file.Name)
		}

		targetPath, err := SafeSubPath(destDir, filepath.FromSlash(cleanName))
		if err != nil {
			return err
		}
		if err := validatePathNoSymlink(destDir, targetPath); err != nil {
			return err
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(targetPath, 0755); err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
			return err
		}

		dstFile, err := os.OpenFile(targetPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
		if err != nil {
			return err
		}

		srcFile, err := file.Open()
		if err != nil {
			dstFile.Close()
			return err
		}

		limit := maxUnzipBytes()
		if limit <= 0 {
			srcFile.Close()
			dstFile.Close()
			_ = os.Remove(targetPath)
			return fmt.Errorf("archive exceeds size limit")
		}
		limitU := uint64(limit)
		if total >= limitU {
			srcFile.Close()
			dstFile.Close()
			_ = os.Remove(targetPath)
			return fmt.Errorf("archive exceeds size limit")
		}
		remaining := int64(limitU - total)
		if file.UncompressedSize64 > 0 && file.UncompressedSize64 > limitU-total {
			srcFile.Close()
			dstFile.Close()
			_ = os.Remove(targetPath)
			return fmt.Errorf("archive exceeds size limit")
		}

		written, err := copyWithLimit(dstFile, srcFile, remaining)
		total += uint64(written)
		if err != nil {
			srcFile.Close()
			dstFile.Close()
			_ = os.Remove(targetPath)
			return err
		}

		srcFile.Close()
		if err := dstFile.Close(); err != nil {
			return err
		}
	}

	return nil
}
