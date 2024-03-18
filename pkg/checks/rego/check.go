package rego

import (
	"context"

	"github.com/pkg/errors"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/msg"
)

type Checker struct {
	locations []string
}

var ErrNoLocationsConfigured = errors.New("no policy locations configured")

func NewChecker(cfg config.ServerConfig) (*Checker, error) {
	var c Checker

	c.locations = cfg.PoliciesLocation
	if len(c.locations) == 0 {
		return nil, ErrNoLocationsConfigured
	}

	return &c, nil
}

func (c *Checker) Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	argoApp, err := request.Container.ArgoClient.GetApplicationByName(ctx, request.AppName)
	if err != nil {
		return msg.Result{}, errors.Wrapf(err, "could not retrieve ArgoCD App data: %q", request.AppName)
	}

	cr, err := conftest(
		ctx, argoApp, request.Repo.Directory, c.locations, request.Container.VcsClient,
	)
	if err != nil {
		return msg.Result{}, err
	}

	return cr, nil
}
