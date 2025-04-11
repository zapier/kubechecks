package server

import "github.com/labstack/echo/v4"

type debugData struct {
	RepoURLs   []string                       `json:"repoUrls"`
	FilesByApp map[string]map[string][]string `json:"filesByApp"`
	DirsByApp  map[string]map[string][]string `json:"dirsByApp"`
}

func (s *Server) dumpDebugInfo(c echo.Context) error {
	response := debugData{
		FilesByApp: make(map[string]map[string][]string),
		DirsByApp:  make(map[string]map[string][]string),
	}

	response.RepoURLs = append(response.RepoURLs, s.ctr.VcsToArgoMap.GetVcsRepos()...)

	for repoURL, appDir := range s.ctr.VcsToArgoMap.GetMap() {
		filesByApp := make(map[string][]string)
		dirsByApp := make(map[string][]string)

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

		cloneURL := repoURL.CloneURL("")
		response.RepoURLs = append(response.RepoURLs, cloneURL)
		response.FilesByApp[cloneURL] = filesByApp
		response.DirsByApp[cloneURL] = dirsByApp
	}

	return c.JSON(200, response)
}
