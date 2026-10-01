package config

import "os"

type Config struct {
	Listen, DatabaseURL, Hostname string
	ScanWorkers                   int
}

func Load() Config {
	return Config{
		Listen:      env("SPACEY_LISTEN", ":8080"),
		DatabaseURL: env("SPACEY_DATABASE_URL", "postgres://spacey:spacey@localhost:5432/spacey?sslmode=disable"),
		Hostname:    env("SPACEY_HOSTNAME", "local"),
		ScanWorkers: 8,
	}
}
func env(k, fallback string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return fallback
}
