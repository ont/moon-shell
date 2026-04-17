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

	"github.com/alecthomas/kong"
	"github.com/labstack/echo/v4"
	"moon-shell/pkg/commandexec"
	"moon-shell/pkg/gogmoon"
)

type cli struct {
	ConfigPath string `name:"config" help:"Path to YAML config file." default:"gog-config.yml"`
}

func main() {
	var cliCfg cli
	kong.Parse(&cliCfg,
		kong.Name("moon-shell-gog"),
		kong.Description("Gmail command runner backed by the gog CLI utility."),
		kong.UsageOnError(),
	)

	cfg, err := gogmoon.LoadConfig(cliCfg.ConfigPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
	gog := gogmoon.NewGog(cfg.Gog)
	store, err := commandexec.NewStore(executionDBPath(cliCfg.ConfigPath))
	if err != nil {
		log.Fatalf("open execution store: %v", err)
	}
	runner := commandexec.NewRunner(cfg.ServiceConfig())
	fetcher := gogmoon.NewFetcher(cfg.Gog, gog)
	responder := gogmoon.NewResponder(gog)
	service := gogmoon.NewService(cfg, fetcher, runner, store, responder, gog, logger)
	server := newHTTPServer(service)

	if err := run(rootCtx, cfg, logger, service, server); err != nil {
		log.Fatalf("run app: %v", err)
	}
}

func executionDBPath(configPath string) string {
	if configPath == "" {
		configPath = gogmoon.DefaultConfigPath
	}
	return configPath + ".exec.db"
}

func newHTTPServer(service *gogmoon.Service) *echo.Echo {
	server := echo.New()
	server.HideBanner = true
	server.HidePort = true
	server.Server.ReadHeaderTimeout = 5 * time.Second

	server.GET("/", func(c echo.Context) error {
		status := service.Snapshot()
		if status.LastError != "" {
			return c.JSON(http.StatusServiceUnavailable, status)
		}
		return c.JSON(http.StatusOK, status)
	})
	server.GET("/healthz", func(c echo.Context) error {
		status := service.Snapshot()
		if status.LastError != "" {
			return c.String(http.StatusServiceUnavailable, status.LastError+"\n")
		}
		return c.String(http.StatusOK, "ok\n")
	})
	return server
}

func run(ctx context.Context, cfg gogmoon.Config, logger *log.Logger, service *gogmoon.Service, server *echo.Echo) error {
	go service.Run(ctx)

	serverErr := make(chan error, 1)
	go func() {
		logger.Printf("http server listening on %s", cfg.ListenAddr)
		if err := server.Start(cfg.ListenAddr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case <-ctx.Done():
		logger.Printf("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			return err
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return server.Shutdown(shutdownCtx)
}
