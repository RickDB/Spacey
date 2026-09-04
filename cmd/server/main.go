package main

import (
	"context"
	"log"
	"net/http"
	"os/signal"
	"spacey/internal/api"
	"spacey/internal/config"
	"spacey/internal/database"
	"syscall"
	"time"
)

func main() {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, e := database.Open(ctx, cfg.DatabaseURL)
	if e != nil {
		log.Fatal(e)
	}
	defer db.Pool.Close()
	host, e := db.EnsureHost(ctx, cfg.Hostname)
	if e != nil {
		log.Fatal(e)
	}
	app := api.New(db, host, cfg.ScanWorkers)
	app.StartDiscovery(ctx)
	srv := &http.Server{Addr: cfg.Listen, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(c)
	}()
	log.Printf("Spacey listening on %s", cfg.Listen)
	if e = srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
