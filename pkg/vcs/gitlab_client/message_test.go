package gitlab_client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gitlab.com/gitlab-org/api/client-go"

	"github.com/zapier/kubechecks/pkg/vcs"
)

type fakeNotes struct {
	NotesServices

	updated   map[int64]string
	created   []string
	createErr error
}

func (f *fakeNotes) UpdateMergeRequestNote(_ interface{}, _, note int64, opt *gitlab.UpdateMergeRequestNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
	if f.updated == nil {
		f.updated = map[int64]string{}
	}
	f.updated[note] = *opt.Body
	return &gitlab.Note{ID: note}, nil, nil
}

func (f *fakeNotes) CreateMergeRequestNote(_ interface{}, _ int64, opt *gitlab.CreateMergeRequestNoteOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
	if f.createErr != nil {
		return nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusBadRequest}}, f.createErr
	}
	f.created = append(f.created, *opt.Body)
	return &gitlab.Note{ID: int64(100 + len(f.created))}, nil, nil
}

func TestUpdateMessage(t *testing.T) {
	pr := vcs.PullRequest{FullName: "zapier/kubechecks", CheckID: 7}

	t.Run("single chunk edits in place", func(t *testing.T) {
		notes := &fakeNotes{}
		c := &Client{c: &GLClient{Notes: notes}}

		require.NoError(t, c.UpdateMessage(context.Background(), pr, 42, []string{"report"}))

		assert.Equal(t, map[int64]string{42: "report"}, notes.updated)
		assert.Empty(t, notes.created)
	})

	t.Run("overflow is posted in order", func(t *testing.T) {
		notes := &fakeNotes{}
		c := &Client{c: &GLClient{Notes: notes}}

		require.NoError(t, c.UpdateMessage(context.Background(), pr, 42, []string{"one", "two", "three"}))

		assert.Equal(t, map[int64]string{42: "one"}, notes.updated)
		assert.Equal(t, []string{"two", "three"}, notes.created)
	})

	t.Run("failure names the chunk", func(t *testing.T) {
		notes := &fakeNotes{createErr: errors.New("bad request")}
		c := &Client{c: &GLClient{Notes: notes}}

		err := c.UpdateMessage(context.Background(), pr, 42, []string{"one", "two"})

		require.ErrorContains(t, err, "posting note 2 of 2: bad request")
		assert.Equal(t, map[int64]string{42: "one"}, notes.updated)
	})

	t.Run("oversized chunk is trimmed", func(t *testing.T) {
		notes := &fakeNotes{}
		c := &Client{c: &GLClient{Notes: notes}}

		require.NoError(t, c.UpdateMessage(context.Background(), pr, 42, []string{strings.Repeat("x", MaxCommentLength+10)}))

		assert.Len(t, notes.updated[42], MaxCommentLength)
	})
}
