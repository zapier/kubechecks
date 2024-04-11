package hooks

import (
	"sort"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type waveNum int32

var waveNumBits = 32

type groupedSyncWaves map[argocdSyncPhase]map[waveNum][]*unstructured.Unstructured

func (g groupedSyncWaves) addResource(phase argocdSyncPhase, wave waveNum, resource *unstructured.Unstructured) {
	syncWaves, ok := g[phase]
	if !ok {
		syncWaves = make(map[waveNum][]*unstructured.Unstructured)
		g[phase] = syncWaves
	}

	phaseResources := syncWaves[wave]
	phaseResources = append(phaseResources, resource)
	syncWaves[wave] = phaseResources
}

// include all hooks that argocd uses: https://argo-cd.readthedocs.io/en/stable/user-guide/helm/#helm-hooks
var sortedPhases = []argocdSyncPhase{
	PreSyncPhase,
	SyncPhase,
	PostSyncPhase,
	SyncFailPhase,
	PostDeletePhase,
}

// argocd sync phases: https://argo-cd.readthedocs.io/en/stable/user-guide/resource_hooks/#usage
type argocdSyncPhase string

const (
	PreSyncPhase    argocdSyncPhase = "PreSync"
	SyncPhase       argocdSyncPhase = "Sync"
	SkipPhase       argocdSyncPhase = "Skip"
	PostSyncPhase   argocdSyncPhase = "PostSync"
	SyncFailPhase   argocdSyncPhase = "SyncFail"
	PostDeletePhase argocdSyncPhase = "PostDelete"
)

type phaseWaveResources struct {
	phase argocdSyncPhase
	waves []waveResources
}

type waveResources struct {
	wave      waveNum
	resources []*unstructured.Unstructured
}

func (g groupedSyncWaves) getSortedPhasesAndWaves() []phaseWaveResources {
	var result []phaseWaveResources
	usedPhases := make(map[argocdSyncPhase]struct{})
	for _, phase := range sortedPhases {
		waves, ok := g[phase]
		if !ok {
			continue
		}

		usedPhases[phase] = struct{}{}

		var wavesNums []waveNum
		for wave := range waves {
			wavesNums = append(wavesNums, wave)
		}
		sort.Sort(byNum(wavesNums))

		pwr := phaseWaveResources{
			phase: phase,
		}

		for _, wave := range wavesNums {
			wr := waveResources{
				wave:      wave,
				resources: waves[wave],
			}

			pwr.waves = append(pwr.waves, wr)
		}

		result = append(result, pwr)
	}

	return result
}

type byNum []waveNum

func (b byNum) Len() int {
	return len(b)
}

func (b byNum) Less(i, j int) bool {
	return b[i] < b[j]
}

func (b byNum) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}
