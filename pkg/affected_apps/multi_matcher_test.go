package affected_apps

import (
	"context"
	"testing"

	"github.com/argoproj/argo-cd/v2/pkg/apis/application/v1alpha1"
	"github.com/stretchr/testify/require"
	"github.com/zapier/kubechecks/pkg/git"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeMatcher struct {
	items AffectedItems
}

func (f fakeMatcher) AffectedApps(ctx context.Context, changeList []string, targetBranch string, repo *git.Repo) (AffectedItems, error) {
	return f.items, nil
}

func TestMultiMatcher(t *testing.T) {
	t.Run("exists in one but not two", func(t *testing.T) {
		app1 := v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app-1"}}
		matcher1 := fakeMatcher{
			items: AffectedItems{
				Applications: []v1alpha1.Application{app1},
			},
		}
		matcher2 := fakeMatcher{}

		ctx := context.Background()
		matcher := NewMultiMatcher(matcher1, matcher2)
		total, err := matcher.AffectedApps(ctx, nil, "", nil)

		require.NoError(t, err)
		require.Len(t, total.Applications, 1)
		require.Equal(t, app1, total.Applications[0])
	})

	t.Run("exists in two but not one", func(t *testing.T) {
		app1 := v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app-1"}}
		matcher1 := fakeMatcher{}
		matcher2 := fakeMatcher{
			items: AffectedItems{
				Applications: []v1alpha1.Application{app1},
			},
		}

		ctx := context.Background()
		matcher := NewMultiMatcher(matcher1, matcher2)
		total, err := matcher.AffectedApps(ctx, nil, "", nil)

		require.NoError(t, err)
		require.Len(t, total.Applications, 1)
		require.Equal(t, app1, total.Applications[0])
	})

	t.Run("exists in both", func(t *testing.T) {
		app1 := v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app-1"}}
		matcher1 := fakeMatcher{
			items: AffectedItems{
				Applications: []v1alpha1.Application{app1},
			},
		}
		matcher2 := fakeMatcher{
			items: AffectedItems{
				Applications: []v1alpha1.Application{app1},
			},
		}

		ctx := context.Background()
		matcher := NewMultiMatcher(matcher1, matcher2)
		total, err := matcher.AffectedApps(ctx, nil, "", nil)

		require.NoError(t, err)
		require.Len(t, total.Applications, 1)
		require.Equal(t, app1, total.Applications[0])
	})

	t.Run("each contains unique app", func(t *testing.T) {
		app1 := v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app-1"}}
		app2 := v1alpha1.Application{ObjectMeta: v1.ObjectMeta{Name: "app-2"}}
		matcher1 := fakeMatcher{
			items: AffectedItems{
				Applications: []v1alpha1.Application{app1},
			},
		}
		matcher2 := fakeMatcher{
			items: AffectedItems{
				Applications: []v1alpha1.Application{app2},
			},
		}

		ctx := context.Background()
		matcher := NewMultiMatcher(matcher1, matcher2)
		total, err := matcher.AffectedApps(ctx, nil, "", nil)

		require.NoError(t, err)
		require.Len(t, total.Applications, 2)
		require.Equal(t, app1, total.Applications[0])
		require.Equal(t, app2, total.Applications[1])
	})
}
