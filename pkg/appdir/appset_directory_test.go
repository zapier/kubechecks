package appdir

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/assert"
	"github.com/zapier/kubechecks/pkg/git"
	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAppSetDirectory_ProcessApp(t *testing.T) {

	type args struct {
		app v1alpha1.ApplicationSet
	}
	tests := []struct {
		name     string
		input    *AppSetDirectory
		args     args
		expected *AppSetDirectory
	}{
		{
			name:  "normal process, expect to get the appset stored in the map",
			input: NewAppSetDirectory(),
			args: args{
				app: v1alpha1.ApplicationSet{
					ObjectMeta: metav1.ObjectMeta{
						Name: "test-appset",
					},
					Spec: v1alpha1.ApplicationSetSpec{
						Template: v1alpha1.ApplicationSetTemplate{
							Spec: v1alpha1.ApplicationSpec{
								Source: &v1alpha1.ApplicationSource{
									Path: "/test1/test2",
									Helm: &v1alpha1.ApplicationSourceHelm{
										ValueFiles: []string{"one.yaml", "./two.yaml", "../three.yaml"},
										FileParameters: []v1alpha1.HelmFileParameter{
											{Name: "one", Path: "one.json"},
											{Name: "two", Path: "./two.json"},
											{Name: "three", Path: "../three.json"},
										},
									},
								},
							},
						},
					},
				},
			},
			expected: &AppSetDirectory{
				appSetDirs: map[string][]string{"/test1/test2": {"test-appset"}},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &AppSetDirectory{
				appSetDirs:  tt.input.appSetDirs,
				appSetFiles: tt.input.appSetFiles,
				appSetsMap:  tt.input.appSetsMap,
			}
			d.ProcessApp(tt.args.app)
			assert.Equal(t, tt.expected.appSetDirs, d.appSetDirs)
		})
	}
}

func TestAppSetDirectory_FindAppsBasedOnChangeList(t *testing.T) {

	tests := []struct {
		name         string
		changeList   []string
		targetBranch string
		mockFiles    map[string]string // Mock file content
		expected     []v1alpha1.ApplicationSet
	}{
		{
			name: "Valid ApplicationSet",
			changeList: []string{
				"appsets/httpdump/valid-appset.yaml",
			},
			targetBranch: "main",
			mockFiles: map[string]string{
				"appsets/httpdump/valid-appset.yaml": `
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: httpdump
  namespace: kubechecks
spec:
  generators:
    # this is a simple list generator
    - list:
        elements:
          - name: a
            url:  https://kubernetes.default.svc
  template:
    metadata:
      finalizers:
        - resources-finalizer.argocd.argoproj.io
      name: "in-cluster-{{ name }}-httpdump"
      namespace: kubechecks
      labels:
        argocd.argoproj.io/application-set-name: "httpdump"
    spec:
      destination:
        namespace: "httpdump-{{ name }}"
        server: '{{ url }}'
      project: default
      source:
        repoURL: REPO_URL
        targetRevision: HEAD
        path: 'apps/httpdump/overlays/{{ name }}/'
      syncPolicy:
        automated:
          prune: true
        syncOptions:
          - CreateNamespace=true
      sources: []
                `,
			},
			expected: []v1alpha1.ApplicationSet{
				{
					TypeMeta: metav1.TypeMeta{
						APIVersion: "argoproj.io/v1alpha1",
						Kind:       "ApplicationSet",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "httpdump",
						Namespace: "kubechecks",
					},
					Spec: v1alpha1.ApplicationSetSpec{
						Template: v1alpha1.ApplicationSetTemplate{
							ApplicationSetTemplateMeta: v1alpha1.ApplicationSetTemplateMeta{
								Name:      "in-cluster-{{ name }}-httpdump",
								Namespace: "kubechecks",
								Labels: map[string]string{
									"argocd.argoproj.io/application-set-name": "httpdump",
								},
								Finalizers: []string{"resources-finalizer.argocd.argoproj.io"},
							},
							Spec: v1alpha1.ApplicationSpec{
								Source: &v1alpha1.ApplicationSource{
									RepoURL:        "REPO_URL",
									Path:           "apps/httpdump/overlays/{{ name }}/",
									TargetRevision: "HEAD",
								},
								Destination: v1alpha1.ApplicationDestination{
									Namespace: "httpdump-{{ name }}",
									Server:    "{{ url }}",
								},
								Project: "default",
								SyncPolicy: &v1alpha1.SyncPolicy{
									Automated: &v1alpha1.SyncPolicyAutomated{
										Prune: true,
									},
									SyncOptions: []string{"CreateNamespace=true"},
								},
								Sources: v1alpha1.ApplicationSources{},
							},
						},
						Generators: []v1alpha1.ApplicationSetGenerator{
							{
								List: &v1alpha1.ListGenerator{
									Elements:     []v1.JSON{{Raw: []byte("{\"name\":\"a\",\"url\":\"https://kubernetes.default.svc\"}")}},
									Template:     v1alpha1.ApplicationSetTemplate{},
									ElementsYaml: "",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "Invalid YAML File",
			changeList: []string{
				"invalid-appset.yaml",
			},
			targetBranch: "main",
			mockFiles: map[string]string{
				"appsets/httpdump/invalid-appset.yaml": "invalid yaml content",
			},
			expected: nil,
		},
		// Add more test cases as needed
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a temporary directory for the test
			tempDir := os.TempDir()
			var fatalErr error
			var cleanUpDirs []string
			for fileName, content := range tt.mockFiles {
				absPath := filepath.Join(tempDir, fileName)
				cleanUpDirs = append(cleanUpDirs, absPath)
				err := os.MkdirAll(filepath.Dir(absPath), 0755)
				if err != nil {
					fatalErr = err
					break
				}
				err = os.WriteFile(absPath, []byte(content), 0644)
				if err != nil {
					fatalErr = err
					break
				}
			}
			defer cleanUpTmpFiles(t, cleanUpDirs)
			if fatalErr != nil {
				t.Fatalf("failed to create tmp folder %s", fatalErr)
			}
			d := &AppSetDirectory{}
			result := d.FindAppsBasedOnChangeList(tt.changeList, tt.targetBranch, &git.Repo{Directory: tempDir})
			assert.Equal(t, tt.expected, result)
		})
	}
}

// cleanUpTmpFiles removes the temporary directories created for the test
func cleanUpTmpFiles(t *testing.T, cleanUpDirs []string) {
	for _, dir := range cleanUpDirs {
		if err := os.RemoveAll(filepath.Dir(dir)); err != nil {
			t.Fatalf("failed to remove tmp folder %s", err)
		}
	}
}
