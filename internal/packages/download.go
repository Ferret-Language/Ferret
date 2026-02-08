package packages

// download.go - Package download logic with mock support

import (
	"compiler/internal/manifest"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// DownloadRemotePackage downloads a remote package using Git refs or mock source
func DownloadRemotePackage(cachePath, repoName, version string, mockConfig *manifest.DevConfig) error {
	// If mock mode is enabled, copy from local directory
	if mockConfig != nil && mockConfig.MockRemote && mockConfig.MockPath != "" {
		return downloadFromMock(cachePath, repoName, version, mockConfig.MockPath)
	}

	// TODO: Implement real Git refs-based download
	// 1. Fetch refs from GitHub
	// 2. Find matching tag/version
	// 3. Download archive
	// 4. Extract to cache
	// 5. Validate fer.ret exists

	return fmt.Errorf("remote package download not yet implemented (use mock mode for testing)")
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
	// TODO: Implement Git refs parsing
	// Parse packet-line format to extract tags

	return nil, fmt.Errorf("version fetching not yet implemented")
}

// ListAvailableVersions lists available versions for a package (mock or real)
func ListAvailableVersions(repoName string, mockConfig *manifest.DevConfig) ([]string, error) {
	// If mock mode, list versions from local directories
	if mockConfig != nil && mockConfig.MockRemote && mockConfig.MockPath != "" {
		return listMockVersions(repoName, mockConfig.MockPath)
	}

	// TODO: Fetch from real Git refs
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
