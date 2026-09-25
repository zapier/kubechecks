package kubeconform

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/rs/zerolog/log"
	"github.com/yannh/kubeconform/pkg/validator"
	"go.opentelemetry.io/otel"

	"github.com/zapier/kubechecks/pkg"
	"github.com/zapier/kubechecks/pkg/container"
	"github.com/zapier/kubechecks/pkg/msg"
)

var tracer = otel.Tracer("pkg/checks/kubeconform")

func getSchemaLocations(ctr container.Container, repoDir string) []string {
	cfg := ctr.Config

	locations := []string{
		// schemas included in kubechecks
		"default",
	}

	// schemas configured globally, with relative ones resolved against the checkout
	locations = append(locations, resolveSchemaLocations(cfg.SchemasLocations, repoDir)...)

	for index := range locations {
		location := locations[index]
		oldLocation := location
		if location == "default" || strings.Contains(location, "{{") {
			log.Debug().Caller().Str("location", location).Msg("location requires no processing to be valid")
			continue
		}

		if !strings.HasSuffix(location, "/") {
			location += "/"
		}

		location += "{{ .NormalizedKubernetesVersion }}/{{ .ResourceKind }}{{ .KindSuffix }}.json"
		locations[index] = location

		log.Debug().Caller().Str("old", oldLocation).Str("new", location).Msg("processed schema location")
	}

	return locations
}

// resolveSchemaLocations roots relative schema locations at the repository being checked.
//
// An absolute path, an http(s) location, and a git remote — which by this point has
// already been cloned to a local directory — all address something outside the pull
// request, and are passed through untouched. A relative location instead names a
// directory committed to the repository under test, so that schemas kept alongside the
// manifests that use them are the ones a resource is checked against.
//
// A location the checkout cannot supply is dropped rather than handed to kubeconform,
// which would report the kinds it covers as having no schema at all.
func resolveSchemaLocations(configured []string, repoDir string) []string {
	var locations []string
	for _, location := range configured {
		location = strings.TrimSpace(location)
		if location == "" {
			continue
		}

		if filepath.IsAbs(location) || looksRemote(location) {
			locations = append(locations, location)
			continue
		}

		if resolved, ok := resolveInCheckout(location, repoDir); ok {
			locations = append(locations, resolved)
		}
	}

	return locations
}

// looksRemote reports whether a location addresses something other than the filesystem:
// an http(s) schema server, or a git remote in either URL or scp-like form. Neither can
// be relative to a checkout, however much the string looks like a path.
func looksRemote(location string) bool {
	if strings.Contains(location, "://") {
		return true
	}

	// scp-like git remotes, git@github.com:org/repo.git
	if at := strings.Index(location, "@"); at > 0 && strings.Contains(location[at:], ":") {
		return true
	}

	return false
}

// resolveInCheckout turns one repository-relative location into an absolute one.
//
// A location that already carries a template is rooted and otherwise left alone, so a
// repository whose schemas are named by some other convention can say so — the
// CRDs-catalog layout, for instance, which `openapi2jsonschema` also produces:
//
//	.github/schemas/{{ .ResourceKind }}_{{ .ResourceAPIVersion }}.json
//
// One without a template is a plain directory and is treated exactly as an absolute or
// remote location would be: the caller appends the Kubernetes version and the filename
// kubeconform's registry asks for. Schemas vendored into a repository are often organised
// by kind alone rather than by cluster version, and those should use a template.
func resolveInCheckout(location, repoDir string) (string, bool) {
	if repoDir == "" {
		log.Warn().Caller().Str("location", location).
			Msg("relative schema location needs a checkout to resolve against, ignoring it")
		return "", false
	}

	root, err := filepath.Abs(repoDir)
	if err != nil {
		log.Warn().Caller().Err(err).Str("dir", repoDir).Str("location", location).
			Msg("could not resolve the checkout, ignoring relative schema location")
		return "", false
	}

	// The directory the location names: the whole thing when it is a plain path, and
	// otherwise whatever precedes the template, with any partial filename dropped.
	// Configured values are slash-separated regardless of host, so path, not filepath.
	dir := location
	if index := strings.Index(location, "{{"); index >= 0 {
		dir = path.Dir(location[:index])
	}

	resolvedDir := filepath.Join(root, filepath.FromSlash(dir))
	// filepath.Join cleans its result, so a location of "../../etc" would otherwise read
	// schemas from outside the checkout.
	if resolvedDir != root && !strings.HasPrefix(resolvedDir, root+string(filepath.Separator)) {
		log.Warn().Caller().Str("location", location).
			Msg("relative schema location escapes the checkout, ignoring it")
		return "", false
	}

	// Naming a directory that is not in this commit is the likeliest way to get this
	// wrong, and contributes nothing without saying so.
	if info, err := os.Stat(resolvedDir); err != nil || !info.IsDir() {
		log.Warn().Caller().Str("location", location).Str("resolved", resolvedDir).
			Msg("relative schema location is not a directory in this commit, ignoring it")
		return "", false
	}

	resolved := filepath.Join(root, filepath.FromSlash(location))
	log.Debug().Caller().Str("location", location).Str("resolved", resolved).
		Msg("resolved schema location inside the repository under test")

	return resolved, true
}

func argoCdAppValidate(ctx context.Context, ctr container.Container, appName, targetKubernetesVersion, repoDir string, appManifests []string) (msg.Result, error) {
	_, span := tracer.Start(ctx, "ArgoCdAppValidate")
	defer span.End()

	log.Debug().
		Caller().
		Str("app_name", appName).
		Str("k8s_version", targetKubernetesVersion).
		Msg("ArgoCDAppValidate")

	schemaCachePath, err := os.MkdirTemp("", "kubechecks-schema-cache-")
	if err != nil {
		return msg.Result{}, errors.Wrap(err, "failed to create schema cache")
	}
	defer pkg.WipeDir(schemaCachePath)

	vOpts := validator.Opts{
		Cache:   schemaCachePath,
		SkipTLS: false,
		SkipKinds: map[string]struct{}{
			"apiextensions.k8s.io/v1/CustomResourceDefinition": {},
		},
		RejectKinds:          nil,
		KubernetesVersion:    targetKubernetesVersion,
		Strict:               true,
		IgnoreMissingSchemas: false,
		Debug:                log.Debug().Caller().Enabled(),
	}

	var (
		outputString    []string
		schemaLocations = getSchemaLocations(ctr, repoDir)
	)

	log.Debug().Caller().Msgf("cache location: %s", vOpts.Cache)
	log.Debug().Caller().Msgf("target kubernetes version: %s", targetKubernetesVersion)
	log.Debug().Caller().Msgf("schema locations: %s", strings.Join(schemaLocations, ", "))

	v, err := validator.New(schemaLocations, vOpts)
	if err != nil {
		return msg.Result{}, fmt.Errorf("could not create kubeconform validator: %v", err)
	}
	result := v.Validate("-", io.NopCloser(strings.NewReader(strings.Join(appManifests, "\n"))))
	var invalid, failedValidation bool
	for _, res := range result {
		sigData, _ := res.Resource.Signature()
		sig := fmt.Sprintf("%s %s %s", sigData.Version, sigData.Kind, sigData.Name)

		switch res.Status {
		case validator.Invalid:
			outputString = append(outputString, fmt.Sprintf(" * :warning: **Invalid**: %s", sig))
			outputString = append(outputString, fmt.Sprintf("   * %s ", res.Err))
			invalid = true
		case validator.Error:
			outputString = append(outputString, fmt.Sprintf(" * :red_circle: **Error**: %s - %v", sig, res.Err))
			failedValidation = true
		case validator.Empty:
			// noop
		case validator.Skipped:
			outputString = append(outputString, fmt.Sprintf(" * :right_arrow: Skipped: %s", sig))
		default:
			outputString = append(outputString, fmt.Sprintf(" * :white_check_mark: Passed: %s", sig))
		}
	}

	var cr msg.Result
	if invalid {
		cr.State = pkg.StateWarning
	} else if failedValidation {
		cr.State = pkg.StateFailure
	} else {
		cr.State = pkg.StateSuccess
	}

	cr.Summary = "<b>Show kubeconform report:</b>"
	cr.Details = fmt.Sprintf(">Validated against Kubernetes Version: %s\n\n%s", targetKubernetesVersion, strings.Join(outputString, "\n"))

	return cr, nil
}
