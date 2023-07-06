package server

import (
	"net/url"
	"strings"

	"github.com/heptiolabs/healthcheck"
	"github.com/labstack/echo-contrib/prometheus"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/rs/zerolog/log"
	"github.com/ziflex/lecho/v3"
)

const KubeChecksHooksPathPrefix = "/hooks"

var singleton *Server

type ServerConfig struct {
	UrlPrefix     string
	WebhookSecret string
}

type Server struct {
	cfg *ServerConfig
}

func NewServer(cfg *ServerConfig) *Server {
	singleton = &Server{cfg: cfg}
	return singleton
}

func GetServer() *Server {
	return singleton
}

func (s *Server) Start() {
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

	hooksGroup := e.Group(s.HooksPrefix())

	ghHooks := NewVCSHookHandler(s.cfg.WebhookSecret)
	ghHooks.AttachHandlers(hooksGroup)

	if err := e.Start(":8080"); err != nil {
		log.Fatal().Err(err).Msg("could not start hooks server")
	}

}

func (s *Server) HooksPrefix() string {
	prefix := s.cfg.UrlPrefix
	url, err := url.JoinPath("/", prefix, KubeChecksHooksPathPrefix)
	if err != nil {
		log.Warn().Err(err).Msg(":whatintarnation:")
	}

	return strings.TrimSuffix(url, "/")
}
