package events

import (
	"context"
	"testing"

	"github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	vcsmocks "github.com/zapier/kubechecks/mocks/vcs/mocks"
	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/archive"
	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
	"github.com/zapier/kubechecks/pkg/vcs"
)

func TestPanicDetailsFoldsTheStackTrace(t *testing.T) {
	details := panicDetails("doing things", "boom", []byte("goroutine 1 [running]:\nmain.main()\n"))

	assert.Contains(t, details, "kubechecks encountered an error while doing things")
	assert.Contains(t, details, "boom")
	assert.Contains(t, details, "<details>\n<summary>Stack trace</summary>")
	assert.Contains(t, details, "goroutine 1 [running]:\nmain.main()\n```\n</details>")
}

// A panic outside of the per-app workers used to take the whole event down without a
// word on the PR, leaving people wondering whether kubechecks ran at all.
func TestProcessReportsPanicsOnThePullRequest(t *testing.T) {
	pr := vcs.PullRequest{Name: "repo", CheckID: 1}

	vcsClient := new(vcsmocks.MockClient)
	vcsClient.EXPECT().DownloadArchive(mock.Anything, pr).Run(func(context.Context, vcs.PullRequest) {
		panic("kaboom")
	})
	vcsClient.EXPECT().MaxCommentLength().Return(65535)
	var posted string
	vcsClient.EXPECT().PostMessage(mock.Anything, pr, mock.Anything).
		Run(func(_ context.Context, _ vcs.PullRequest, message string) { posted = message }).
		Return(&msg.Message{}, nil)
	vcsClient.EXPECT().CommitStatus(mock.Anything, pr, pkg.StatePanic).Return(nil)

	cfg := config.ServerConfig{Identifier: "test", ArchiveCacheDir: t.TempDir()}
	ctr := container.Container{Config: cfg, VcsClient: vcsClient, ArchiveManager: archive.NewManager(cfg, vcsClient)}
	ce := NewCheckEvent(pr, ctr, nil, nil, nil)

	err := ce.Process(context.Background())
	require.ErrorContains(t, err, "kaboom")

	assert.Contains(t, posted, "## Kubechecks test Report")
	assert.Contains(t, posted, "kubechecks encountered an error")
	assert.Contains(t, posted, "kaboom")
	assert.Contains(t, posted, "<summary>Stack trace</summary>")
	assert.Contains(t, posted, "TestProcessReportsPanicsOnThePullRequest")
}

// Once the placeholder comment exists, the panic replaces it rather than leaving
// "kubechecks running..." behind.
func TestReportPanicUpdatesThePlaceholder(t *testing.T) {
	pr := vcs.PullRequest{Name: "repo", CheckID: 1}

	vcsClient := new(vcsmocks.MockClient)
	vcsClient.EXPECT().MaxCommentLength().Return(65535)
	vcsClient.EXPECT().UpdateMessage(mock.Anything, pr, 42, mock.MatchedBy(func(chunks []string) bool {
		return len(chunks) == 1 && assert.Contains(t, chunks[0], "kaboom")
	})).Return(nil)
	vcsClient.EXPECT().CommitStatus(mock.Anything, pr, pkg.StatePanic).Return(nil)

	ce := NewCheckEvent(pr, container.Container{VcsClient: vcsClient}, nil, nil, nil)
	ce.vcsNote = msg.NewMessage("repo", 1, 42, vcsClient)

	ce.reportPanic(context.Background(), "kaboom", []byte("stack"))
}

// The panic result has to be on the note by the time Wait returns, or the comment
// gets built without it.
func TestRunnerRecordsPanicBeforeWaitReturns(t *testing.T) {
	vcsClient := new(vcsmocks.MockClient)
	note := msg.NewMessage("repo", 1, 42, vcsClient)
	note.AddNewApp(context.Background(), "app")

	runner := newRunner(
		container.Container{}, v1alpha1.Application{}, "app", "1.30.0",
		nil, nil, zerolog.Nop(), note, nil, nil, nil,
	)
	runner.Run(context.Background(), "exploding", func(context.Context, checks.Request) (msg.Result, error) {
		panic("kaboom")
	}, pkg.StatePanic)
	runner.Wait()

	assert.Equal(t, pkg.StatePanic, note.WorstState())
}
