package packages

import (
	"fmt"
	"os"
	"path/filepath"

	"compiler/internal/manifest"
)

// CheckRemoteSecurity verifies that remote imports are allowed
func CheckRemoteSecurity(manifestPath string) error {
	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to load manifest: %w", err)
	}

	// TODO: Implement [remote] section in manifest
	// For now, we'll assume remote imports are disabled
	_ = m

	return fmt.Errorf("remote imports not yet implemented")
}

// ValidatePackageForSharing checks if a package can be shared (imported by others)
func ValidatePackageForSharing(packagePath string) error {
	manifestPath := filepath.Join(packagePath, "fer.ret")

	// Check if manifest exists
	if _, err := os.Stat(manifestPath); err != nil {
		return fmt.Errorf("package missing fer.ret manifest: %w", err)
	}

	// Load and validate manifest
	m, err := manifest.LoadManifest(manifestPath)
	if err != nil {
		return fmt.Errorf("invalid package manifest: %w", err)
	}

	// Verify package has a name
	if m.Package.Name == "" {
		return fmt.Errorf("package manifest missing package name")
	}

	// TODO: Check [remote].share = true when implemented

	return nil
}
