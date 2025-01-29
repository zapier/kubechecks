package argo_client

import (
	"io"
	"os"
	"path/filepath"

	"github.com/pkg/errors"
	"github.com/zapier/kubechecks/pkg/kustomize"
	"sigs.k8s.io/kustomize/kyaml/filesys"
)

type processor struct {
	repoRoot string
	tempDir  string
	repoFS   filesys.FileSystem
}

func (p processor) AddDir(s string) error {
	return nil
}

func (p processor) AddFile(relPath string) error {
	absDepPath := filepath.Clean(filepath.Join(p.repoRoot, relPath))

	// Get relative path from repo root
	relPath, err := filepath.Rel(p.repoRoot, absDepPath)
	if err != nil {
		return errors.Wrapf(err, "failed to get relative path for %s", absDepPath)
	}

	// check if the file exists in the temp directory
	// skip copying if it exists
	tempPath := filepath.Join(p.tempDir, relPath)
	if _, err := os.Stat(tempPath); err == nil {
		return nil
	}

	dstdir := filepath.Dir(tempPath)
	if err := os.MkdirAll(dstdir, 0o777); err != nil {
		return errors.Wrap(err, "failed to make directories")
	}

	r, err := os.Open(absDepPath)
	if err != nil {
		return err
	}
	defer r.Close() // ignore error: file was opened read-only.

	w, err := os.Create(tempPath)
	if err != nil {
		return err
	}

	defer func() {
		// Report the error, if any, from Close, but do so
		// only if there isn't already an outgoing error.
		if c := w.Close(); err == nil {
			err = c
		}
	}()

	_, err = io.Copy(w, r)
	return errors.Wrap(err, "failed to copy file")
}

var _ kustomize.Processor = new(processor)
