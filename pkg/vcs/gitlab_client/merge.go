package gitlab_client

import (
	"context"

	"gitlab.com/gitlab-org/api/client-go"

	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/telemetry"
)

var tracer = otel.Tracer("pkg/vcs/gitlab_client")

type Changes struct {
	OldPath     string `json:"old_path"`
	NewPath     string `json:"new_path"`
	AMode       string `json:"a_mode"`
	BMode       string `json:"b_mode"`
	Diff        string `json:"diff"`
	NewFile     bool   `json:"new_file"`
	RenamedFile bool   `json:"renamed_file"`
	DeletedFile bool   `json:"deleted_file"`
}

func (c *Client) GetMergeChanges(ctx context.Context, projectId int, mergeReqId int) ([]*Changes, error) {
	_, span := tracer.Start(ctx, "GetMergeChanges")
	defer span.End()

	var changes []*Changes
	opts := &gitlab.ListMergeRequestDiffsOptions{
		ListOptions: gitlab.ListOptions{
			PerPage: 100,
			Page:    1,
		},
	}

	for {
		diffs, resp, err := c.c.MergeRequests.ListMergeRequestDiffs(projectId, mergeReqId, opts)
		if err != nil {
			telemetry.SetError(span, err, "Get MergeRequest Changes")
			return changes, err
		}

		for _, change := range diffs {
			changes = append(changes, &Changes{
				OldPath:     change.OldPath,
				NewPath:     change.NewPath,
				AMode:       change.AMode,
				BMode:       change.BMode,
				Diff:        change.Diff,
				NewFile:     change.NewFile,
				RenamedFile: change.RenamedFile,
				DeletedFile: change.DeletedFile,
			})
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	return changes, nil
}

type MergeRequestsServices interface {
	GetMergeRequestDiffVersions(pid interface{}, mergeRequest int, opt *gitlab.GetMergeRequestDiffVersionsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.MergeRequestDiffVersion, *gitlab.Response, error)
	ListMergeRequestDiffs(pid interface{}, mergeRequest int, opt *gitlab.ListMergeRequestDiffsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.MergeRequestDiff, *gitlab.Response, error)
	UpdateMergeRequest(pid interface{}, mergeRequest int, opt *gitlab.UpdateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error)
	GetMergeRequest(pid interface{}, mergeRequest int, opt *gitlab.GetMergeRequestsOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error)
}

type MergeRequestsService struct {
	MergeRequestsServices
}
