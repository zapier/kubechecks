package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
	"github.com/zapier/kubechecks/pkg/appdir"
	"github.com/zapier/kubechecks/pkg/container"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestDumpDebugInfo(t *testing.T) {
	// setup
	vcsToArgoMap := appdir.NewVcsToArgoMap("username")
	vcsToArgoMap.AddApp(&v1alpha1.Application{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-app",
		},
		Spec: v1alpha1.ApplicationSpec{
			Source: &v1alpha1.ApplicationSource{
				RepoURL: "https://github.com/test/test2.git",
			},
		},
	})
	ctr := container.Container{
		VcsToArgoMap: vcsToArgoMap,
	}
	s := NewServer(ctr, nil)
	e := echo.New()
	r := httptest.NewRequest(http.MethodGet, "/debug", nil)
	w := httptest.NewRecorder()
	c := e.NewContext(r, w)

	// execute
	err := s.dumpDebugInfo(c)

	// verify
	require.NoError(t, err)
	var result debugData
	err = json.Unmarshal(w.Body.Bytes(), &result)
	require.NoError(t, err)
}
