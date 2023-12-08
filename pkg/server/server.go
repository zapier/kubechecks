package server

import (
	"context"
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

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/argo_client"
)

const KubeChecksHooksPathPrefix = "/hooks"

var singleton *Server

type Server struct {
	cfg *pkg.ServerConfig
}

func NewServer(cfg *pkg.ServerConfig) *Server {
	singleton = &Server{cfg: cfg}
	return singleton
}

func GetServer() *Server {
	return singleton
}

func (s *Server) Start() {
	if err := s.buildVcsToArgoMap(); err != nil {
		log.Warn().Err(err).Msg("failed to build vcs app map from argo")
	}

	if err := s.ensureWebhooks(); err != nil {
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

func (s *Server) ensureWebhooks() error {
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

	ctx := context.TODO()
	vcsClient, _ := GetVCSClient()

	fullUrl, err := url.JoinPath(urlBase, s.hooksPrefix(), vcsClient.GetName(), "project")
	if err != nil {
		log.Warn().Str("urlBase", urlBase).Msg("failed to create a webhook url")
		return errors.Wrap(err, "failed to create a webhook url")
	}
	log.Info().Str("webhookUrl", fullUrl).Msg("webhook URL for this kubechecks instance")

	for _, repo := range s.cfg.GetVcsRepos() {
		wh, err := vcsClient.GetHookByUrl(ctx, repo, fullUrl)
		if err != nil && !errors.Is(err, pkg.ErrHookNotFound) {
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

func (s *Server) buildVcsToArgoMap() error {
	log.Debug().Msg("building VCS to Application Map")
	if !viper.GetBool("monitor-all-applications") {
		return nil
	}

	ctx := context.TODO()

	result := pkg.NewVcsToArgoMap()

	argoClient := argo_client.GetArgoClient()

	apps, err := argoClient.GetApplications(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to list applications")
	}
	for _, app := range apps.Items {
		result.AddApp(app)
	}

	s.cfg.VcsToArgoMap = result
	return nil
}
