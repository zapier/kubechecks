package generator

import (
	"context"
	"testing"

	"github.com/argoproj/argo-cd/v3/applicationset/utils"
	argov1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	controllerClient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/zapier/kubechecks/pkg/config"
	"github.com/zapier/kubechecks/pkg/container"
)

// fakeKubeClientSet is a minimal implementation of pkg/kubernetes.Interface backed by a
// controller-runtime fake client, so GenerateApplicationSetApps can be exercised without a
// real cluster.
type fakeKubeClientSet struct {
	controllerClient controllerClient.Client
}

func newFakeKubeClientSet(t *testing.T) *fakeKubeClientSet {
	t.Helper()

	scheme := runtime.NewScheme()
	require.NoError(t, argov1alpha1.AddToScheme(scheme))

	return &fakeKubeClientSet{
		controllerClient: fake.NewClientBuilder().WithScheme(scheme).Build(),
	}
}

func (f *fakeKubeClientSet) ClientSet() kubernetes.Interface { return nil }
func (f *fakeKubeClientSet) Config() *rest.Config            { return nil }
func (f *fakeKubeClientSet) ControllerClient() *controllerClient.Client {
	return &f.controllerClient
}

func listElement(t *testing.T, jsonStr string) apiextensionsv1.JSON {
	t.Helper()
	return apiextensionsv1.JSON{Raw: []byte(jsonStr)}
}

func metav1ObjectMeta(name string, labels map[string]string) metav1.ObjectMeta {
	return metav1.ObjectMeta{Name: name, Labels: labels}
}

func baseApplicationSpec() argov1alpha1.ApplicationSpec {
	return argov1alpha1.ApplicationSpec{
		Project: "default",
		Source: &argov1alpha1.ApplicationSource{
			RepoURL: "https://example.com/repo.git",
			Path:    ".",
		},
		Destination: argov1alpha1.ApplicationDestination{
			Server: "https://kubernetes.default.svc",
		},
	}
}

func TestGetTempApplication(t *testing.T) {
	tmpl := argov1alpha1.ApplicationSetTemplate{
		ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
			Name:        "my-app",
			Namespace:   "my-ns",
			Labels:      map[string]string{"team": "infra"},
			Annotations: map[string]string{"note": "hello"},
			Finalizers:  []string{"resources-finalizer.argocd.argoproj.io"},
		},
		Spec: baseApplicationSpec(),
	}

	app := getTempApplication(tmpl)

	assert.Equal(t, "my-app", app.Name)
	assert.Equal(t, "my-ns", app.Namespace)
	assert.Equal(t, map[string]string{"team": "infra"}, app.Labels)
	assert.Equal(t, map[string]string{"note": "hello"}, app.Annotations)
	assert.Equal(t, []string{"resources-finalizer.argocd.argoproj.io"}, app.Finalizers)
	assert.Equal(t, tmpl.Spec, app.Spec)
	assert.Equal(t, "argoproj.io/v1alpha1", app.APIVersion)
	assert.Equal(t, "Application", app.Kind)
}

func TestGetGenerators(t *testing.T) {
	generators := getGenerators(fake.NewClientBuilder().Build(), "argocd")

	assert.Len(t, generators, 4)
	for _, name := range []string{"List", "Clusters", "Matrix", "Merge"} {
		assert.NotNil(t, generators[name], "expected generator %q to be registered", name)
	}
}

func TestApplyTemplatePatch(t *testing.T) {
	app := &argov1alpha1.Application{
		ObjectMeta: metav1ObjectMeta("my-app", map[string]string{"team": "infra"}),
		Spec:       baseApplicationSpec(),
	}

	t.Run("applies a merge patch", func(t *testing.T) {
		patched, err := applyTemplatePatch(app, `metadata:
  labels:
    owner: platform
spec:
  project: patched-project`)
		require.NoError(t, err)

		assert.Equal(t, "platform", patched.Labels["owner"])
		assert.Equal(t, "infra", patched.Labels["team"], "existing labels should be preserved")
	})

	t.Run("preserves the original project even if the patch tries to change it", func(t *testing.T) {
		patched, err := applyTemplatePatch(app, `spec:
  project: malicious-project`)
		require.NoError(t, err)

		assert.Equal(t, "default", patched.Spec.Project)
	})

	t.Run("errors on invalid patch yaml", func(t *testing.T) {
		_, err := applyTemplatePatch(app, "not: valid: yaml: [")
		assert.Error(t, err)
	})

	t.Run("errors when patch does not match the Application shape", func(t *testing.T) {
		_, err := applyTemplatePatch(app, `spec: "this-should-be-an-object"`)
		assert.Error(t, err)
	})
}

func TestRenderTemplatePatch(t *testing.T) {
	app := &argov1alpha1.Application{
		ObjectMeta: metav1ObjectMeta("my-app", nil),
		Spec:       baseApplicationSpec(),
	}

	appset := argov1alpha1.ApplicationSet{
		Spec: argov1alpha1.ApplicationSetSpec{
			GoTemplate: true,
		},
	}

	patch := `metadata:
  labels:
    owner: {{.owner}}`
	appset.Spec.TemplatePatch = &patch

	patched, err := renderTemplatePatch(&utils.Render{}, app, appset, map[string]interface{}{"owner": "team-a"})
	require.NoError(t, err)
	assert.Equal(t, "team-a", patched.Labels["owner"])
}

func TestRenderTemplatePatch_InvalidTemplate(t *testing.T) {
	app := &argov1alpha1.Application{
		ObjectMeta: metav1ObjectMeta("my-app", nil),
		Spec:       baseApplicationSpec(),
	}

	patch := `metadata:
  labels:
    owner: {{.owner.doesnotexist.nested}}`
	appset := argov1alpha1.ApplicationSet{
		Spec: argov1alpha1.ApplicationSetSpec{
			GoTemplate:    true,
			TemplatePatch: &patch,
		},
	}

	_, err := renderTemplatePatch(&utils.Render{}, app, appset, map[string]interface{}{"owner": "team-a"})
	assert.Error(t, err)
}

func TestGenerateApplications(t *testing.T) {
	fakeClient := fake.NewClientBuilder().Build()

	t.Run("go template list generator produces applications and overrides namespace", func(t *testing.T) {
		appset := argov1alpha1.ApplicationSet{
			ObjectMeta: metav1ObjectMeta("my-appset", nil),
			Spec: argov1alpha1.ApplicationSetSpec{
				GoTemplate: true,
				Generators: []argov1alpha1.ApplicationSetGenerator{
					{
						List: &argov1alpha1.ListGenerator{
							Elements: []apiextensionsv1.JSON{
								listElement(t, `{"name": "app1"}`),
								listElement(t, `{"name": "app2"}`),
							},
						},
					},
				},
				Template: argov1alpha1.ApplicationSetTemplate{
					ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
						Name:      "{{.name}}",
						Namespace: "template-namespace-should-be-overridden",
					},
					Spec: baseApplicationSpec(),
				},
			},
		}
		appset.Namespace = "argocd"

		apps, reason, err := generateApplications(appset, getGenerators(fakeClient, "argocd"), fakeClient)
		require.NoError(t, err)
		assert.Empty(t, reason)
		require.Len(t, apps, 2)

		names := []string{apps[0].Name, apps[1].Name}
		assert.ElementsMatch(t, []string{"app1", "app2"}, names)
		for _, app := range apps {
			assert.Equal(t, "argocd", app.Namespace, "app namespace must be forced to the appset namespace")
		}
	})

	t.Run("non go template list generator flattens values params", func(t *testing.T) {
		appset := argov1alpha1.ApplicationSet{
			ObjectMeta: metav1ObjectMeta("my-appset", nil),
			Spec: argov1alpha1.ApplicationSetSpec{
				GoTemplate: false,
				Generators: []argov1alpha1.ApplicationSetGenerator{
					{
						List: &argov1alpha1.ListGenerator{
							Elements: []apiextensionsv1.JSON{
								listElement(t, `{"name": "app1", "values": {"region": "us-east-1"}}`),
							},
						},
					},
				},
				Template: argov1alpha1.ApplicationSetTemplate{
					ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
						Name:   "{{name}}",
						Labels: map[string]string{"region": "{{values.region}}"},
					},
					Spec: baseApplicationSpec(),
				},
			},
		}
		appset.Namespace = "argocd"

		apps, _, err := generateApplications(appset, getGenerators(fakeClient, "argocd"), fakeClient)
		require.NoError(t, err)
		require.Len(t, apps, 1)
		assert.Equal(t, "app1", apps[0].Name)
		assert.Equal(t, "us-east-1", apps[0].Labels["region"])
	})

	t.Run("template patch is applied to every generated application", func(t *testing.T) {
		patch := `metadata:
  labels:
    owner: {{.name}}`
		appset := argov1alpha1.ApplicationSet{
			ObjectMeta: metav1ObjectMeta("my-appset", nil),
			Spec: argov1alpha1.ApplicationSetSpec{
				GoTemplate:    true,
				TemplatePatch: &patch,
				Generators: []argov1alpha1.ApplicationSetGenerator{
					{
						List: &argov1alpha1.ListGenerator{
							Elements: []apiextensionsv1.JSON{
								listElement(t, `{"name": "app1"}`),
							},
						},
					},
				},
				Template: argov1alpha1.ApplicationSetTemplate{
					ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
						Name: "{{.name}}",
					},
					Spec: baseApplicationSpec(),
				},
			},
		}
		appset.Namespace = "argocd"

		apps, _, err := generateApplications(appset, getGenerators(fakeClient, "argocd"), fakeClient)
		require.NoError(t, err)
		require.Len(t, apps, 1)
		assert.Equal(t, "app1", apps[0].Labels["owner"])
	})

	t.Run("a failing generator surfaces an error and reason but does not panic", func(t *testing.T) {
		appset := argov1alpha1.ApplicationSet{
			ObjectMeta: metav1ObjectMeta("my-appset", nil),
			Spec: argov1alpha1.ApplicationSetSpec{
				GoTemplate: true,
				Generators: []argov1alpha1.ApplicationSetGenerator{
					{
						List: &argov1alpha1.ListGenerator{
							// malformed JSON causes the List generator to fail generating params
							Elements: []apiextensionsv1.JSON{listElement(t, `not-json`)},
						},
					},
				},
				Template: argov1alpha1.ApplicationSetTemplate{
					ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
						Name: "{{.name}}",
					},
					Spec: baseApplicationSpec(),
				},
			},
		}
		appset.Namespace = "argocd"

		apps, reason, err := generateApplications(appset, getGenerators(fakeClient, "argocd"), fakeClient)
		require.Error(t, err)
		assert.Equal(t, argov1alpha1.ApplicationSetReasonType(argov1alpha1.ApplicationSetReasonApplicationParamsGenerationError), reason)
		assert.Empty(t, apps)
	})

	t.Run("a per-parameter render error skips only that application", func(t *testing.T) {
		appset := argov1alpha1.ApplicationSet{
			ObjectMeta: metav1ObjectMeta("my-appset", nil),
			Spec: argov1alpha1.ApplicationSetSpec{
				GoTemplate: true,
				Generators: []argov1alpha1.ApplicationSetGenerator{
					{
						List: &argov1alpha1.ListGenerator{
							Elements: []apiextensionsv1.JSON{
								listElement(t, `{"name": "good-app"}`),
								listElement(t, `{"name": "bad-app"}`),
							},
						},
					},
				},
				Template: argov1alpha1.ApplicationSetTemplate{
					ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
						// references a field that only exists on one of the two elements
						Name: `{{if eq .name "bad-app"}}{{.name.nested.missing}}{{else}}{{.name}}{{end}}`,
					},
					Spec: baseApplicationSpec(),
				},
			},
		}
		appset.Namespace = "argocd"

		apps, reason, err := generateApplications(appset, getGenerators(fakeClient, "argocd"), fakeClient)
		require.Error(t, err)
		assert.Equal(t, argov1alpha1.ApplicationSetReasonType(argov1alpha1.ApplicationSetReasonRenderTemplateParamsError), reason)
		require.Len(t, apps, 1)
		assert.Equal(t, "good-app", apps[0].Name)
	})
}

func TestGenerateApplicationSetApps(t *testing.T) {
	gen := New()

	t.Run("success", func(t *testing.T) {
		ctr := &container.Container{
			Config:        config.ServerConfig{ArgoCDNamespace: "argocd"},
			KubeClientSet: newFakeKubeClientSet(t),
		}

		appset := argov1alpha1.ApplicationSet{
			ObjectMeta: metav1ObjectMeta("my-appset", nil),
			Spec: argov1alpha1.ApplicationSetSpec{
				GoTemplate: true,
				Generators: []argov1alpha1.ApplicationSetGenerator{
					{
						List: &argov1alpha1.ListGenerator{
							Elements: []apiextensionsv1.JSON{
								listElement(t, `{"name": "app1"}`),
							},
						},
					},
				},
				Template: argov1alpha1.ApplicationSetTemplate{
					ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
						Name: "{{.name}}",
					},
					Spec: baseApplicationSpec(),
				},
			},
		}
		appset.Namespace = "team-namespace"

		apps, err := gen.GenerateApplicationSetApps(context.Background(), appset, ctr)
		require.NoError(t, err)
		require.Len(t, apps, 1)
		assert.Equal(t, "app1", apps[0].Name)
		assert.Equal(t, "team-namespace", apps[0].Namespace)
	})

	t.Run("error is wrapped and no applications are returned", func(t *testing.T) {
		ctr := &container.Container{
			Config:        config.ServerConfig{ArgoCDNamespace: "argocd"},
			KubeClientSet: newFakeKubeClientSet(t),
		}

		appset := argov1alpha1.ApplicationSet{
			ObjectMeta: metav1ObjectMeta("my-appset", nil),
			Spec: argov1alpha1.ApplicationSetSpec{
				GoTemplate: true,
				Generators: []argov1alpha1.ApplicationSetGenerator{
					{
						List: &argov1alpha1.ListGenerator{
							Elements: []apiextensionsv1.JSON{listElement(t, `not-json`)},
						},
					},
				},
				Template: argov1alpha1.ApplicationSetTemplate{
					ApplicationSetTemplateMeta: argov1alpha1.ApplicationSetTemplateMeta{
						Name: "{{.name}}",
					},
					Spec: baseApplicationSpec(),
				},
			},
		}
		appset.Namespace = "team-namespace"

		apps, err := gen.GenerateApplicationSetApps(context.Background(), appset, ctr)
		assert.Error(t, err)
		assert.Nil(t, apps)
	})
}
