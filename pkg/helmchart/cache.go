package helmchart

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/ghodss/yaml"
	"github.com/rs/zerolog/log"
	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
)

// Cache manages downloaded and extracted Helm charts on disk.
type Cache struct {
	cacheDir string
	mu       sync.Mutex
}

// NewCache creates a new chart cache at the given directory.
func NewCache(cacheDir string) (*Cache, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return nil, fmt.Errorf("failed to create chart cache dir %s: %w", cacheDir, err)
	}
	return &Cache{cacheDir: cacheDir}, nil
}

// EnsureChart downloads and extracts a chart if not already cached.
// Returns the path to the extracted chart directory.
func (c *Cache) EnsureChart(repoURL, chart, version string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	chartDir, err := c.chartPath(chart, version)
	if err != nil {
		return "", err
	}

	// Check if already cached
	if info, err := os.Stat(chartDir); err == nil && info.IsDir() {
		log.Debug().Caller().Str("chart", chart).Str("version", version).Msg("chart cache hit")
		return chartDir, nil
	}

	log.Info().Str("chart", chart).Str("version", version).Str("repo", repoURL).Msg("downloading chart")

	// Create a temp dir for the pull, then move to cache
	tmpDir, err := os.MkdirTemp(c.cacheDir, "pull-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp dir: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(tmpDir); err != nil {
			log.Error().Err(err).Msg("failed to remove temp dir")
		}
	}()
	if err := c.pullChart(repoURL, chart, version, tmpDir); err != nil {
		return "", fmt.Errorf("failed to pull chart %s@%s: %w", chart, version, err)
	}

	// The pull extracts to tmpDir/<chart-name>/, move it to the cache
	extractedDir := filepath.Join(tmpDir, chart)
	if _, err := os.Stat(extractedDir); err != nil {
		// Some charts extract with a different name, find the first directory
		entries, readErr := os.ReadDir(tmpDir)
		if readErr != nil {
			return "", fmt.Errorf("failed to read extracted chart: %w", readErr)
		}
		found := false
		for _, entry := range entries {
			if entry.IsDir() {
				extractedDir = filepath.Join(tmpDir, entry.Name())
				found = true
				break
			}
		}
		if !found {
			return "", fmt.Errorf("no chart directory found after extracting %s@%s", chart, version)
		}
	}

	// Ensure parent dir exists and move
	if err := os.MkdirAll(filepath.Dir(chartDir), 0o755); err != nil {
		return "", fmt.Errorf("failed to create cache dir: %w", err)
	}
	if err := os.Rename(extractedDir, chartDir); err != nil {
		return "", fmt.Errorf("failed to move chart to cache: %w", err)
	}

	log.Info().Str("chart", chart).Str("version", version).Str("path", chartDir).Msg("chart cached")
	return chartDir, nil
}

// ListFiles returns all file paths relative to the chart root.
func (c *Cache) ListFiles(chart, version string) ([]string, error) {
	chartDir, err := c.chartPath(chart, version)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(chartDir); err != nil {
		return nil, fmt.Errorf("chart %s@%s not cached", chart, version)
	}

	var files []string
	err = filepath.Walk(chartDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			rel, relErr := filepath.Rel(chartDir, path)
			if relErr != nil {
				return fmt.Errorf("failed to compute relative path for %s: %w", path, relErr)
			}
			files = append(files, rel)
		}
		return nil
	})
	return files, err
}

// ReadFile reads a file from a cached chart.
func (c *Cache) ReadFile(chart, version, path string) (string, error) {
	chartDir, err := c.chartPath(chart, version)
	if err != nil {
		return "", err
	}

	// Prevent path traversal
	fullPath := filepath.Join(chartDir, filepath.Clean(path))
	if !strings.HasPrefix(fullPath, chartDir+string(filepath.Separator)) {
		return "", fmt.Errorf("invalid path: %s", path)
	}

	data, err := os.ReadFile(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("file %q not found in chart %s@%s", path, chart, version)
		}
		return "", fmt.Errorf("failed to read %s: %w", path, err)
	}
	return string(data), nil
}

func (c *Cache) chartPath(chart, version string) (string, error) {
	// Sanitize to prevent path traversal via chart/version values
	cleanChart := filepath.Clean(chart)
	cleanVersion := filepath.Clean(version)
	if cleanChart != chart || cleanVersion != version ||
		filepath.IsAbs(cleanChart) || filepath.IsAbs(cleanVersion) ||
		strings.Contains(cleanChart, "..") || strings.Contains(cleanVersion, "..") {
		return "", fmt.Errorf("invalid chart %q or version %q: path traversal detected", chart, version)
	}
	return filepath.Join(c.cacheDir, cleanChart, cleanVersion), nil
}

func (c *Cache) pullChart(repoURL, chart, version, destDir string) error {
	settings := cli.New()

	pull := action.NewPullWithOpts(action.WithConfig(&action.Configuration{}))
	pull.Settings = settings
	pull.Untar = true
	pull.UntarDir = destDir
	pull.Version = version

	// For OCI repos, the chart ref is the full OCI URL
	var chartRef string
	if registry.IsOCI(repoURL) {
		chartRef = fmt.Sprintf("%s/%s", strings.TrimSuffix(repoURL, "/"), chart)

		// Set up OCI registry client
		regClient, err := registry.NewClient()
		if err != nil {
			return fmt.Errorf("failed to create registry client: %w", err)
		}
		pull.SetRegistryClient(regClient)
	} else {
		// For HTTP repos, set RepoURL and use chart name as ref
		pull.RepoURL = repoURL
		chartRef = chart
	}

	_, err := pull.Run(chartRef)
	return err
}

// ChartDependency represents a dependency from Chart.yaml.
type ChartDependency struct {
	Name       string `json:"name" yaml:"name"`
	Version    string `json:"version" yaml:"version"`
	Repository string `json:"repository" yaml:"repository"`
}

// chartYAML is the minimal structure of a Chart.yaml for parsing dependencies.
type chartYAML struct {
	Dependencies []ChartDependency `json:"dependencies" yaml:"dependencies"`
}

// ParseDependencies reads a Chart.yaml file and returns its dependencies.
func ParseDependencies(chartYAMLPath string) ([]ChartDependency, error) {
	data, err := os.ReadFile(chartYAMLPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read Chart.yaml: %w", err)
	}

	var chart chartYAML
	if err := yaml.Unmarshal(data, &chart); err != nil {
		return nil, fmt.Errorf("failed to parse Chart.yaml: %w", err)
	}

	return chart.Dependencies, nil
}
