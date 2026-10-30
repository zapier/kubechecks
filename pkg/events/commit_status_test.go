package events

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg"
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
