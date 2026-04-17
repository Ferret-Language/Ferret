package packages

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"compiler/internal/core/manifest"
)

func DownloadRemotePackage(cachePath, repoName, version string, devConfig *manifest.DevConfig) error {
	if devConfig != nil && devConfig.MockRemote && devConfig.MockPath != "" {
		return downloadFromMock(cachePath, repoName, version, devConfig.MockPath)
	}
	return downloadFromGit(cachePath, repoName, version)
}

func ListAvailableVersions(repoName string, devConfig *manifest.DevConfig) ([]string, error) {
	if devConfig != nil && devConfig.MockRemote && devConfig.MockPath != "" {
		return listMockVersions(repoName, devConfig.MockPath)
	}
	return fetchAvailableVersions(repoName)
}

func downloadFromGit(cachePath, repoName, version string) error {
	url, err := packageArchiveURL(repoName, version)
	if err != nil {
		return err
	}
	tempPath, err := downloadFile(url)
	if err != nil {
		return err
	}
	defer os.Remove(tempPath)

	dest := GetModulePath(cachePath, repoName, version)
	if err := extractTarGz(tempPath, dest); err != nil {
		return err
	}
	manifestPath := filepath.Join(dest, manifest.FileName)
	if _, err := os.Stat(manifestPath); err != nil {
		_ = os.RemoveAll(dest)
		return fmt.Errorf("downloaded package missing %s", manifest.FileName)
	}
	return nil
}

func packageArchiveURL(repoName, version string) (string, error) {
	if after, ok := strings.CutPrefix(repoName, "github.com/"); ok {
		parts := after
		return fmt.Sprintf("https://github.com/%s/archive/refs/tags/%s.tar.gz", parts, version), nil
	}
	if after, ok := strings.CutPrefix(repoName, "gitlab.com/"); ok {
		parts := after
		repoBase := filepath.Base(parts)
		return fmt.Sprintf("https://gitlab.com/%s/-/archive/%s/%s-%s.tar.gz", parts, version, repoBase, version), nil
	}
	if after, ok := strings.CutPrefix(repoName, "bitbucket.org/"); ok {
		parts := after
		return fmt.Sprintf("https://bitbucket.org/%s/get/%s.tar.gz", parts, version), nil
	}
	return "", fmt.Errorf("unsupported remote host for %s", repoName)
}

func downloadFromMock(cachePath, repoName, version, mockBasePath string) error {
	mockBasePath, err := filepath.Abs(mockBasePath)
	if err != nil {
		return fmt.Errorf("resolve mock path: %w", err)
	}
	repoPath := stripProviderPrefix(repoName)
	packageName := filepath.Base(repoPath)
	packageDir := filepath.Dir(repoPath)
	versionedDir := packageName + "-" + version

	source := filepath.Join(mockBasePath, filepath.Dir(repoName), versionedDir)
	if _, err := os.Stat(source); os.IsNotExist(err) {
		source = filepath.Join(mockBasePath, packageDir, versionedDir)
	}
	if _, err := os.Stat(source); os.IsNotExist(err) {
		source = filepath.Join(mockBasePath, repoPath)
	}
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("mock package not found for %s", repoName)
	}

	dest := GetModulePath(cachePath, repoName, version)
	if err := copyDir(source, dest); err != nil {
		return err
	}
	manifestPath := filepath.Join(dest, manifest.FileName)
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("mock package missing %s", manifest.FileName)
	}
	return nil
}

func listMockVersions(repoName, mockBasePath string) ([]string, error) {
	mockBasePath, err := filepath.Abs(mockBasePath)
	if err != nil {
		return nil, fmt.Errorf("resolve mock path: %w", err)
	}
	repoPath := stripProviderPrefix(repoName)
	baseDir := filepath.Join(mockBasePath, filepath.Dir(repoPath))
	packageName := filepath.Base(repoPath)

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("read mock directory: %w", err)
	}
	versions := make([]string, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if strings.HasPrefix(name, packageName+"-") {
			versions = append(versions, strings.TrimPrefix(name, packageName+"-"))
		}
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no mock versions found for %s", repoName)
	}
	return versions, nil
}

func fetchAvailableVersions(repoName string) ([]string, error) {
	if strings.HasPrefix(repoName, "github.com/") {
		return fetchGitHubVersions(repoName)
	}
	if strings.HasPrefix(repoName, "gitlab.com/") {
		return fetchGitLabVersions(repoName)
	}
	if strings.HasPrefix(repoName, "bitbucket.org/") {
		return fetchBitbucketVersions(repoName)
	}
	return nil, fmt.Errorf("unsupported remote host for %s", repoName)
}

func fetchGitHubVersions(repoName string) ([]string, error) {
	parts := strings.TrimPrefix(repoName, "github.com/")
	url := fmt.Sprintf("https://api.github.com/repos/%s/tags", parts)
	response, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github tags API status %d", response.StatusCode)
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tags); err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(tags))
	for _, tag := range tags {
		versions = append(versions, tag.Name)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %s", repoName)
	}
	return versions, nil
}

func fetchGitLabVersions(repoName string) ([]string, error) {
	parts := strings.TrimPrefix(repoName, "gitlab.com/")
	encoded := strings.ReplaceAll(parts, "/", "%2F")
	url := fmt.Sprintf("https://gitlab.com/api/v4/projects/%s/repository/tags", encoded)
	response, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gitlab tags API status %d", response.StatusCode)
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(response.Body).Decode(&tags); err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(tags))
	for _, tag := range tags {
		versions = append(versions, tag.Name)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %s", repoName)
	}
	return versions, nil
}

func fetchBitbucketVersions(repoName string) ([]string, error) {
	parts := strings.TrimPrefix(repoName, "bitbucket.org/")
	url := fmt.Sprintf("https://api.bitbucket.org/2.0/repositories/%s/refs/tags", parts)
	response, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bitbucket tags API status %d", response.StatusCode)
	}
	var payload struct {
		Values []struct {
			Name string `json:"name"`
		} `json:"values"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	versions := make([]string, 0, len(payload.Values))
	for _, value := range payload.Values {
		versions = append(versions, value.Name)
	}
	if len(versions) == 0 {
		return nil, fmt.Errorf("no versions found for %s", repoName)
	}
	return versions, nil
}

func stripProviderPrefix(repo string) string {
	for _, prefix := range []string{"github.com/", "gitlab.com/", "bitbucket.org/"} {
		if strings.HasPrefix(repo, prefix) {
			return strings.TrimPrefix(repo, prefix)
		}
	}
	return repo
}

func downloadFile(url string) (string, error) {
	tempFile, err := os.CreateTemp("", "ferret-download-*.tar.gz")
	if err != nil {
		return "", err
	}
	defer tempFile.Close()

	client := &http.Client{Timeout: 5 * time.Minute}
	response, err := client.Get(url)
	if err != nil {
		_ = os.Remove(tempFile.Name())
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_ = os.Remove(tempFile.Name())
		return "", fmt.Errorf("download failed with status %d", response.StatusCode)
	}
	if _, err := io.Copy(tempFile, response.Body); err != nil {
		_ = os.Remove(tempFile.Name())
		return "", err
	}
	return tempFile.Name(), nil
}

func extractTarGz(archivePath, destPath string) error {
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archiveFile.Close()

	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		relative := header.Name
		if index := strings.Index(relative, "/"); index >= 0 {
			relative = relative[index+1:]
		}
		if relative == "" {
			continue
		}
		target := filepath.Join(destPath, relative)
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			outFile, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(outFile, tarReader); err != nil {
				_ = outFile.Close()
				return err
			}
			if err := outFile.Close(); err != nil {
				return err
			}
			if err := os.Chmod(target, os.FileMode(header.Mode)); err != nil {
				return err
			}
		}
	}
	return nil
}

func copyDir(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, info.Mode()); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())
		if entry.IsDir() {
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
			continue
		}
		if err := copyFile(srcPath, dstPath); err != nil {
			return err
		}
	}
	return nil
}

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
	info, err := os.Stat(src)
	if err != nil {
		return err
	}
	return os.Chmod(dst, info.Mode())
}
