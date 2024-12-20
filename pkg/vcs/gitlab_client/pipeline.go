package gitlab_client

import (
	"github.com/rs/zerolog/log"
	"github.com/xanzy/go-gitlab"

	"github.com/zapier/kubechecks/pkg"
)

func (c *Client) GetLastPipelinesForCommit(projectName string, commitSHA string) *gitlab.PipelineInfo {
	pipelines, _, err := c.c.Pipelines.ListProjectPipelines(projectName, &gitlab.ListProjectPipelinesOptions{
		SHA: pkg.Pointer(commitSHA),
	})
	if err != nil {
		log.Error().Err(err).Msg("gitlab client: could not get last pipeline for commit")
		return nil
	}

	if len(pipelines) > 0 {
		// check for merge_requests_event
		for _, p := range pipelines {
			if p.Source == "merge_request_event" {
				return p
			}
		}

		// check for external_pull_request_events next
		for _, p := range pipelines {
			if p.Source == "pipeline" {
				return p
			}
		}

		// check for external_pull_request_events next
		for _, p := range pipelines {
			if p.Source == "external_pull_request_event" {
				return p
			}
		}

		for _, p := range pipelines {
			if p.Source == "external" {
				return p
			}
		}
	}

	return nil
}

type PipelinesServices interface {
	ListProjectPipelines(pid interface{}, opt *gitlab.ListProjectPipelinesOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.PipelineInfo, *gitlab.Response, error)
}

type PipelinesService struct {
	PipelinesServices
}
