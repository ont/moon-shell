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

	"cloud.google.com/go/pubsub"
	"github.com/alecthomas/kong"
	"github.com/defval/di"
	"github.com/labstack/echo/v4"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
	"moon-shell/pkg/auth"
	"moon-shell/pkg/commandexec"
	appconfig "moon-shell/pkg/config"
	gmailsvc "moon-shell/pkg/gmail"
	gmailconfig "moon-shell/pkg/gmail/config"
	"moon-shell/pkg/gmail/fetcher"
	"moon-shell/pkg/gmail/responder"
	"moon-shell/pkg/gmail/watcher"
)

type cli struct {
	ConfigPath string   `name:"config" help:"Path to YAML config file." default:"config.yml"`
	Serve      serveCmd `cmd:"" default:"1" hidden:"" help:"Run the server."`
	Auth       authCLI  `cmd:"" help:"OAuth helper commands."`
}

type serveCmd struct{}

type authCLI struct {
	Init authInitCmd `cmd:"" help:"Run browser-based OAuth setup and store the refresh token locally."`
}

type authInitCmd struct{}

func runAuthInit(cliCfg cli) error {
	cfg, err := appconfig.Load(cliCfg.ConfigPath)
	if err != nil {
		return err
	}
	if cfg.Gmail.ClientID == "" || cfg.Gmail.ClientSecret == "" {
		return errors.New("gmail.client_id and gmail.client_secret must be set in config before running auth init")
	}

	conf := newOAuthConfig(cfg.Gmail, "")
	store := auth.Store{Path: auth.TokenPath(cliCfg.ConfigPath)}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	result, err := auth.Init(ctx, conf, store, func(url string) {
		log.Printf("Open this URL in your browser and complete consent:\n%s", url)
		log.Printf("Waiting for OAuth callback on a temporary local server")
	})
	if err != nil {
		return err
	}

	log.Printf("OAuth token saved to %s", result.TokenPath)
	return nil
}

func main() {
	var cliCfg cli
	ctx := kong.Parse(&cliCfg,
		kong.Name("moon-shell"),
		kong.Description("Simple Gmail draft fetcher with watch support and an HTTP health endpoint."),
		kong.UsageOnError(),
	)
	if ctx.Command() == "auth init" {
		if err := runAuthInit(cliCfg); err != nil {
			log.Fatalf("auth init: %v", err)
		}
		return
	}

	cfg, err := appconfig.Load(cliCfg.ConfigPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("invalid config: %v", err)
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	container, err := di.New(
		di.ProvideValue(rootCtx, di.As(new(context.Context))),
		di.ProvideValue(cfg),
		di.ProvideValue(cfg.Gmail),
		di.ProvideValue(appconfig.GmailServiceConfig(cfg)),
		di.ProvideValue(auth.Store{Path: auth.TokenPath(cliCfg.ConfigPath)}),
		di.ProvideValue(executionDBPath(cliCfg.ConfigPath)),
		di.Provide(newLogger),
		di.Provide(newOAuthToken),
		di.Provide(newGmailAPIService),
		di.Provide(newPubSubClient),
		di.Provide(commandexec.NewStore),
		di.Provide(commandexec.NewRunner),
		di.Provide(fetcher.New),
		di.Provide(responder.NewResponder),
		di.Provide(watcher.New),
		di.Provide(gmailsvc.NewService),
		di.Provide(newHTTPServer),
	)
	if err != nil {
		log.Fatalf("build container: %v", err)
	}

	if err := container.Invoke(runApp); err != nil {
		log.Fatalf("run app: %v", err)
	}
}

func newLogger() *log.Logger {
	return log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)
}

func executionDBPath(configPath string) string {
	if configPath == "" {
		configPath = appconfig.DefaultPath
	}
	return configPath + ".exec.db"
}

func newOAuthConfig(cfg gmailconfig.Config, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		RedirectURL:  redirectURL,
		Endpoint:     google.Endpoint,
		Scopes: []string{
			gmailapi.GmailReadonlyScope,
			gmailapi.GmailSendScope,
		},
	}
}

func newOAuthToken(store auth.Store) (*oauth2.Token, error) {
	return store.Load()
}

func newGmailAPIService(ctx context.Context, cfg gmailconfig.Config, token *oauth2.Token) (*gmailapi.Service, error) {
	oauthCfg := newOAuthConfig(cfg, "")

	client := oauthCfg.Client(ctx, token)

	svc, err := gmailapi.NewService(ctx, option.WithHTTPClient(client))
	if err != nil {
		return nil, err
	}

	return svc, nil
}

func newPubSubClient(ctx context.Context, cfg gmailconfig.Config) (*pubsub.Client, error) {
	if !cfg.Watch.Enabled {
		return nil, nil
	}

	if cfg.Watch.CredentialsFile != "" {
		return pubsub.NewClient(ctx, cfg.Watch.ProjectID, option.WithCredentialsFile(cfg.Watch.CredentialsFile))
	}

	return pubsub.NewClient(ctx, cfg.Watch.ProjectID)
}

func newHTTPServer(service *gmailsvc.Service) *echo.Echo {
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

func runApp(ctx context.Context, cfg appconfig.Config, logger *log.Logger, service *gmailsvc.Service, server *echo.Echo) error {
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
