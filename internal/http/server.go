package http

import (
	"log"
	"net/http"

	"vestri-worker/internal/http/fs"
	"vestri-worker/internal/http/stack"
)

func router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/databases", databaseHandler)
	mux.HandleFunc("/fs/read", fs.ReadFileHandler)
	mux.HandleFunc("/fs/write", fs.WriteFileHandler)
	mux.HandleFunc("/fs/mkdir", fs.MkdirHandler)
	mux.HandleFunc("/fs/copy", fs.CopyFileHandler)
	mux.HandleFunc("/fs/transfer", fs.TransferEntryHandler)
	mux.HandleFunc("/fs/list", fs.ListDirHandler)
	mux.HandleFunc("/fs/upload", fs.UploadFileHandler)
	mux.HandleFunc("/fs/fetch", fs.FetchRemoteFileHandler)
	mux.HandleFunc("/fs/download", fs.DownloadFileHandler)
	mux.HandleFunc("/fs/delete", fs.DeleteHandler)
	mux.HandleFunc("/fs/zip", fs.ZipHandler)
	mux.HandleFunc("/fs/unzip", fs.UnzipHandler)
	mux.HandleFunc("/backup", backupStateHandler)
	mux.HandleFunc("/backup/settings", backupSettingsHandler)
	mux.HandleFunc("/backup/create", backupCreateHandler)
	mux.HandleFunc("/backup/delete", backupDeleteHandler)
	mux.HandleFunc("/backup/restore", backupRestoreHandler)
	mux.HandleFunc("/backup/download", backupDownloadHandler)
	mux.HandleFunc("/backup/upload", backupUploadHandler)
	mux.HandleFunc("/stack/up", stack.StackUpHandler)
	mux.HandleFunc("/stack/down", stack.StackDownHandler)
	mux.HandleFunc("/stack/pull", stack.StackPullHandler)
	mux.HandleFunc("/stack/restart", stack.StackRestartHandler)
	mux.HandleFunc("/stack/velocity/reload", stack.StackVelocityReloadHandler)
	mux.HandleFunc("/stack/status", stack.StackStatusHandler)
	mux.HandleFunc("/stack/bluemap/status", stack.BlueMapHandler)
	mux.HandleFunc("/stack/bluemap/view/", stack.BlueMapHandler)
	mux.HandleFunc("/stack/image-status", stack.StackImageStatusHandler)
	mux.HandleFunc("/stack/logs/stream", stack.StackLogsStreamHandler)
	mux.HandleFunc("/stack/logs/ws", stack.StackLogsWebSocketHandler)
	mux.HandleFunc("/stack/exec/ws", stack.StackExecWebSocketHandler)
	mux.HandleFunc("/stack/exec/session", stack.StackExecSessionHandler)
	mux.HandleFunc("/stack/exec/stream", stack.StackExecStreamHandler)
	mux.HandleFunc("/stack/exec/input", stack.StackExecInputHandler)
	mux.HandleFunc("/stack/exec/resize", stack.StackExecResizeHandler)
	return withMiddlewares(mux)
}

func Start(addr string) error {
	startScheduler()
	log.Printf("listening on %s (no TLS)\n", addr)
	return http.ListenAndServe(addr, router())
}

func StartTLS(addr, certFile, keyFile string) error {
	startScheduler()
	log.Printf("listening on %s (TLS)\n", addr)
	return http.ListenAndServeTLS(addr, certFile, keyFile, router())
}
