// Command v2v-web serves the plain web client of the v2v engine and forwards
// its /v1 calls to the engine's end-user API.
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/valpere/v2v-web"
	"github.com/valpere/v2v-web/internal/server"
)

func main() {
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	cfg := server.Config{
		EngineURL:    os.Getenv("ENGINE_URL"),
		EngineKey:    os.Getenv("ENGINE_KEY"),
		WebDir:       os.Getenv("WEB_DIR"),
		TrustProxy:   os.Getenv("TRUST_PROXY") == "1",
		PublicOrigin: os.Getenv("PUBLIC_ORIGIN"),
	}
	if cfg.WebDir == "" { // production: the client embedded in this binary
		cfg.Static = v2vweb.Client()
	}
	h, err := server.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr: env("ADDR", "127.0.0.1:8086"), Handler: h,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second,
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("v2v-web: listening on http://%s, engine %s", srv.Addr, os.Getenv("ENGINE_URL"))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
