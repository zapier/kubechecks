package server

import "github.com/labstack/echo/v4"

type debugData struct {
	RepoURLs []string `json:"repoUrls"`

	FilesByApp map[string]map[string][]string `json:"filesByApp"`
	DirsByApp  map[string]map[string][]string `json:"dirsByApp"`

	FilesByAppSet map[string]map[string][]string `json:"filesByAppSet"`
	DirsByAppSet  map[string]map[string][]string `json:"dirsByAppSet"`
}

func (s *Server) dumpDebugInfo(c echo.Context) error {
	response := debugData{
		FilesByApp: make(map[string]map[string][]string),
		DirsByApp:  make(map[string]map[string][]string),

		FilesByAppSet: make(map[string]map[string][]string),
		DirsByAppSet:  make(map[string]map[string][]string),
	}

	response.RepoURLs = append(response.RepoURLs, s.ctr.VcsToArgoMap.GetVcsRepos()...)

	for repoURL, appDir := range s.ctr.VcsToArgoMap.GetAppMap() {
		cloneURL := repoURL.CloneURL("")
		response.RepoURLs = append(response.RepoURLs, cloneURL)

		filesByApp, ok := response.FilesByApp[cloneURL]
		if !ok {
			filesByApp = make(map[string][]string)
			response.FilesByApp[cloneURL] = filesByApp
		}

		dirsByApp, ok := response.DirsByApp[cloneURL]
		if !ok {
			dirsByApp = make(map[string][]string)
			response.DirsByApp[cloneURL] = dirsByApp
		}

		for dir, apps := range appDir.AppDirs() {
			for _, app := range apps {
				dirsByApp[app] = append(dirsByApp[app], dir)
			}
		}

		for file, apps := range appDir.AppFiles() {
			for _, app := range apps {
				filesByApp[app] = append(filesByApp[app], file)
			}
		}
	}

	for repoURL, appSetDir := range s.ctr.VcsToArgoMap.GetAppSetMap() {
		cloneURL := repoURL.CloneURL("")
		response.RepoURLs = append(response.RepoURLs, cloneURL)

		filesByApp, ok := response.FilesByApp[cloneURL]
		if !ok {
			filesByApp = make(map[string][]string)
			response.FilesByApp[cloneURL] = filesByApp
		}

		dirsByApp, ok := response.DirsByApp[cloneURL]
		if !ok {
			dirsByApp = make(map[string][]string)
			response.DirsByApp[cloneURL] = dirsByApp
		}

		for dir, apps := range appSetDir.AppSetDirs() {
			for _, app := range apps {
				dirsByApp[app] = append(dirsByApp[app], dir)
			}
		}

		for file, apps := range appSetDir.AppSetFiles() {
			for _, app := range apps {
				filesByApp[app] = append(filesByApp[app], file)
			}
		}
	}

	return c.JSON(200, response)
}
