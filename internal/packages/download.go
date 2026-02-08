package packages

// download.go - Placeholder for Git refs-based download logic
// Will be implemented in next phase

import (
	"fmt"
)

// DownloadRemotePackage downloads a remote package using Git refs
// This will be implemented by porting code from old compiler
func DownloadRemotePackage(cachePath, repoName, version string) error {
	// TODO: Implement Git refs-based download
	// 1. Fetch refs from GitHub
	// 2. Find matching tag/version
	// 3. Download archive
	// 4. Extract to cache
	// 5. Validate fer.ret exists
	
	return fmt.Errorf("remote package download not yet implemented")
}

// FetchAvailableVersions fetches available versions for a remote package
func FetchAvailableVersions(repoName string) ([]string, error) {
	// TODO: Implement Git refs parsing
	// Parse packet-line format to extract tags
	
	return nil, fmt.Errorf("version fetching not yet implemented")
}
