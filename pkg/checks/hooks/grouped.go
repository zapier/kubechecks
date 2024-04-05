package hooks

import (
	"sort"

	"github.com/argoproj/gitops-engine/pkg/sync/common"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type waveNum int32

var waveNumBits = 32

type groupedSyncWaves map[common.HookType]map[waveNum][]*unstructured.Unstructured

func (g groupedSyncWaves) addResource(phase common.HookType, wave waveNum, resource *unstructured.Unstructured) {
	syncWaves, ok := g[phase]
	if !ok {
		syncWaves = make(map[waveNum][]*unstructured.Unstructured)
		g[phase] = syncWaves
	}

	phaseResources, ok := syncWaves[wave]
	phaseResources = append(phaseResources, resource)
	syncWaves[wave] = phaseResources
}

var sortedPhases = []common.HookType{
	"PreSync",
	"Sync",
	"PostSync",
	"SyncFail",
	"PostDelete",
}

type phaseWaveResources struct {
	phase     common.HookType
	wave      waveNum
	resources []*unstructured.Unstructured
}

func (g groupedSyncWaves) getSortedPhasesAndWaves() []phaseWaveResources {
	var result []phaseWaveResources
	for _, phase := range sortedPhases {
		waves, ok := g[phase]
		if !ok {
			continue
		}

		var wavesNums []waveNum
		for wave := range waves {
			wavesNums = append(wavesNums, wave)
		}
		sort.Sort(byNum(wavesNums))

		for _, wave := range wavesNums {
			pwr := phaseWaveResources{
				phase:     phase,
				wave:      wave,
				resources: waves[wave],
			}

			result = append(result, pwr)
		}
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
