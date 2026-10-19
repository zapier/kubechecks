package events

import (
	"context"
	"errors"
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	affectedappsmocks "github.com/zapier/kubechecks/mocks/affected_apps/mocks"
	generatorsmocks "github.com/zapier/kubechecks/mocks/generator/mocks"
	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/affected_apps"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/generator"
	"github.com/zapier/kubechecks/pkg/git"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func appSetNamed(name string) v1alpha1.ApplicationSet {
	return v1alpha1.ApplicationSet{ObjectMeta: metav1.ObjectMeta{Name: name}}
}

func TestGenerateListOfAffectedApps_RecoversGeneratorPanic(t *testing.T) {
	gen := new(generatorsmocks.MockAppsGenerator)
	gen.On("GenerateApplicationSetApps", mock.Anything, mock.MatchedBy(func(a v1alpha1.ApplicationSet) bool { return a.Name == "broken" }), mock.Anything).
		Run(func(mock.Arguments) { panic("nil pointer dereference") })
	gen.On("GenerateApplicationSetApps", mock.Anything, mock.MatchedBy(func(a v1alpha1.ApplicationSet) bool { return a.Name == "working" }), mock.Anything).
		Return(generator.AppSetResult{Apps: []v1alpha1.Application{{ObjectMeta: metav1.ObjectMeta{Name: "app1"}}}}, nil)

	matcher := new(affectedappsmocks.MockMatcher)
	matcher.On("AffectedApps", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(affected_apps.AffectedItems{
		ApplicationSets: []v1alpha1.ApplicationSet{appSetNamed("broken"), appSetNamed("working")},
	}, nil)

	ce := &CheckEvent{generator: gen, matcher: matcher}

	require.NotPanics(t, func() {
		err := ce.GenerateListOfAffectedApps(context.Background(), &git.Repo{}, "main", MockInitMatcherFn())
		require.NoError(t, err)
	})

	require.Len(t, ce.affectedItems.Applications, 1, "a panicking appset must not stop the others")
	assert.Equal(t, "app1", ce.affectedItems.Applications[0].Name)

	results := ce.appSetResults["broken (ApplicationSet)"]
	require.Len(t, results, 1)
	assert.Equal(t, pkg.StatePanic, results[0].State)
	assert.Contains(t, results[0].Details, "nil pointer dereference")
	assert.NotContains(t, ce.appSetResults, "working (ApplicationSet)")
}

func TestRecordAppSetResult(t *testing.T) {
	t.Run("previews and partial errors are both reported", func(t *testing.T) {
		ce := &CheckEvent{}
		ce.recordAppSetResult("orbit-preview", generator.AppSetResult{
			Previews: []v1alpha1.Application{{
				TypeMeta:   metav1.TypeMeta{APIVersion: "argoproj.io/v1alpha1", Kind: "Application"},
				ObjectMeta: metav1.ObjectMeta{Name: "orbit-pr-1234", Namespace: "argocd"},
				Spec:       v1alpha1.ApplicationSpec{Project: "default"},
			}},
		}, errors.New("generated application \"Bad\" is invalid"))

		results := ce.appSetResults["orbit-preview (ApplicationSet)"]
		require.Len(t, results, 2)

		assert.Equal(t, pkg.StateError, results[0].State)
		assert.Contains(t, results[0].Details, `"Bad" is invalid`)

		assert.Equal(t, pkg.StateSuccess, results[1].State)
		assert.Contains(t, results[1].Details, "name: orbit-pr-1234")
		assert.Contains(t, results[1].Details, "project: default")
		assert.NotContains(t, results[1].Details, "status:")
		assert.NotContains(t, results[1].Details, "creationTimestamp")
	})

	t.Run("an appset that only produced real apps is not reported", func(t *testing.T) {
		ce := &CheckEvent{}
		ce.recordAppSetResult("plain", generator.AppSetResult{Apps: []v1alpha1.Application{{}}}, nil)
		assert.Empty(t, ce.appSetResults)
	})
}

func TestReportPanic(t *testing.T) {
	pr := vcs.PullRequest{Name: "argocd", CheckID: 1234}

	t.Run("posts a new comment when none exists yet", func(t *testing.T) {
		vcsClient := new(vcsmocks.MockClient)
		vcsClient.On("PostMessage", mock.Anything, pr, mock.MatchedBy(func(body string) bool {
			return assert.Contains(t, body, "boom")
		})).Return(&msg.Message{}, nil)
		vcsClient.On("CommitStatus", mock.Anything, pr, pkg.StatePanic).Return(nil)

		ce := &CheckEvent{pullRequest: pr, ctr: container.Container{VcsClient: vcsClient}}
		err := ce.reportPanic(context.Background(), "boom")

		assert.ErrorContains(t, err, "boom")
		vcsClient.AssertExpectations(t)
	})

	t.Run("overwrites the in-progress comment when one exists", func(t *testing.T) {
		vcsClient := new(vcsmocks.MockClient)
		vcsClient.On("UpdateMessage", mock.Anything, pr, 42, mock.MatchedBy(func(chunks []string) bool {
			return len(chunks) == 1 && assert.Contains(t, chunks[0], "boom")
		})).Return(nil)
		vcsClient.On("CommitStatus", mock.Anything, pr, pkg.StatePanic).Return(nil)

		ce := &CheckEvent{pullRequest: pr, ctr: container.Container{VcsClient: vcsClient}, vcsNote: &msg.Message{NoteID: 42}}
		err := ce.reportPanic(context.Background(), "boom")

		assert.ErrorContains(t, err, "boom")
		vcsClient.AssertExpectations(t)
	})
}

func TestProcess_ReportsPanic(t *testing.T) {
	pr := vcs.PullRequest{Name: "argocd", CheckID: 1234}

	vcsClient := new(vcsmocks.MockClient)
	vcsClient.On("PostMessage", mock.Anything, pr, mock.Anything).Return(&msg.Message{}, nil)
	vcsClient.On("CommitStatus", mock.Anything, pr, pkg.StatePanic).Return(nil)

	// a nil ArchiveManager panics on first use, standing in for any unexpected panic during a run
	ce := &CheckEvent{pullRequest: pr, ctr: container.Container{VcsClient: vcsClient}}

	var err error
	require.NotPanics(t, func() { err = ce.Process(context.Background()) })
	assert.ErrorContains(t, err, "panic while processing check")
	vcsClient.AssertExpectations(t)
}
