package server

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/heptiolabs/healthcheck"
	"github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/spf13/viper"
	"github.com/ziflex/lecho/v3"

	"github.com/zapier/kubechecks/pkg/app_watcher"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/vcs"
)

const KubeChecksHooksPathPrefix = "/hooks"

type Server struct {
	cfg        *config.ServerConfig
	appWatcher *app_watcher.ApplicationWatcher
}

func NewServer(ctx context.Context, cfg *config.ServerConfig) *Server {
	var appWatcher *app_watcher.ApplicationWatcher
	if viper.GetBool("monitor-all-applications") {
		argoMap, err := config.BuildAppsMap(ctx)
		if err != nil {
			log.Fatal().Err(err).Msg("could not build VcsToArgoMap")
		}
		cfg.VcsToArgoMap = argoMap

		appWatcher, err = app_watcher.NewApplicationWatcher(cfg)
		if err != nil {
			log.Fatal().Err(err).Msg("could not create ApplicationWatcher")
		}
	} else {
		cfg.VcsToArgoMap = config.NewVcsToArgoMap()
	}

	return &Server{cfg: cfg, appWatcher: appWatcher}
}

func (s *Server) Start(ctx context.Context) {
	if s.appWatcher != nil {
		go s.appWatcher.Run(ctx, 1)
	}

	if err := s.ensureWebhooks(ctx); err != nil {
		log.Warn().Err(err).Msg("failed to create webhooks")
	}

	e := echo.New()
	e.HideBanner = true
	e.Use(middleware.Recover())
	e.Logger = lecho.New(log.Logger)
	// Enable metrics middleware
	p := prometheus.NewPrometheus("kubechecks_echo", nil)
	p.Use(e)

	// add routes
	health := healthcheck.NewHandler()
	e.GET("/ready", echo.WrapHandler(health))
	e.GET("/live", echo.WrapHandler(health))

	hooksGroup := e.Group(s.hooksPrefix())

	ghHooks := NewVCSHookHandler(s.cfg)
	ghHooks.AttachHandlers(hooksGroup)

	fmt.Println("Method\tPath")
	for _, r := range e.Routes() {
		fmt.Printf("%s\t%s\n", r.Method, r.Path)
	}

	if err := e.Start(":8080"); err != nil {
		log.Fatal().Err(err).Msg("could not start hooks server")
	}
}

func (s *Server) hooksPrefix() string {
	prefix := s.cfg.UrlPrefix
	serverUrl, err := url.JoinPath("/", prefix, KubeChecksHooksPathPrefix)
	if err != nil {
		log.Warn().Err(err).Msg(":whatintarnation:")
	}

	return strings.TrimSuffix(serverUrl, "/")
}

func (s *Server) ensureWebhooks(ctx context.Context) error {
	if !viper.GetBool("ensure-webhooks") {
		return nil
	}

	if !viper.GetBool("monitor-all-applications") {
		return errors.New("must enable 'monitor-all-applications' to create webhooks")
	}

	urlBase := viper.GetString("webhook-url-base")
	if urlBase == "" {
		return errors.New("must define 'webhook-url-base' to create webhooks")
	}

	log.Info().Msg("ensuring all webhooks are created correctly")

	vcsClient := s.cfg.VcsClient

	fullUrl, err := url.JoinPath(urlBase, s.hooksPrefix(), vcsClient.GetName(), "project")
	if err != nil {
		log.Warn().Str("urlBase", urlBase).Msg("failed to create a webhook url")
		return errors.Wrap(err, "failed to create a webhook url")
	}
	log.Info().Str("webhookUrl", fullUrl).Msg("webhook URL for this kubechecks instance")

	for _, repo := range s.cfg.GetVcsRepos() {
		wh, err := vcsClient.GetHookByUrl(ctx, repo, fullUrl)
		if err != nil && !errors.Is(err, vcs.ErrHookNotFound) {
			log.Error().Err(err).Msgf("failed to get hook for %s:", repo)
			continue
		}

		if wh == nil {
			if err = vcsClient.CreateHook(ctx, repo, fullUrl, s.cfg.WebhookSecret); err != nil {
				log.Info().Err(err).Msgf("failed to create hook for %s:", repo)
			}
		}
	}

	return nil
}
