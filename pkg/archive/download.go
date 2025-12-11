package archive

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/pkg/errors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rs/zerolog/log"
)

// Downloader handles downloading and extracting archives
type Downloader struct {
	httpClient *http.Client
}

// NewDownloader creates a new archive downloader
func NewDownloader() *Downloader {
	return &Downloader{
		httpClient: &http.Client{
			Timeout: 0, // No timeout, let context handle it
		},
	}
}

// DownloadAndExtract downloads an archive from URL and extracts it to targetDir
// Returns the path to the extracted directory
func (d *Downloader) DownloadAndExtract(ctx context.Context, archiveURL, targetDir string, authHeaders map[string]string) (string, error) {
	log.Debug().
		Str("url", archiveURL).
		Str("target_dir", targetDir).
		Msg("downloading and extracting archive")

	// Download archive
	zipData, err := d.download(ctx, archiveURL, authHeaders)
	if err != nil {
		return "", errors.Wrap(err, "failed to download archive")
	}

	// Extract archive
	extractedPath, err := d.extract(zipData, targetDir)
	if err != nil {
		return "", errors.Wrap(err, "failed to extract archive")
	}

	log.Info().
		Str("url", archiveURL).
		Str("extracted_path", extractedPath).
		Msg("archive downloaded and extracted successfully")

	return extractedPath, nil
}

// download downloads archive from URL
func (d *Downloader) download(ctx context.Context, archiveURL string, authHeaders map[string]string) ([]byte, error) {
	archiveDownloadTotal.Inc()
	timer := prometheus.NewTimer(archiveDownloadDuration)
	defer timer.ObserveDuration()

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		archiveDownloadFailed.Inc()
		return nil, errors.Wrap(err, "failed to create request")
	}

	// Add authentication headers
	for key, value := range authHeaders {
		req.Header.Set(key, value)
	}

	// Execute request
	resp, err := d.httpClient.Do(req)
	if err != nil {
		archiveDownloadFailed.Inc()
		return nil, errors.Wrap(err, "failed to download archive")
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Msg("failed to close response body")
		}
	}()

	// Log response details for debugging
	log.Debug().
		Str("url", archiveURL).
		Int("status_code", resp.StatusCode).
		Str("content_type", resp.Header.Get("Content-Type")).
		Int64("content_length", resp.ContentLength).
		Msg("received HTTP response")

	// Read response body first (needed for both success and error cases)
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		archiveDownloadFailed.Inc()
		return nil, errors.Wrap(err, "failed to read response body")
	}

	// Log actual data received (first 16 bytes as hex for debugging)
	previewLen := 16
	if len(data) < previewLen {
		previewLen = len(data)
	}
	log.Debug().
		Str("url", archiveURL).
		Int("bytes_read", len(data)).
		Str("first_bytes_hex", fmt.Sprintf("%x", data[:previewLen])).
		Msg("read response body")

	// Check status code
	if resp.StatusCode != http.StatusOK {
		archiveDownloadFailed.Inc()
		// Include response body snippet in error for debugging (first 500 chars)
		bodySnippet := string(data)
		if len(bodySnippet) > 500 {
			bodySnippet = bodySnippet[:500] + "..."
		}
		return nil, fmt.Errorf("HTTP %d %s - URL: %s - Response: %s",
			resp.StatusCode, http.StatusText(resp.StatusCode), archiveURL, bodySnippet)
	}

	archiveDownloadSuccess.Inc()
	archiveDownloadSizeBytes.Observe(float64(len(data)))

	log.Debug().
		Str("url", archiveURL).
		Int("size_bytes", len(data)).
		Msg("archive downloaded")

	return data, nil
}

// extract extracts zip archive to target directory
// Returns the path to the extracted content (strips top-level directory if present)
func (d *Downloader) extract(zipData []byte, targetDir string) (string, error) {
	archiveExtractTotal.Inc()
	timer := prometheus.NewTimer(archiveExtractDuration)
	defer timer.ObserveDuration()

	// Create temp file for zip data
	tmpFile, err := os.CreateTemp("", "kubechecks-archive-*.zip")
	if err != nil {
		archiveExtractFailed.Inc()
		return "", errors.Wrap(err, "failed to create temp file")
	}
	defer func() {
		if removeErr := os.Remove(tmpFile.Name()); removeErr != nil {
			log.Warn().Err(removeErr).Str("file", tmpFile.Name()).Msg("failed to remove temp file")
		}
	}()
	// Note: We explicitly close tmpFile below (before opening zip reader), so no defer close needed

	// Write zip data to temp file
	if _, err := tmpFile.Write(zipData); err != nil {
		archiveExtractFailed.Inc()
		return "", errors.Wrap(err, "failed to write zip data")
	}

	// Close temp file so we can read it
	if err := tmpFile.Close(); err != nil {
		archiveExtractFailed.Inc()
		return "", errors.Wrap(err, "failed to close temp file")
	}

	// Open zip file for reading
	zipReader, err := zip.OpenReader(tmpFile.Name())
	if err != nil {
		archiveExtractFailed.Inc()
		return "", errors.Wrap(err, "failed to open zip file")
	}
	defer func() {
		if closeErr := zipReader.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Msg("failed to close zip reader")
		}
	}()

	// Create target directory
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		archiveExtractFailed.Inc()
		return "", errors.Wrap(err, "failed to create target directory")
	}

	// Track top-level directory (GitHub/GitLab archives have a top-level folder)
	var topLevelDir string
	fileCount := 0

	// Extract all files
	for _, file := range zipReader.File {
		fileCount++

		// Track top-level directory
		if topLevelDir == "" {
			parts := strings.SplitN(file.Name, "/", 2)
			if len(parts) > 0 && file.FileInfo().IsDir() {
				topLevelDir = parts[0]
			}
		}

		// Extract file
		if err := d.extractFile(file, targetDir); err != nil {
			archiveExtractFailed.Inc()
			return "", errors.Wrapf(err, "failed to extract file: %s", file.Name)
		}
	}

	archiveExtractSuccess.Inc()

	log.Debug().
		Str("target_dir", targetDir).
		Int("file_count", fileCount).
		Str("top_level_dir", topLevelDir).
		Msg("archive extracted")

	// Return path to actual content (strip top-level directory)
	if topLevelDir != "" {
		return filepath.Join(targetDir, topLevelDir), nil
	}
	return targetDir, nil
}

// extractFile extracts a single file from zip archive
func (d *Downloader) extractFile(file *zip.File, targetDir string) error {
	// Create full path
	path := filepath.Join(targetDir, file.Name)

	// Prevent path traversal
	if !strings.HasPrefix(path, filepath.Clean(targetDir)+string(os.PathSeparator)) {
		return fmt.Errorf("invalid file path (path traversal detected): %s", file.Name)
	}

	// Create directory for file
	if file.FileInfo().IsDir() {
		return os.MkdirAll(path, file.Mode())
	}

	// Create parent directories
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	// Open file in zip
	srcFile, err := file.Open()
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := srcFile.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Str("file", file.Name).Msg("failed to close source file")
		}
	}()

	// Create destination file
	dstFile, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := dstFile.Close(); closeErr != nil {
			log.Warn().Err(closeErr).Str("path", path).Msg("failed to close destination file")
		}
	}()

	// Copy contents
	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	return nil
}
