package affected_apps

import (
	"context"

	"github.com/pkg/errors"
)

func NewMultiMatcher(matchers ...Matcher) Matcher {
	return MultiMatcher{matchers: matchers}
}

type MultiMatcher struct {
	matchers []Matcher
}

func (m MultiMatcher) AffectedApps(ctx context.Context, changeList []string, targetBranch string) (AffectedItems, error) {
	var total AffectedItems

	for index, matcher := range m.matchers {
		items, err := matcher.AffectedApps(ctx, changeList, targetBranch)
		if err != nil {
			return total, errors.Wrapf(err, "failed to find items in matcher #%d", index)
		}
		total = total.Union(items)
	}

	return total, nil
}
