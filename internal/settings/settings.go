package settings

type Settings struct {
	UseTLS                 bool    `json:"useTLS"`
	TLSCert                string  `json:"TLSCert"`
	TLSKey                 string  `json:"TLSKey"`
	HTTPPort               string  `json:"http_port"`
	WorkerName             string  `json:"worker_name"`
	LogLevel               string  `json:"log_level"`
	MaxJobs                int     `json:"max_jobs"`
	FsBasePath             string  `json:"fs_base_path"`
	ReplayWindowSeconds    int     `json:"replay_window_seconds"`
	RateLimitRPS           float64 `json:"rate_limit_rps"`
	RateLimitBurst         int     `json:"rate_limit_burst"`
	MaxArchiveRequestBytes int64   `json:"max_archive_request_bytes"`
	MaxInlineWriteBytes    int64   `json:"max_inline_write_bytes"`
	MaxUploadBytes         int64   `json:"max_upload_bytes"`
	MaxUnzipBytes          int64   `json:"max_unzip_bytes"`
	MaxZipEntries          int     `json:"max_zip_entries"`
	RequireTLS             bool    `json:"require_tls"`
	TrustProxyHeaders      bool    `json:"trust_proxy_headers"`
	HealthRequiresAuth     bool    `json:"health_requires_auth"`
}

func Default() Settings {
	return Settings{
		UseTLS:                 false,
		TLSCert:                "",
		TLSKey:                 "",
		HTTPPort:               ":8031",
		FsBasePath:             "/etc/vestri/servers",
		ReplayWindowSeconds:    300,
		RateLimitRPS:           10,
		RateLimitBurst:         20,
		MaxArchiveRequestBytes: 1 << 20,
		MaxInlineWriteBytes:    10 << 20,
		MaxUploadBytes:         1 << 30,
		MaxUnzipBytes:          10 << 30,
		MaxZipEntries:          100000,
		RequireTLS:             false,
		TrustProxyHeaders:      false,
		HealthRequiresAuth:     false,
	}
}
