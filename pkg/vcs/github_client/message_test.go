package github_client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/google/go-github/v74/github"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/zapier/kubechecks/pkg/vcs"
)

type fakeIssues struct {
	IssuesServices

	edited  map[int64]string
	created []string
	// status codes to fail the next calls with, 0 for no response at all
	editFailures, createFailures []int
}

func failNext(failures *[]int) (*github.Response, error) {
	if len(*failures) == 0 {
		return nil, nil
	}
	code := (*failures)[0]
	*failures = (*failures)[1:]
	if code == 0 {
		return nil, errors.New("connection reset")
	}
	return &github.Response{Response: &http.Response{StatusCode: code}}, errors.New(http.StatusText(code))
}

func (f *fakeIssues) EditComment(_ context.Context, _, _ string, id int64, comment *github.IssueComment) (*github.IssueComment, *github.Response, error) {
	if resp, err := failNext(&f.editFailures); err != nil {
		return nil, resp, err
	}
	if f.edited == nil {
		f.edited = map[int64]string{}
	}
	f.edited[id] = comment.GetBody()
	return comment, nil, nil
}

func (f *fakeIssues) CreateComment(_ context.Context, _, _ string, _ int, comment *github.IssueComment) (*github.IssueComment, *github.Response, error) {
	if resp, err := failNext(&f.createFailures); err != nil {
		return nil, resp, err
	}
	f.created = append(f.created, comment.GetBody())
	return comment, nil, nil
}

func newMessageClient(issues *fakeIssues) *Client {
	return &Client{googleClient: &GClient{Issues: issues}, commentRetry: fastRetry}
}

var testPR = vcs.PullRequest{Owner: "zapier", Name: "kubechecks", FullName: "zapier/kubechecks", CheckID: 7}

func updateMessage(t *testing.T, issues *fakeIssues, chunks ...string) error {
	t.Helper()
	return newMessageClient(issues).UpdateMessage(context.Background(), testPR, 42, chunks)
}

func TestUpdateMessage_SingleChunkEditsInPlace(t *testing.T) {
	issues := &fakeIssues{}

	require.NoError(t, updateMessage(t, issues, "report"))

	assert.Equal(t, map[int64]string{42: "report"}, issues.edited)
	assert.Empty(t, issues.created)
}

func TestUpdateMessage_OverflowIsPostedInOrder(t *testing.T) {
	issues := &fakeIssues{}

	require.NoError(t, updateMessage(t, issues, "one", "two", "three"))

	assert.Equal(t, map[int64]string{42: "one"}, issues.edited)
	assert.Equal(t, []string{"two", "three"}, issues.created)
}

func TestUpdateMessage_Retries(t *testing.T) {
	issues := &fakeIssues{
		editFailures:   []int{0, http.StatusBadGateway},
		createFailures: []int{http.StatusTooManyRequests, http.StatusTooManyRequests},
	}

	require.NoError(t, updateMessage(t, issues, "one", "two"))

	assert.Equal(t, map[int64]string{42: "one"}, issues.edited)
	assert.Equal(t, []string{"two"}, issues.created)
}

func TestUpdateMessage_GivesUp(t *testing.T) {
	for name, tc := range map[string]struct {
		issues *fakeIssues
		want   string
	}{
		"client error on edit":   {&fakeIssues{editFailures: []int{http.StatusUnprocessableEntity, http.StatusBadGateway}}, "posting comment 1 of 2"},
		"client error on create": {&fakeIssues{createFailures: []int{http.StatusUnprocessableEntity, http.StatusBadGateway}}, "posting comment 2 of 2"},
		// the comment may be there already
		"no response to a create":  {&fakeIssues{createFailures: []int{0, http.StatusBadGateway}}, "posting comment 2 of 2"},
		"server error on a create": {&fakeIssues{createFailures: []int{http.StatusServiceUnavailable, http.StatusBadGateway}}, "posting comment 2 of 2"},
	} {
		t.Run(name, func(t *testing.T) {
			err := updateMessage(t, tc.issues, "one", "two")

			require.ErrorContains(t, err, tc.want)
			// a second attempt would have used up the 502
			assert.Equal(t, []int{http.StatusBadGateway}, append(tc.issues.editFailures, tc.issues.createFailures...))
			assert.Empty(t, tc.issues.created)
		})
	}
}

func TestUpdateMessage_TrimsOversizedChunk(t *testing.T) {
	issues := &fakeIssues{}

	require.NoError(t, updateMessage(t, issues, strings.Repeat("x", MaxCommentLength+10)))

	assert.Len(t, issues.edited[42], MaxCommentLength)
}
