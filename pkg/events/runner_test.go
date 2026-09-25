package events

import (
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"

	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
)

// Every check reads its inputs from the request the runner carries, so anything the
// runner drops is simply absent for all of them — the kubeconform check resolves
// relative schema locations against Repo, and saw nothing to resolve against when this
// was not wired through.
func TestNewRunnerCarriesTheCheckout(t *testing.T) {
	repo := &git.Repo{Directory: "/tmp/checkout"}

	runner := newRunner(
		container.Container{}, v1alpha1.Application{}, "app", "1.30.0",
		nil, nil, zerolog.Nop(), nil, nil, nil, repo,
	)

	assert.Same(t, repo, runner.Request.Repo)
}

func TestNewRunnerToleratesNoCheckout(t *testing.T) {
	runner := newRunner(
		container.Container{}, v1alpha1.Application{}, "app", "1.30.0",
		nil, nil, zerolog.Nop(), nil, nil, nil, nil,
	)

	assert.Nil(t, runner.Request.Repo)
}
