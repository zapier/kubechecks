package generator

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"

	"github.com/argoproj/argo-cd/v3/util/settings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/argoproj/argo-cd/v3/applicationset/utils"
	argoappsetv1alpha1 "github.com/argoproj/argo-cd/v3/pkg/apis/application/v1alpha1"
)

const (
	ArgoCDSecretTypeLabel   = "argocd.argoproj.io/secret-type"
	ArgoCDSecretTypeCluster = "cluster"
)

var _ Generator = (*ClusterGenerator)(nil)

// ClusterGenerator generates Applications for some or all clusters registered with ArgoCD.
type ClusterGenerator struct {
	client.Client
	ctx       context.Context
	clientset kubernetes.Interface
	// namespace is the Argo CD namespace
	namespace       string
	settingsManager *settings.SettingsManager
}

var render = &utils.Render{}

func NewClusterGenerator(c client.Client, ctx context.Context, clientset kubernetes.Interface, namespace string) Generator {

	settingsManager := settings.NewSettingsManager(ctx, clientset, namespace)

	g := &ClusterGenerator{
		Client:          c,
		ctx:             ctx,
		clientset:       clientset,
		namespace:       namespace,
		settingsManager: settingsManager,
	}
	return g
}

// GetRequeueAfter never requeue the cluster generator because the `clusterSecretEventHandler` will requeue the appsets
// when the cluster secrets change
func (g *ClusterGenerator) GetRequeueAfter(_ *argoappsetv1alpha1.ApplicationSetGenerator) time.Duration {
	return NoRequeueAfter
}

func (g *ClusterGenerator) GetTemplate(appSetGenerator *argoappsetv1alpha1.ApplicationSetGenerator) *argoappsetv1alpha1.ApplicationSetTemplate {
	return &appSetGenerator.Clusters.Template
}

func (g *ClusterGenerator) GenerateParams(appSetGenerator *argoappsetv1alpha1.ApplicationSetGenerator, appSet *argoappsetv1alpha1.ApplicationSet) ([]map[string]interface{}, error) {

	if appSetGenerator == nil {
		return nil, EmptyAppSetGeneratorError
	}

	if appSetGenerator.Clusters == nil {
		return nil, EmptyAppSetGeneratorError
	}

	// Do not include the local cluster in the cluster parameters IF there is a non-empty selector
	// - Since local clusters do not have secrets, they do not have labels to match against
	ignoreLocalClusters := len(appSetGenerator.Clusters.Selector.MatchExpressions) > 0 || len(appSetGenerator.Clusters.Selector.MatchLabels) > 0

	// ListCluster from Argo CD's util/db package will include the local cluster in the list of clusters
	clustersFromArgoCD, err := utils.ListClusters(g.ctx, g.clientset, g.namespace)
	if err != nil {
		return nil, fmt.Errorf("error listing clusters: %w", err)
	}

	if clustersFromArgoCD == nil {
		return nil, nil
	}

	clusterSecrets, err := g.getSecretsByClusterName(appSetGenerator)
	if err != nil {
		return nil, err
	}

	var res []map[string]interface{}

	var secretsFound []corev1.Secret

	for _, cluster := range clustersFromArgoCD {

		// If there is a secret for this cluster, then it's a non-local cluster, so it will be
		// handled by the next step.
		if secretForCluster, exists := clusterSecrets[cluster.Name]; exists {
			secretsFound = append(secretsFound, secretForCluster)

		} else if !ignoreLocalClusters {
			// If there is no secret for the cluster, it's the local cluster, so handle it here.
			params := map[string]interface{}{}
			params["name"] = cluster.Name
			params["nameNormalized"] = cluster.Name
			params["server"] = cluster.Server

			err = appendTemplatedValues(appSetGenerator.Clusters.Values, params, appSet.Spec.GoTemplate, appSet.Spec.GoTemplateOptions)
			if err != nil {
				return nil, err
			}

			res = append(res, params)

			log.Info().Str("cluster", "local cluster").Msg("matched local cluster")
		}
	}

	// For each matching cluster secret (non-local clusters only)
	for _, cluster := range secretsFound {
		params := map[string]interface{}{}

		params["name"] = string(cluster.Data["name"])
		params["nameNormalized"] = utils.SanitizeName(string(cluster.Data["name"]))
		params["server"] = string(cluster.Data["server"])

		if appSet.Spec.GoTemplate {
			meta := map[string]interface{}{}

			if len(cluster.Annotations) > 0 {
				meta["annotations"] = cluster.Annotations
			}
			if len(cluster.Labels) > 0 {
				meta["labels"] = cluster.Labels
			}

			params["metadata"] = meta
		} else {
			for key, value := range cluster.Annotations {
				params[fmt.Sprintf("metadata.annotations.%s", key)] = value
			}

			for key, value := range cluster.Labels {
				params[fmt.Sprintf("metadata.labels.%s", key)] = value
			}
		}

		err = appendTemplatedValues(appSetGenerator.Clusters.Values, params, appSet.Spec.GoTemplate, appSet.Spec.GoTemplateOptions)
		if err != nil {
			return nil, err
		}

		res = append(res, params)

		log.Info().Msgf("matched cluster secret. \"cluster\": %s", cluster.Name)
	}

	return res, nil
}

func (g *ClusterGenerator) getSecretsByClusterName(appSetGenerator *argoappsetv1alpha1.ApplicationSetGenerator) (map[string]corev1.Secret, error) {
	// List all Clusters:
	clusterSecretList := &corev1.SecretList{}

	selector := metav1.AddLabelToSelector(&appSetGenerator.Clusters.Selector, ArgoCDSecretTypeLabel, ArgoCDSecretTypeCluster)
	secretSelector, err := metav1.LabelSelectorAsSelector(selector)
	if err != nil {
		return nil, err
	}

	if err := g.List(context.Background(), clusterSecretList, client.MatchingLabelsSelector{Selector: secretSelector}); err != nil {
		return nil, err
	}
	log.Debug().Msgf("clusters matching labels, count: %d", len(clusterSecretList.Items))

	res := map[string]corev1.Secret{}

	for _, cluster := range clusterSecretList.Items {
		clusterName := string(cluster.Data["name"])

		res[clusterName] = cluster
	}

	return res, nil

}
