package checks

import (
	"context"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/crdschema"
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
	// RepoCRDSchemas holds schemas generated from the CustomResourceDefinitions in the
	// commit under test, so a resource can be validated against a CRD added alongside
	// it. Nil when the feature is disabled or nothing was found.
	RepoCRDSchemas *crdschema.Schemas
	JsonManifests  []string
	YamlManifests  []string
	ChangedFiles   []string // files changed in the PR/MR
	RenderedDiff   string   // pre-computed diff text; if empty, Check() will compute it
	PRTitle        string   // MR/PR title — author's stated intent
	PRDescription  string   // MR/PR description — author's stated intent (passed to LLM, truncated at send time)
}
