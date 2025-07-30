package checks

import (
	"context"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
)

type ProcessorEntry struct {
	Name       string
	Processor  func(ctx context.Context, request Request) (msg.Result, error)
	WorstState pkg.CommitState
}

type Processor interface {
	Name() string
	Command()
}

type Request struct {
	Log       zerolog.Logger
	Note      *msg.Message
	App       v1alpha1.Application
	Repo      *git.Repo
	Container container.Container

	QueueApp  func(app v1alpha1.Application)
	RemoveApp func(app v1alpha1.Application)

	AppName           string
	KubernetesVersion string
	JsonManifests     []string
	YamlManifests     []string
}
