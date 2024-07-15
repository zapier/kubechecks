package generator

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/argoproj/argo-cd/v2/applicationset/utils"
	argov1alpha1 "github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/zapier/kubechecks/pkg/container"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GenerateApplicationSetApps(ctx context.Context, appset argov1alpha1.ApplicationSet, ctr *container.Container) ([]argov1alpha1.Application, error) {

	appSetGenerators := getGenerators(ctx, *ctr.KubeClientSet.ControllerClient(), ctr.KubeClientSet.ClientSet(), ctr.Config.ArgoCDNamespace)

	apps, appsetReason, err := generateApplications(appset, appSetGenerators)
	if err != nil {
		fmt.Printf("error generating applications: %v, appset reason: %v", err, appsetReason)
		return nil, fmt.Errorf("error generating applications: %w", err)
	}
	return apps, nil
}

// GetGenerators returns the generators that will be used to generate applications for the ApplicationSet
//
// only support List and Clusters generators
func getGenerators(ctx context.Context, c client.Client, k8sClient kubernetes.Interface, namespace string) map[string]Generator {

	terminalGenerators := map[string]Generator{
		"List":     NewListGenerator(),
		"Clusters": NewClusterGenerator(c, ctx, k8sClient, namespace),
	}

	nestedGenerators := map[string]Generator{
		"List":     terminalGenerators["List"],
		"Clusters": terminalGenerators["Clusters"],
		"Matrix":   NewMatrixGenerator(terminalGenerators),
		"Merge":    NewMergeGenerator(terminalGenerators),
	}

	topLevelGenerators := map[string]Generator{
		"List":     terminalGenerators["List"],
		"Clusters": terminalGenerators["Clusters"],
		"Matrix":   NewMatrixGenerator(nestedGenerators),
		"Merge":    NewMergeGenerator(nestedGenerators),
	}
	return topLevelGenerators
}

// generateApplications generates applications from the ApplicationSet
func generateApplications(applicationSetInfo argov1alpha1.ApplicationSet, g map[string]Generator) (
	[]argov1alpha1.Application, argov1alpha1.ApplicationSetReasonType, error,
) {
	var res []argov1alpha1.Application
	renderer := &utils.Render{}
	var firstError error
	var applicationSetReason argov1alpha1.ApplicationSetReasonType

	for _, requestedGenerator := range applicationSetInfo.Spec.Generators {
		t, err := Transform(requestedGenerator, g, applicationSetInfo.Spec.Template, &applicationSetInfo, map[string]interface{}{})
		if err != nil {
			if firstError == nil {
				firstError = err
				applicationSetReason = argov1alpha1.ApplicationSetReasonApplicationParamsGenerationError
			}
			continue
		}

		for _, a := range t {
			tmplApplication := getTempApplication(a.Template)

			for _, p := range a.Params {
				app, err := renderer.RenderTemplateParams(tmplApplication, applicationSetInfo.Spec.SyncPolicy, p, applicationSetInfo.Spec.GoTemplate, applicationSetInfo.Spec.GoTemplateOptions)
				if err != nil {
					//logCtx.WithError(err).WithField("params", a.Params).WithField("generator", requestedGenerator).
					//	Error("error generating application from params")

					if firstError == nil {
						firstError = err
						applicationSetReason = argov1alpha1.ApplicationSetReasonRenderTemplateParamsError
					}
					continue
				}

				if applicationSetInfo.Spec.TemplatePatch != nil {
					patchedApplication, err := renderTemplatePatch(renderer, app, applicationSetInfo, p)
					if err != nil {
						if firstError == nil {
							firstError = err
							applicationSetReason = argov1alpha1.ApplicationSetReasonRenderTemplateParamsError
						}
						continue
					}

					app = patchedApplication
				}

				// The app's namespace must be the same as the AppSet's namespace to preserve the appsets-in-any-namespace
				// security boundary.
				app.Namespace = applicationSetInfo.Namespace
				res = append(res, *app)
			}
		}

		//logCtx.WithField("generator", requestedGenerator).Infof("generated %d applications", len(res))
		//logCtx.WithField("generator", requestedGenerator).Debugf("apps from generator: %+v", res)
	}

	return res, applicationSetReason, firstError
}

func renderTemplatePatch(r utils.Renderer, app *argov1alpha1.Application, applicationSetInfo argov1alpha1.ApplicationSet, params map[string]interface{}) (*argov1alpha1.Application, error) {
	replacedTemplate, err := r.Replace(*applicationSetInfo.Spec.TemplatePatch, params, applicationSetInfo.Spec.GoTemplate, applicationSetInfo.Spec.GoTemplateOptions)
	if err != nil {
		return nil, fmt.Errorf("error replacing values in templatePatch: %w", err)
	}

	return applyTemplatePatch(app, replacedTemplate)
}

func getTempApplication(applicationSetTemplate argov1alpha1.ApplicationSetTemplate) *argov1alpha1.Application {
	tmplApplication := argov1alpha1.Application{}
	tmplApplication.Annotations = applicationSetTemplate.Annotations
	tmplApplication.Labels = applicationSetTemplate.Labels
	tmplApplication.Namespace = applicationSetTemplate.Namespace
	tmplApplication.Name = applicationSetTemplate.Name
	tmplApplication.Spec = applicationSetTemplate.Spec
	tmplApplication.Finalizers = applicationSetTemplate.Finalizers
	tmplApplication.APIVersion = "argoproj.io/v1alpha1"
	tmplApplication.Kind = "Application"
	return &tmplApplication
}

func applyTemplatePatch(app *argov1alpha1.Application, templatePatch string) (*argov1alpha1.Application, error) {

	appString, err := json.Marshal(app)
	if err != nil {
		return nil, fmt.Errorf("error while marhsalling Application %w", err)
	}

	convertedTemplatePatch, err := utils.ConvertYAMLToJSON(templatePatch)

	if err != nil {
		return nil, fmt.Errorf("error while converting template to json %q: %w", convertedTemplatePatch, err)
	}

	if err := json.Unmarshal([]byte(convertedTemplatePatch), &argov1alpha1.Application{}); err != nil {
		return nil, fmt.Errorf("invalid templatePatch %q: %w", convertedTemplatePatch, err)
	}

	data, err := strategicpatch.StrategicMergePatch(appString, []byte(convertedTemplatePatch), argov1alpha1.Application{})

	if err != nil {
		return nil, fmt.Errorf("error while applying templatePatch template to json %q: %w", convertedTemplatePatch, err)
	}

	finalApp := argov1alpha1.Application{}
	err = json.Unmarshal(data, &finalApp)
	if err != nil {
		return nil, fmt.Errorf("error while unmarhsalling patched application: %w", err)
	}

	// Prevent changes to the `project` field. This helps prevent malicious template patches
	finalApp.Spec.Project = app.Spec.Project

	return &finalApp, nil
}
