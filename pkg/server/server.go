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
	"github.com/ziflex/lecho/v3"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

const KubeChecksHooksPathPrefix = "/hooks"

type Server struct {
	ctr        container.Container
	processors []checks.ProcessorEntry
}

func NewServer(ctr container.Container, processors []checks.ProcessorEntry) *Server {
	return &Server{ctr: ctr, processors: processors}
}

func (s *Server) Start(ctx context.Context) {
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

	ghHooks := NewVCSHookHandler(s.ctr, s.processors)
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
	prefix := s.ctr.Config.UrlPrefix
	serverUrl, err := url.JoinPath("/", prefix, KubeChecksHooksPathPrefix)
	if err != nil {
		log.Warn().Err(err).Msg(":whatintarnation:")
	}

	return strings.TrimSuffix(serverUrl, "/")
}

func (s *Server) ensureWebhooks(ctx context.Context) error {
	if !s.ctr.Config.EnsureWebhooks {
		return nil
	}

	if !s.ctr.Config.MonitorAllApplications {
		return errors.New("must enable 'monitor-all-applications' to create webhooks")
	}

	urlBase := s.ctr.Config.WebhookUrlBase
	if urlBase == "" {
		return errors.New("must define 'webhook-url-base' to create webhooks")
	}

	log.Info().Msg("ensuring all webhooks are created correctly")

	vcsClient := s.ctr.VcsClient

	fullUrl, err := url.JoinPath(urlBase, s.hooksPrefix(), vcsClient.GetName(), "project")
	if err != nil {
		log.Warn().Str("urlBase", urlBase).Msg("failed to create a webhook url")
		return errors.Wrap(err, "failed to create a webhook url")
	}
	log.Info().Str("webhookUrl", fullUrl).Msg("webhook URL for this kubechecks instance")

	for _, repo := range s.ctr.VcsToArgoMap.GetVcsRepos() {
		wh, err := vcsClient.GetHookByUrl(ctx, repo, fullUrl)
		if err != nil && !errors.Is(err, vcs.ErrHookNotFound) {
			log.Error().Err(err).Msgf("failed to get hook for %s:", repo)
			continue
		}

		if wh == nil {
			if err = vcsClient.CreateHook(ctx, repo, fullUrl, s.ctr.Config.WebhookSecret); err != nil {
				log.Info().Err(err).Msgf("failed to create hook for %s:", repo)
			}
		}
	}

	return nil
}
