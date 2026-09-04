package config

import (
	"os"
	"strconv"
)

type Config struct {
	Listen, DatabaseURL, HostPrefix, ContainerPrefix, Hostname string
	ScanWorkers                                                int
}

func Load() Config {
	return Config{
		Listen:          env("SPACEY_LISTEN", ":8080"),
		DatabaseURL:     env("SPACEY_DATABASE_URL", "postgres://spacey:spacey@localhost:5432/spacey?sslmode=disable"),
		HostPrefix:      env("SPACEY_HOST_PREFIX", "/"),
		ContainerPrefix: env("SPACEY_CONTAINER_PREFIX", ""),
		Hostname:        env("SPACEY_HOSTNAME", "local"),
		ScanWorkers:     envInt("SPACEY_SCAN_WORKERS", 8),
	}
}

func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
func envInt(k string, fallback int) int {
	v, err := strconv.Atoi(os.Getenv(k))
	if err == nil && v > 0 {
		return v
	}
	return fallback
}
