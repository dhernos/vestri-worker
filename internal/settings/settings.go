package settings

type Settings struct {
	HTTPPort  string `json:"http_port"`
	WorkerName string `json:"worker_name"`
	LogLevel   string `json:"log_level"`
	MaxJobs    int    `json:"max_jobs"`
	FsBasePath string `json:"fs_base_path"`
}

func Default() Settings {
	return Settings{
		HTTPPort:  ":8031",
		FsBasePath: "/tmp/vestri",
	}
}