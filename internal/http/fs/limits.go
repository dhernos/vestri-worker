package fs

import "vestri-worker/internal/settings"

const (
	defaultMaxArchiveRequestBytes int64 = 1 << 20  // 1 MiB
	defaultMaxInlineWriteBytes    int64 = 10 << 20 // 10 MiB
	defaultMaxUploadBytes         int64 = 1 << 30  // 1 GiB
	defaultMaxUnzipBytes          int64 = 10 << 30 // 10 GiB
	defaultMaxZipEntries          int   = 100000
)

func maxArchiveRequestBytes() int64 {
	if v := settings.Get().MaxArchiveRequestBytes; v > 0 {
		return v
	}
	return defaultMaxArchiveRequestBytes
}

func maxInlineWriteBytes() int64 {
	if v := settings.Get().MaxInlineWriteBytes; v > 0 {
		return v
	}
	return defaultMaxInlineWriteBytes
}

func maxUploadBytes() int64 {
	if v := settings.Get().MaxUploadBytes; v > 0 {
		return v
	}
	return defaultMaxUploadBytes
}

func maxUnzipBytes() int64 {
	if v := settings.Get().MaxUnzipBytes; v > 0 {
		return v
	}
	return defaultMaxUnzipBytes
}

func maxZipEntries() int {
	if v := settings.Get().MaxZipEntries; v > 0 {
		return v
	}
	return defaultMaxZipEntries
}
