package settings

type Settings struct {
	UseTLS bool `json:"useTLS"`
        TLSCert string `json:"TLSCert"`
        TLSKey string `json:"TLSKey"`
	HTTPPort  string `json:"http_port"`
	WorkerName string `json:"worker_name"`
	LogLevel   string `json:"log_level"`
	MaxJobs    int    `json:"max_jobs"`
	FsBasePath string `json:"fs_base_path"`
}

func Default() Settings {
	return Settings{
		UseTLS: false,
                TLSCert:"",
                TLSKey:"",
		HTTPPort:  ":8031",
		FsBasePath: "/tmp/vestri",
	}
}
