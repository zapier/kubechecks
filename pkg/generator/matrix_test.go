package generator

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	argoprojiov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
)

func TestMatrixGenerate(t *testing.T) {

	listGenerator := &argoprojiov1alpha1.ListGenerator{
		Elements: []apiextensionsv1.JSON{{Raw: []byte(`{"cluster": "Cluster","url": "Url", "templated": "test-{{path.basenameNormalized}}"}`)}},
	}

	testCases := []struct {
		name           string
		baseGenerators []argoprojiov1alpha1.ApplicationSetNestedGenerator
		expectedErr    error
		expected       []map[string]interface{}
	}{
		{
			name: "happy flow - generate params from two lists",
			baseGenerators: []argoprojiov1alpha1.ApplicationSetNestedGenerator{
				{
					List: &argoprojiov1alpha1.ListGenerator{
						Elements: []apiextensionsv1.JSON{
							{Raw: []byte(`{"a": "1"}`)},
							{Raw: []byte(`{"a": "2"}`)},
						},
					},
				},
				{
					List: &argoprojiov1alpha1.ListGenerator{
						Elements: []apiextensionsv1.JSON{
							{Raw: []byte(`{"b": "1"}`)},
							{Raw: []byte(`{"b": "2"}`)},
						},
					},
				},
			},
			expected: []map[string]interface{}{
				{"a": "1", "b": "1"},
				{"a": "1", "b": "2"},
				{"a": "2", "b": "1"},
				{"a": "2", "b": "2"},
			},
		},
		{
			name: "returns error if there is more than two base generators",
			baseGenerators: []argoprojiov1alpha1.ApplicationSetNestedGenerator{
				{
					List: listGenerator,
				},
				{
					List: listGenerator,
				},
				{
					List: listGenerator,
				},
			},
			expectedErr: ErrMoreThanTwoGenerators,
		},
	}

	for _, testCase := range testCases {
		testCaseCopy := testCase // Since tests may run in parallel

		t.Run(testCaseCopy.name, func(t *testing.T) {
			genMock := &generatorMock{}
			appSet := &argoprojiov1alpha1.ApplicationSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "set",
				},
				Spec: argoprojiov1alpha1.ApplicationSetSpec{},
			}

			for _, g := range testCaseCopy.baseGenerators {
				gitGeneratorSpec := argoprojiov1alpha1.ApplicationSetGenerator{
					List: g.List,
				}
				genMock.On("GenerateParams", mock.AnythingOfType("*v1alpha1.ApplicationSetGenerator"), appSet, mock.Anything).Return([]map[string]interface{}{
					{
						"path":                    "app1",
						"path.basename":           "app1",
						"path.basenameNormalized": "app1",
					},
					{
						"path":                    "app2",
						"path.basename":           "app2",
						"path.basenameNormalized": "app2",
					},
				}, nil)

				genMock.On("GetTemplate", &gitGeneratorSpec).
					Return(&argoprojiov1alpha1.ApplicationSetTemplate{})
			}

			matrixGenerator := NewMatrixGenerator(
				map[string]Generator{
					"List": &ListGenerator{},
				},
			)

			got, err := matrixGenerator.GenerateParams(&argoprojiov1alpha1.ApplicationSetGenerator{
				Matrix: &argoprojiov1alpha1.MatrixGenerator{
					Generators: testCaseCopy.baseGenerators,
					Template:   argoprojiov1alpha1.ApplicationSetTemplate{},
				},
			}, appSet)

			if testCaseCopy.expectedErr != nil {
				require.ErrorIs(t, err, testCaseCopy.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCaseCopy.expected, got)
			}
		})
	}
}

func TestMatrixGenerateGoTemplate(t *testing.T) {

	listGenerator := &argoprojiov1alpha1.ListGenerator{
		Elements: []apiextensionsv1.JSON{{Raw: []byte(`{"cluster": "Cluster","url": "Url"}`)}},
	}

	testCases := []struct {
		name           string
		baseGenerators []argoprojiov1alpha1.ApplicationSetNestedGenerator
		expectedErr    error
		expected       []map[string]interface{}
	}{
		{
			name: "happy flow - generate params from two lists",
			baseGenerators: []argoprojiov1alpha1.ApplicationSetNestedGenerator{
				{
					List: &argoprojiov1alpha1.ListGenerator{
						Elements: []apiextensionsv1.JSON{
							{Raw: []byte(`{"a": "1"}`)},
							{Raw: []byte(`{"a": "2"}`)},
						},
					},
				},
				{
					List: &argoprojiov1alpha1.ListGenerator{
						Elements: []apiextensionsv1.JSON{
							{Raw: []byte(`{"b": "1"}`)},
							{Raw: []byte(`{"b": "2"}`)},
						},
					},
				},
			},
			expected: []map[string]interface{}{
				{"a": "1", "b": "1"},
				{"a": "1", "b": "2"},
				{"a": "2", "b": "1"},
				{"a": "2", "b": "2"},
			},
		},
		{
			name: "parameter override: first list elements take precedence",
			baseGenerators: []argoprojiov1alpha1.ApplicationSetNestedGenerator{
				{
					List: &argoprojiov1alpha1.ListGenerator{
						Elements: []apiextensionsv1.JSON{
							{Raw: []byte(`{"booleanFalse": false, "booleanTrue": true, "stringFalse": "false", "stringTrue": "true"}`)},
						},
					},
				},
				{
					List: &argoprojiov1alpha1.ListGenerator{
						Elements: []apiextensionsv1.JSON{
							{Raw: []byte(`{"booleanFalse": true, "booleanTrue": false, "stringFalse": "true", "stringTrue": "false"}`)},
						},
					},
				},
			},
			expected: []map[string]interface{}{
				{"booleanFalse": false, "booleanTrue": true, "stringFalse": "false", "stringTrue": "true"},
			},
		},
		{
			name: "returns error if there is more than two base generators",
			baseGenerators: []argoprojiov1alpha1.ApplicationSetNestedGenerator{
				{
					List: listGenerator,
				},
				{
					List: listGenerator,
				},
				{
					List: listGenerator,
				},
			},
			expectedErr: ErrMoreThanTwoGenerators,
		},
	}

	for _, testCase := range testCases {
		testCaseCopy := testCase // Since tests may run in parallel

		t.Run(testCaseCopy.name, func(t *testing.T) {
			genMock := &generatorMock{}
			appSet := &argoprojiov1alpha1.ApplicationSet{
				ObjectMeta: metav1.ObjectMeta{
					Name: "set",
				},
				Spec: argoprojiov1alpha1.ApplicationSetSpec{
					GoTemplate: true,
				},
			}

			for _, g := range testCaseCopy.baseGenerators {
				gitGeneratorSpec := argoprojiov1alpha1.ApplicationSetGenerator{
					List: g.List,
				}
				genMock.On("GenerateParams", mock.AnythingOfType("*v1alpha1.ApplicationSetGenerator"), appSet, mock.Anything).Return([]map[string]interface{}{
					{
						"path": map[string]string{
							"path":               "app1",
							"basename":           "app1",
							"basenameNormalized": "app1",
						},
					},
					{
						"path": map[string]string{
							"path":               "app2",
							"basename":           "app2",
							"basenameNormalized": "app2",
						},
					},
				}, nil)

				genMock.On("GetTemplate", &gitGeneratorSpec).
					Return(&argoprojiov1alpha1.ApplicationSetTemplate{})
			}

			matrixGenerator := NewMatrixGenerator(
				map[string]Generator{
					"List": &ListGenerator{},
				},
			)

			got, err := matrixGenerator.GenerateParams(&argoprojiov1alpha1.ApplicationSetGenerator{
				Matrix: &argoprojiov1alpha1.MatrixGenerator{
					Generators: testCaseCopy.baseGenerators,
					Template:   argoprojiov1alpha1.ApplicationSetTemplate{},
				},
			}, appSet)

			if testCaseCopy.expectedErr != nil {
				require.ErrorIs(t, err, testCaseCopy.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, testCaseCopy.expected, got)
			}
		})
	}
}

type generatorMock struct {
	mock.Mock
}

func (g *generatorMock) GetTemplate(appSetGenerator *argoprojiov1alpha1.ApplicationSetGenerator) *argoprojiov1alpha1.ApplicationSetTemplate {
	args := g.Called(appSetGenerator)

	return args.Get(0).(*argoprojiov1alpha1.ApplicationSetTemplate)
}

func (g *generatorMock) GenerateParams(appSetGenerator *argoprojiov1alpha1.ApplicationSetGenerator, appSet *argoprojiov1alpha1.ApplicationSet, _ client.Client) ([]map[string]interface{}, error) {
	args := g.Called(appSetGenerator, appSet)

	return args.Get(0).([]map[string]interface{}), args.Error(1)
}

func (g *generatorMock) GetRequeueAfter(appSetGenerator *argoprojiov1alpha1.ApplicationSetGenerator) time.Duration {
	args := g.Called(appSetGenerator)

	return args.Get(0).(time.Duration)
}
