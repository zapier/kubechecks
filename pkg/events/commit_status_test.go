package events

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func TestCheckEvent_CommitStatusSetOnlyAfterSuccess(t *testing.T) {
	vcsClient := vcsmocks.NewMockClient(t)
	pr := vcs.PullRequest{CheckID: 1, SHA: "abc"}
	ce := NewCheckEvent(pr, container.Container{VcsClient: vcsClient}, nil, nil, nil)

	vcsClient.EXPECT().CommitStatus(mock.Anything, pr, pkg.StateSuccess).Return(errors.New("rejected")).Once()
	ce.CommitStatus(context.Background(), pkg.StateSuccess)
	assert.False(t, ce.CommitStatusSet(), "a rejected status must not count as set")

	vcsClient.EXPECT().CommitStatus(mock.Anything, pr, pkg.StateSuccess).Return(nil).Once()
	ce.CommitStatus(context.Background(), pkg.StateSuccess)
	assert.True(t, ce.CommitStatusSet())
}

func TestCheckEvent_NoAffectedAppsSetsSkipStatus(t *testing.T) {
	for _, announceNoChanges := range []bool{true, false} {
		t.Run(fmt.Sprintf("announce-no-changes=%t", announceNoChanges), func(t *testing.T) {
			vcsClient := vcsmocks.NewMockClient(t)
			pr := vcs.PullRequest{CheckID: 1, SHA: "abc"}
			ctr := container.Container{
				VcsClient: vcsClient,
				Config:    config.ServerConfig{AnnounceNoChanges: announceNoChanges, Identifier: "test"},
			}
			ce := NewCheckEvent(pr, ctr, nil, nil, nil)

			vcsClient.EXPECT().CommitStatus(mock.Anything, pr, pkg.StateSkip).Return(nil).Once()
			if announceNoChanges {
				vcsClient.EXPECT().PostMessage(mock.Anything, pr, "## Kubechecks test Report\nNo changes").Return(nil, nil).Once()
			}

			require.NoError(t, ce.reportNoAffectedApps(context.Background()))
			assert.True(t, ce.CommitStatusSet(), "a quiet run must not fall through to the error status")
		})
	}
}
