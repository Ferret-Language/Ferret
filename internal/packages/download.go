package packages

// download.go - Package download logic with mock support

import (
	"archive/tar"
	"compress/gzip"
	"compiler/internal/manifest"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DownloadRemotePackage downloads a remote package using Git refs or mock source
func DownloadRemotePackage(cachePath, repoName, version string, mockConfig *manifest.DevConfig) error {
	// If mock mode is enabled, copy from local directory
	if mockConfig != nil && mockConfig.MockRemote && mockConfig.MockPath != "" {
		return downloadFromMock(cachePath, repoName, version, mockConfig.MockPath)
	}

	// Real Git download
	return downloadFromGit(cachePath, repoName, version)
}

// downloadFromGit downloads a package from GitHub/GitLab/Bitbucket
func downloadFromGit(cachePath, repoName, version string) error {
	// Determine Git provider
	var downloadURL string
	
	if strings.HasPrefix(repoName, "github.com/") {
		// GitHub: https://github.com/user/repo/archive/refs/tags/v1.0.0.tar.gz
		parts := strings.TrimPrefix(repoName, "github.com/")
		downloadURL = fmt.Sprintf("https://github.com/%s/archive/refs/tags/%s.tar.gz", parts, version)
	} else if strings.HasPrefix(repoName, "gitlab.com/") {
		// GitLab: https://gitlab.com/user/repo/-/archive/v1.0.0/repo-v1.0.0.tar.gz
		parts := strings.TrimPrefix(repoName, "gitlab.com/")
		repoBase := filepath.Base(parts)
		downloadURL = fmt.Sprintf("https://gitlab.com/%s/-/archive/%s/%s-%s.tar.gz", parts, version, repoBase, version)
	} else if strings.HasPrefix(repoName, "bitbucket.org/") {
		// Bitbucket: https://bitbucket.org/user/repo/get/v1.0.0.tar.gz
		parts := strings.TrimPrefix(repoName, "bitbucket.org/")
		downloadURL = fmt.Sprintf("https://bitbucket.org/%s/get/%s.tar.gz", parts, version)
	} else {
		return fmt.Errorf("unsupported Git provider: %s", repoName)
	}

	// Download tarball
	tempFile, err := downloadFile(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download package: %w", err)
	}
	defer os.Remove(tempFile)

	// Extract to cache
	destPath := filepath.Join(cachePath, repoName+"@"+version)
	if err := extractTarGz(tempFile, destPath); err != nil {
		return fmt.Errorf("failed to extract package: %w", err)
	}

	// Validate fer.ret exists
	manifestPath := filepath.Join(destPath, "fer.ret")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		os.RemoveAll(destPath) // Clean up invalid package
		return fmt.Errorf("package missing fer.ret: %s", repoName)
	}

	return nil
}

// downloadFromMock copies a package from local mock directory to cache
func downloadFromMock(cachePath, repoName, version string, mockBasePath string) error {
	// Resolve mock base path to absolute
	mockBasePath, err := filepath.Abs(mockBasePath)
	if err != nil {
		return fmt.Errorf("cannot resolve mock path: %w", err)
	}

	// Convert github.com/user/repo to filesystem path
	// github.com/user/repo -> user/repo
	repoPath := repoName
	if idx := len("github.com/"); len(repoName) > idx {
		repoPath = repoName[idx:]
	} else if idx := len("gitlab.com/"); len(repoName) > idx {
		repoPath = repoName[idx:]
	} else if idx := len("bitbucket.org/"); len(repoName) > idx {
		repoPath = repoName[idx:]
	}

	// Try versioned directory first (e.g., logger-v1.0.0)
	packageName := filepath.Base(repoPath)
	packageDir := filepath.Dir(repoPath)
	versionedDir := packageName + "-" + version
	
	// Try with full domain path first (github.com/user/logger-v1.9.0)
	sourcePath := filepath.Join(mockBasePath, repoName)
	sourcePath = filepath.Join(filepath.Dir(sourcePath), versionedDir)
	
	// If not found, try without domain (user/logger-v1.9.0)
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		sourcePath = filepath.Join(mockBasePath, packageDir, versionedDir)
	}

	// If versioned directory doesn't exist, try base directory
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		sourcePath = filepath.Join(mockBasePath, repoPath)
	}

	// Destination: cachePath/github.com/user/repo@version
	destPath := filepath.Join(cachePath, repoName+"@"+version)

	// Check if source exists
	if _, err := os.Stat(sourcePath); os.IsNotExist(err) {
		return fmt.Errorf("mock package not found: %s (tried %s)", repoName, sourcePath)
	}

	// Create destination directory
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("cannot create cache directory: %w", err)
	}

	// Copy directory recursively
	if err := copyDir(sourcePath, destPath); err != nil {
		return fmt.Errorf("failed to copy mock package: %w", err)
	}

	// Validate fer.ret exists
	manifestPath := filepath.Join(destPath, "fer.ret")
	if _, err := os.Stat(manifestPath); os.IsNotExist(err) {
		return fmt.Errorf("mock package missing fer.ret: %s", repoName)
	}

	return nil
}

// copyDir recursively copies a directory
func copyDir(src, dst string) error {
	// Get source directory info
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}

	// Create destination directory
	if err := os.MkdirAll(dst, srcInfo.Mode()); err != nil {
		return err
	}

	// Read source directory
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			// Recursively copy subdirectory
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			// Copy file
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}

	return nil
}

// copyFile copies a single file
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	if _, err := io.Copy(dstFile, srcFile); err != nil {
		return err
	}

	// Copy permissions
	srcInfo, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, srcInfo.Mode())
}

// FetchAvailableVersions fetches available versions for a remote package
func FetchAvailableVersions(repoName string) ([]string, error) {
	// Determine Git provider and fetch tags
	if strings.HasPrefix(repoName, "github.com/") {
		return fetchGitHubVersions(repoName)
	} else if strings.HasPrefix(repoName, "gitlab.com/") {
		return fetchGitLabVersions(repoName)
	} else if strings.HasPrefix(repoName, "bitbucket.org/") {
		return fetchBitbucketVersions(repoName)
	}

	return nil, fmt.Errorf("unsupported Git provider: %s", repoName)
}

// fetchGitHubVersions fetches available versions from GitHub API
func fetchGitHubVersions(repoName string) ([]string, error) {
	// GitHub API: https://api.github.com/repos/user/repo/tags
	parts := strings.TrimPrefix(repoName, "github.com/")
	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/tags", parts)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions from GitHub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API returned status %d for %s", resp.StatusCode, repoName)
	}

	// Parse JSON response
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("failed to parse GitHub response: %w", err)
	}

	// Extract version names
	versions := make([]string, 0, len(tags))
	for _, tag := range tags {
		versions = append(versions, tag.Name)
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %s", repoName)
	}

	return versions, nil
}

// fetchGitLabVersions fetches available versions from GitLab API
func fetchGitLabVersions(repoName string) ([]string, error) {
	// GitLab API: https://gitlab.com/api/v4/projects/USER%2FREPO/repository/tags
	parts := strings.TrimPrefix(repoName, "gitlab.com/")
	// URL encode the project path
	encodedPath := strings.ReplaceAll(parts, "/", "%2F")
	apiURL := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/tags", encodedPath)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions from GitLab: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitLab API returned status %d for %s", resp.StatusCode, repoName)
	}

	// Parse JSON response
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, fmt.Errorf("failed to parse GitLab response: %w", err)
	}

	// Extract version names
	versions := make([]string, 0, len(tags))
	for _, tag := range tags {
		versions = append(versions, tag.Name)
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %s", repoName)
	}

	return versions, nil
}

// fetchBitbucketVersions fetches available versions from Bitbucket API
func fetchBitbucketVersions(repoName string) ([]string, error) {
	// Bitbucket API: https://api.bitbucket.org/2.0/repositories/user/repo/refs/tags
	parts := strings.TrimPrefix(repoName, "bitbucket.org/")
	apiURL := fmt.Sprintf("https://api.bitbucket.org/2.0/repositories/%s/refs/tags", parts)

	resp, err := http.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch versions from Bitbucket: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bitbucket API returned status %d for %s", resp.StatusCode, repoName)
	}

	// Parse JSON response
	var response struct {
		Values []struct {
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, fmt.Errorf("failed to parse Bitbucket response: %w", err)
	}

	// Extract version names
	versions := make([]string, 0, len(response.Values))
	for _, tag := range response.Values {
		versions = append(versions, tag.Name)
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %s", repoName)
	}

	return versions, nil
}

// downloadFile downloads a file from a URL to a temporary location
func downloadFile(url string) (string, error) {
	// Create temporary file
	tempFile, err := os.CreateTemp("", "ferret-download-*.tar.gz")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer tempFile.Close()

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	// Download file
	resp, err := client.Get(url)
	if err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Copy to temp file
	if _, err := io.Copy(tempFile, resp.Body); err != nil {
		os.Remove(tempFile.Name())
		return "", fmt.Errorf("failed to write download: %w", err)
	}

	return tempFile.Name(), nil
}

// extractTarGz extracts a tar.gz archive to a destination directory
func extractTarGz(archivePath, destPath string) error {
	// Open archive
	file, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("failed to open archive: %w", err)
	}
	defer file.Close()

	// Create gzip reader
	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	// Create tar reader
	tarReader := tar.NewReader(gzReader)

	// Extract files
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("failed to read tar: %w", err)
		}

		// Strip top-level directory (GitHub adds repo-version/ prefix)
		targetPath := header.Name
		if idx := strings.Index(targetPath, "/"); idx != -1 {
			targetPath = targetPath[idx+1:]
		}

		// Skip if empty after stripping
		if targetPath == "" {
			continue
		}

		// Create full path
		fullPath := filepath.Join(destPath, targetPath)

		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if err := os.MkdirAll(fullPath, 0755); err != nil {
				return fmt.Errorf("failed to create directory: %w", err)
			}
		case tar.TypeReg:
			// Create parent directory
			if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
				return fmt.Errorf("failed to create parent directory: %w", err)
			}

			// Create file
			outFile, err := os.Create(fullPath)
			if err != nil {
				return fmt.Errorf("failed to create file: %w", err)
			}

			// Copy contents
			if _, err := io.Copy(outFile, tarReader); err != nil {
				outFile.Close()
				return fmt.Errorf("failed to write file: %w", err)
			}
			outFile.Close()

			// Set permissions
			if err := os.Chmod(fullPath, os.FileMode(header.Mode)); err != nil {
				return fmt.Errorf("failed to set permissions: %w", err)
			}
		}
	}

	return nil
}

// ListAvailableVersions lists available versions for a package (mock or real)
func ListAvailableVersions(repoName string, mockConfig *manifest.DevConfig) ([]string, error) {
	// If mock mode, list versions from local directories
	if mockConfig != nil && mockConfig.MockRemote && mockConfig.MockPath != "" {
		return listMockVersions(repoName, mockConfig.MockPath)
	}

	// Fetch from real Git provider
	return FetchAvailableVersions(repoName)
}

// listMockVersions lists available versions from mock directory
func listMockVersions(repoName, mockBasePath string) ([]string, error) {
	// Resolve mock base path
	mockBasePath, err := filepath.Abs(mockBasePath)
	if err != nil {
		return nil, fmt.Errorf("cannot resolve mock path: %w", err)
	}

	// Convert github.com/user/repo to filesystem path
	repoPath := repoName
	if idx := len("github.com/"); len(repoName) > idx {
		repoPath = repoName[idx:]
	} else if idx := len("gitlab.com/"); len(repoName) > idx {
		repoPath = repoName[idx:]
	} else if idx := len("bitbucket.org/"); len(repoName) > idx {
		repoPath = repoName[idx:]
	}

	// Base path for this package
	packageBasePath := filepath.Join(mockBasePath, repoPath)

	// List directories that match package-vX.Y.Z pattern
	baseDir := filepath.Dir(packageBasePath)
	packageName := filepath.Base(repoPath)

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read mock directory: %w", err)
	}

	var versions []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		// Match packagename-vX.Y.Z or just vX.Y.Z directories
		if name == packageName {
			// Single version directory (exact package name)
			// Try to read version from fer.ret
			ferretPath := filepath.Join(baseDir, name, "fer.ret")
			if m, err := manifest.LoadManifest(ferretPath); err == nil {
				versions = append(versions, m.Package.Version)
			}
		} else if len(name) > len(packageName)+2 && name[:len(packageName)+1] == packageName+"-" {
			// Versioned directory like logger-v1.0.0
			version := name[len(packageName)+1:]
			versions = append(versions, version)
		}
	}

	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %s in mock directory", repoName)
	}

	return versions, nil
}
