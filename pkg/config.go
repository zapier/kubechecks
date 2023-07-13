package pkg

import "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"

type VcsToArgoMap struct {
	VcsRepos map[string][]v1alpha1.Application
}

func (v2a *VcsToArgoMap) GetRepo(repo string) []v1alpha1.Application {
	return v2a.VcsRepos[repo]
}

type ServerConfig struct {
	UrlPrefix     string
	WebhookSecret string
	VcsToArgoMap  VcsToArgoMap
}
