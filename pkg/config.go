package pkg

type VcsToArgoMap struct {
	//                map[RepoCloneUrl]map[AppPath]AppName
	vcsAppStubsByRepo map[string]map[string]string
}

func NewVcsToArgoMap() VcsToArgoMap {
	return VcsToArgoMap{
		vcsAppStubsByRepo: make(map[string]map[string]string),
	}
}

func (v2a *VcsToArgoMap) GetAppsInRepo(repoCloneUrl string) map[string]string {
	return v2a.vcsAppStubsByRepo[repoCloneUrl]
}

func (v2a *VcsToArgoMap) AddApp(repoCloneUrl, path, name string) {
	apps, ok := v2a.vcsAppStubsByRepo[repoCloneUrl]
	if !ok {
		apps = make(map[string]string)
		v2a.vcsAppStubsByRepo[repoCloneUrl] = apps
	}
	apps[path] = name
}

type ServerConfig struct {
	UrlPrefix     string
	WebhookSecret string
	VcsToArgoMap  VcsToArgoMap
}

func (cfg *ServerConfig) GetVcsRepos() []string {
	var repos []string
	for key := range cfg.VcsToArgoMap.vcsAppStubsByRepo {
		repos = append(repos, key)
	}
	return repos
}
