package rego

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pkg/errors"

	"github.com/zapier/kubechecks/pkg"
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

func dumpFiles(manifests []string) (string, error) {
	result, err := os.MkdirTemp("", "kubechecks-manifests-")
	if err != nil {
		return "", errors.Wrap(err, "failed to create temp dir")
	}

	for index, manifest := range manifests {
		filename := fmt.Sprintf("%d.yaml", index)
		fullPath := filepath.Join(result, filename)
		if err = os.WriteFile(fullPath, []byte(manifest), 0o666); err != nil {
			return result, errors.Wrapf(err, "failed to write %s", filename)
		}
	}

	return result, nil
}

func (c *Checker) Check(ctx context.Context, request checks.Request) (msg.Result, error) {
	path, err := dumpFiles(request.JsonManifests)
	if path != "" {
		defer pkg.WipeDir(path)
	}
	if err != nil {
		return msg.Result{}, errors.Wrap(err, "failed to write manifests to disk")
	}

	cr, err := conftest(
		ctx, request.App, path, c.locations, request.Container.VcsClient,
	)
	if err != nil {
		return msg.Result{}, errors.Wrap(err, "failed to run conftest")
	}

	return cr, nil
}
