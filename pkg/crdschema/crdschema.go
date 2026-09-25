// Package crdschema collects the schemas out of CustomResourceDefinitions found in a git
// checkout and lays them out as files named the way kubeconform's local schema registry
// expects.
//
// It exists so that a pull request can add a CRD and an instance of that CRD in the
// same commit and have the instance validated against the definition it ships with,
// rather than against whatever version of the CRD happens to be published elsewhere.
//
// Each openAPIV3Schema is written out as-is: OpenAPI 3.0's schema object is a subset of
// JSON Schema draft-04, the dialect kubeconform compiles with, so no translation is
// needed. Schemas kubeconform would fail to compile are dropped rather than rewritten;
// see compiles.
package crdschema

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog"
	k8syaml "k8s.io/apimachinery/pkg/util/yaml"
)

// crdKind is the only kind we care about; it doubles as the substring we look for to
// rule a file out before paying to parse it.
const crdKind = "CustomResourceDefinition"

// defaultMaxFileSize bounds how large a file we are willing to read while searching for
// CRDs. Manifests are small; anything much larger is a data file that happens to end
// in .yaml.
const defaultMaxFileSize = 4 << 20 // 4MiB

// skipDirs are never descended into. Vendored trees do hold real CRDs, so only
// directories that cannot usefully contain manifests are listed here.
var skipDirs = map[string]struct{}{
	".git":         {},
	"node_modules": {},
}

var manifestExtensions = map[string]struct{}{
	".yaml": {},
	".yml":  {},
	".json": {},
}

// Options controls how a checkout is searched for CRDs.
type Options struct {
	// RepoDir is the root of the checkout to search.
	RepoDir string

	// Paths optionally narrows the search to these directories, relative to RepoDir.
	// Empty means the whole checkout.
	Paths []string

	// MaxFileSize bounds the size of a file that will be read; zero selects a default.
	MaxFileSize int64
}

// Schema describes one generated schema file: a single version of a single CRD.
type Schema struct {
	Kind       string
	Group      string
	Version    string
	SourceFile string // path of the CRD manifest, relative to the repository root
}

// APIVersion renders the apiVersion a resource must declare to be checked against
// this schema.
func (s Schema) APIVersion() string {
	return s.Group + "/" + s.Version
}

// Schemas is a directory of generated schema files plus a description of what went into
// it. The zero value is usable and yields no schema locations.
type Schemas struct {
	dir     string
	schemas []Schema
	byGVK   map[string]Schema
}

// Location returns the templated path to hand kubeconform as a schema location, or an
// empty string when nothing was generated. The template mirrors the one kubeconform's
// registry expands per resource; it deliberately omits the Kubernetes version, because
// a CRD's schema does not vary with the version of the cluster it is installed on.
func (s *Schemas) Location() string {
	if s == nil || len(s.schemas) == 0 {
		return ""
	}
	return filepath.Join(s.dir, "{{ .ResourceKind }}{{ .KindSuffix }}.json")
}

// Locations returns Location as a slice — empty when nothing was generated — so callers
// can splice it into a list of configured locations without a nil check.
func (s *Schemas) Locations() []string {
	if location := s.Location(); location != "" {
		return []string{location}
	}
	return nil
}

// Schemas lists what was generated, sorted, for reporting back to the pull request.
func (s *Schemas) Schemas() []Schema {
	if s == nil {
		return nil
	}
	return s.schemas
}

// Lookup finds the schema generated for a resource's apiVersion and kind, reporting
// whether this commit is what a validator would have resolved that kind against.
func (s *Schemas) Lookup(apiVersion, kind string) (Schema, bool) {
	if s == nil {
		return Schema{}, false
	}
	schema, ok := s.byGVK[apiVersion+"/"+kind]
	return schema, ok
}

// Cleanup removes the generated directory. It is safe to call on a nil or empty result,
// and safe to call more than once.
func (s *Schemas) Cleanup() {
	if s == nil || s.dir == "" {
		return
	}
	_ = os.RemoveAll(s.dir)
	s.dir = ""
	s.schemas = nil
	s.byGVK = nil
}

// Extract walks a checkout and writes the schema of every CustomResourceDefinition it
// finds to a temporary directory. The caller owns that directory and must Cleanup the
// result.
//
// A manifest that cannot be parsed is skipped rather than failing the extraction: a
// repository is free to contain Helm templates and fixtures that are not valid YAML on
// their own, and refusing to validate anything because of one of them would be worse
// than validating what we can.
func Extract(ctx context.Context, logger zerolog.Logger, opts Options) (*Schemas, error) {
	if opts.MaxFileSize <= 0 {
		opts.MaxFileSize = defaultMaxFileSize
	}

	repoDir, roots, unusable, err := resolveRoots(opts)
	if err != nil {
		return nil, err
	}
	opts.RepoDir = repoDir

	for _, path := range unusable {
		logger.Warn().Str("path", path).
			Msg("configured CRD schema path is not a directory in this commit, skipping it")
	}

	outputDir, err := os.MkdirTemp("", "kubechecks-repo-crds-")
	if err != nil {
		return nil, errors.Wrap(err, "failed to create directory for generated CRD schemas")
	}

	e := extractor{
		logger:    logger,
		opts:      opts,
		outputDir: outputDir,
		claimedBy: make(map[string]Schema),
	}

	for _, root := range roots {
		if err := e.walk(ctx, root); err != nil {
			e.result().Cleanup()
			return nil, err
		}
	}

	// Finding nothing is legitimate, but it is also what a mistyped path, a directory of
	// something other than CRDs, and a CRD we failed to parse all look like from outside.
	// Report enough to tell them apart without turning on debug logging.
	if len(e.schemas) == 0 {
		logger.Warn().
			Strs("searched", relativeTo(repoDir, roots)).
			Int("files_considered", e.considered).
			Int("files_mentioning_crds", e.candidates).
			Msg("no CustomResourceDefinitions found in the configured CRD schema paths")
	}

	return e.result(), nil
}

// relativeTo renders the walked roots as repository-relative paths, so the log echoes
// back something the reader can compare against what they configured.
func relativeTo(repoDir string, roots []string) []string {
	out := make([]string, 0, len(roots))
	for _, root := range roots {
		relative, err := filepath.Rel(repoDir, root)
		if err != nil {
			relative = root
		}
		out = append(out, relative)
	}
	return out
}

type extractor struct {
	logger    zerolog.Logger
	opts      Options
	outputDir string

	// claimedBy maps a generated filename to the CRD that produced it, so that a kind
	// defined twice in one checkout resolves to whichever copy we saw first rather than
	// to whichever the walk happened to reach last.
	claimedBy map[string]Schema
	schemas   []Schema

	// considered counts the files whose extension and size made them worth reading;
	// candidates counts the subset that mentioned a CRD. Together they separate "the
	// path was wrong" from "nothing there is a CRD" from "a CRD failed to parse".
	considered int
	candidates int
}

func (e *extractor) result() *Schemas {
	sort.Slice(e.schemas, func(i, j int) bool {
		if e.schemas[i].Group != e.schemas[j].Group {
			return e.schemas[i].Group < e.schemas[j].Group
		}
		if e.schemas[i].Kind != e.schemas[j].Kind {
			return e.schemas[i].Kind < e.schemas[j].Kind
		}
		return e.schemas[i].Version < e.schemas[j].Version
	})
	byGVK := make(map[string]Schema, len(e.schemas))
	for _, schema := range e.schemas {
		byGVK[schema.APIVersion()+"/"+schema.Kind] = schema
	}

	return &Schemas{dir: e.outputDir, schemas: e.schemas, byGVK: byGVK}
}

func (e *extractor) walk(ctx context.Context, root string) error {
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			// An unreadable entry is not a reason to abandon the rest of the tree.
			e.logger.Debug().Err(err).Str("path", path).Msg("skipping unreadable path")
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}

		if entry.IsDir() {
			if _, skip := skipDirs[entry.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}

		// Symlinks are not followed: WalkDir does not descend into them, and a symlinked
		// manifest is reachable through its real path anyway.
		if !entry.Type().IsRegular() {
			return nil
		}

		if _, ok := manifestExtensions[strings.ToLower(filepath.Ext(path))]; !ok {
			return nil
		}

		info, err := entry.Info()
		if err != nil || info.Size() == 0 || info.Size() > e.opts.MaxFileSize {
			return nil
		}

		e.considered++

		contents, err := os.ReadFile(path)
		if err != nil {
			e.logger.Debug().Err(err).Str("path", path).Msg("skipping unreadable file")
			return nil
		}

		// The overwhelming majority of files in a manifest repository are not CRDs.
		// Checking for the kind as a substring first keeps the walk close to the cost
		// of reading the tree.
		if !bytes.Contains(contents, []byte(crdKind)) {
			return nil
		}

		e.candidates++
		e.processFile(path, contents)
		return nil
	})

	return errors.Wrapf(err, "failed to search %q for CRDs", root)
}

func (e *extractor) processFile(path string, contents []byte) {
	relative, err := filepath.Rel(e.opts.RepoDir, path)
	if err != nil {
		relative = path
	}

	decoder := k8syaml.NewYAMLOrJSONDecoder(bytes.NewReader(contents), 4096)
	for {
		var document map[string]any
		if err := decoder.Decode(&document); err != nil {
			if !errors.Is(err, io.EOF) {
				e.logger.Debug().Err(err).Str("path", relative).Msg("skipping unparseable manifest")
			}
			return
		}

		for _, crd := range crdsInDocument(document) {
			e.processCRD(relative, crd)
		}
	}
}

// crdsInDocument returns the CRDs in a decoded document, unwrapping a List if it holds
// them. Bundles of CRDs distributed as a single file are commonly wrapped this way.
func crdsInDocument(document map[string]any) []map[string]any {
	if document == nil {
		return nil
	}

	if kind, _ := document["kind"].(string); kind == crdKind {
		return []map[string]any{document}
	}

	items, ok := document["items"].([]any)
	if !ok {
		return nil
	}

	var crds []map[string]any
	for _, item := range items {
		if crd, ok := item.(map[string]any); ok {
			if kind, _ := crd["kind"].(string); kind == crdKind {
				crds = append(crds, crd)
			}
		}
	}
	return crds
}

func (e *extractor) processCRD(sourceFile string, crd map[string]any) {
	spec, _ := crd["spec"].(map[string]any)
	if spec == nil {
		return
	}

	group, _ := spec["group"].(string)
	names, _ := spec["names"].(map[string]any)
	kind, _ := names["kind"].(string)
	if group == "" || kind == "" {
		e.logger.Debug().Str("path", sourceFile).Msg("skipping CRD without a group or kind")
		return
	}

	// v1beta1 CRDs may carry one schema for every version, under spec.validation; v1
	// requires a schema per version. Either may be present, and a v1beta1 CRD that
	// predates the `versions` list names its single version in spec.version.
	sharedSchema, _ := valueAt(spec, "validation", "openAPIV3Schema")

	versions, _ := spec["versions"].([]any)
	if len(versions) == 0 {
		if version, _ := spec["version"].(string); version != "" && sharedSchema != nil {
			e.writeSchema(sourceFile, kind, group, version, sharedSchema)
		}
		return
	}

	for _, entry := range versions {
		version, _ := entry.(map[string]any)
		if version == nil {
			continue
		}

		name, _ := version["name"].(string)
		if name == "" {
			continue
		}

		schema, _ := valueAt(version, "schema", "openAPIV3Schema")
		if schema == nil {
			schema = sharedSchema
		}
		if schema == nil {
			// A structural schema is required in v1, but not in v1beta1. Without one
			// there is nothing to validate against, so leave the kind to whichever
			// schema location comes next.
			e.logger.Debug().
				Str("path", sourceFile).Str("kind", kind).Str("version", name).
				Msg("skipping CRD version without an openAPIV3Schema")
			continue
		}

		e.writeSchema(sourceFile, kind, group, name, schema)
	}
}

func (e *extractor) writeSchema(sourceFile, kind, group, version string, openAPISchema any) {
	filename := SchemaFileName(kind, group, version)

	schema := Schema{Kind: kind, Group: group, Version: version, SourceFile: sourceFile}
	if existing, claimed := e.claimedBy[filename]; claimed {
		if existing.SourceFile != sourceFile {
			e.logger.Warn().
				Str("kind", kind).Str("apiVersion", schema.APIVersion()).
				Str("using", existing.SourceFile).Str("ignoring", sourceFile).
				Msg("CRD defined more than once in the repository")
		}
		return
	}

	// The openAPIV3Schema is written out as-is. OpenAPI 3.0's schema object is a subset
	// of JSON Schema draft-04, which is the dialect kubeconform compiles with, so the two
	// agree without translation; the handful of places they diverge are rare enough in
	// practice not to be worth a rewriting pass.
	encoded, err := json.MarshalIndent(openAPISchema, "", "  ")
	if err != nil {
		e.logger.Warn().Err(err).Str("kind", kind).Str("path", sourceFile).
			Msg("failed to encode generated CRD schema")
		return
	}

	if err := compiles(filename, encoded); err != nil {
		e.logger.Warn().Err(err).Str("kind", kind).Str("path", sourceFile).
			Msg("CRD schema does not compile, falling back to the other schema locations for this kind")
		return
	}

	if err := os.WriteFile(filepath.Join(e.outputDir, filename), encoded, 0o644); err != nil {
		e.logger.Warn().Err(err).Str("kind", kind).Str("path", sourceFile).
			Msg("failed to write generated CRD schema")
		return
	}

	e.claimedBy[filename] = schema
	e.schemas = append(e.schemas, schema)
	e.logger.Debug().
		Str("kind", kind).Str("apiVersion", schema.APIVersion()).
		Str("path", sourceFile).Str("schema", filename).
		Msg("generated schema from repository CRD")
}

// SchemaFileName reproduces the filename kubeconform's registry asks its schema location
// for, given a resource's kind and apiVersion. kubeconform lowercases the kind and
// appends the first label of the API group and the version, so a Widget in
// example.com/v1 is looked up as widget-example-v1.json.
func SchemaFileName(kind, group, version string) string {
	groupLabel := group
	if index := strings.Index(groupLabel, "."); index >= 0 {
		groupLabel = groupLabel[:index]
	}

	return strings.ToLower(kind + "-" + groupLabel + "-" + version + ".json")
}

// valueAt walks a chain of map keys, returning nil if any link is missing.
func valueAt(node map[string]any, keys ...string) (any, bool) {
	var current any = node
	for _, key := range keys {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[key]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

// resolveRoots returns the absolute repository root along with the directories to walk.
//
// Configured paths that are not directories in this checkout are reported separately
// rather than walked. They are not an error — a path can legitimately exist on some
// branches and not others — but they are the likeliest reason for finding no CRDs at
// all, so the caller surfaces them instead of letting the walk silently do nothing.
func resolveRoots(opts Options) (repoDir string, roots []string, unusable []string, err error) {
	repoDir, err = filepath.Abs(opts.RepoDir)
	if err != nil {
		return "", nil, nil, errors.Wrapf(err, "failed to resolve %q", opts.RepoDir)
	}

	if len(opts.Paths) == 0 {
		return repoDir, []string{repoDir}, nil, nil
	}

	for _, path := range opts.Paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}

		root := filepath.Join(repoDir, filepath.FromSlash(path))
		// filepath.Join cleans its result, so a configured path of "../../etc" would
		// otherwise walk out of the checkout entirely.
		if root != repoDir && !strings.HasPrefix(root, repoDir+string(filepath.Separator)) {
			return "", nil, nil, errors.Errorf("crd schema path %q escapes the repository", path)
		}

		switch info, statErr := os.Stat(root); {
		case statErr != nil:
			unusable = append(unusable, path)
		case !info.IsDir():
			unusable = append(unusable, path)
		default:
			roots = append(roots, root)
		}
	}

	if len(roots) == 0 && len(unusable) == 0 {
		return repoDir, []string{repoDir}, nil, nil
	}
	return repoDir, roots, unusable, nil
}
