package packages

import (
	"fmt"
	"os"
	"path/filepath"
)

func GetModulePath(cachePath, repoName, version string) string {
	return filepath.Join(cachePath, filepath.FromSlash(repoName+"@"+version))
}

func IsModuleCached(cachePath, repoName, version string) bool {
	_, err := os.Stat(GetModulePath(cachePath, repoName, version))
	return err == nil
}

func DeleteModule(cachePath, repoName, version string) error {
	modulePath := GetModulePath(cachePath, repoName, version)
	if err := os.RemoveAll(modulePath); err != nil {
		return fmt.Errorf("delete module %s: %w", modulePath, err)
	}
	return nil
}
