package cmd

import (
	"github.com/pkg/errors"

	"github.com/zapier/kubechecks/pkg/checks"
	"github.com/zapier/kubechecks/pkg/checks/diff"
	"github.com/zapier/kubechecks/pkg/checks/kubeconform"
	"github.com/zapier/kubechecks/pkg/checks/preupgrade"
	"github.com/zapier/kubechecks/pkg/checks/rego"
	"github.com/zapier/kubechecks/pkg/container"
)

func getProcessors(ctr container.Container) ([]checks.ProcessorEntry, error) {
	var procs []checks.ProcessorEntry

	procs = append(procs, checks.ProcessorEntry{
		Name:      "validating app against schema",
		Processor: kubeconform.Check,
	})

	procs = append(procs, checks.ProcessorEntry{
		Name:      "generating diff for app",
		Processor: diff.Check,
	})

	procs = append(procs, checks.ProcessorEntry{
		Name:      "running pre-upgrade check",
		Processor: preupgrade.Check,
	})

	if ctr.Config.EnableConfTest {
		checker, err := rego.NewChecker(ctr.Config)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create rego checker")
		}

		procs = append(procs, checks.ProcessorEntry{
			Name:      "validation policy",
			Processor: checker.Check,
		})
	}

	return procs, nil
}
