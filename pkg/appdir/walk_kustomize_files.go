package appdir

import (
	"github.com/zapier/kubechecks/pkg/kustomize"
)

type processor struct {
	appName string
	result  *AppDirectory
}

func (p *processor) AddDir(dir string) error {
	p.result.addDir(p.appName, dir)
	return nil
}

func (p *processor) AddFile(file string) error {
	p.result.addFile(p.appName, file)
	return nil
}

var _ kustomize.Processor = new(processor)
