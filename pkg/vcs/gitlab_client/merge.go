package gitlab_client

import (
	"context"
	"strings"

	"github.com/zapier/kubechecks/pkg/repo_config"
	"github.com/zapier/kubechecks/telemetry"
	"go.opentelemetry.io/otel"

	"github.com/xanzy/go-gitlab"
)

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
	_, span := otel.Tracer("Kubechecks").Start(ctx, "GetMergeChanges")
	defer span.End()

	changes := []*Changes{}
	mr, _, err := c.MergeRequests.GetMergeRequestChanges(projectId, mergeReqId, &gitlab.GetMergeRequestChangesOptions{})
	if err != nil {
		telemetry.SetError(span, err, "Get MergeRequest Changes")
		return changes, err
	}

	for _, change := range mr.Changes {
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

	return changes, nil
}

func CheckForValidChanges(ctx context.Context, changes []*Changes, paths []string, fileTypes []string) bool {
	_, span := otel.Tracer("Kubechecks").Start(ctx, "CheckForValidChanges")
	defer span.End()

	for _, change := range changes {
		// check for change to .kubechecks.yaml file
		for _, cfgFile := range repo_config.RepoConfigFilenameVariations() {
			if change.NewPath == cfgFile {
				return true
			}
		}
		for _, path := range paths {
			for _, fileType := range fileTypes {
				if change.validChange(path, fileType) {
					return true
				}
			}
		}
	}

	return false
}

func (chg *Changes) validChange(path, fileType string) bool {
	if strings.HasSuffix(chg.NewPath, fileType) {
		if strings.HasPrefix(chg.NewPath, path) {
			return true
		}
	}

	return false
}
