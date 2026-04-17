package manifest

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const LockfileName = "ferret.lock"

type LockfileEntry struct {
	Version      string   `json:"version"`
	ResolvedURL  string   `json:"resolved_url,omitempty"`
	Checksum     string   `json:"checksum,omitempty"`
	Direct       bool     `json:"direct,omitempty"`
	Description  string   `json:"description,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
	UsedBy       []string `json:"used_by,omitempty"`
	DownloadedAt string   `json:"downloaded_at,omitempty"`
}

type Lockfile struct {
	Version      string                   `json:"version"`
	DirectDeps   map[string]string        `json:"direct_deps,omitempty"`
	Packages     map[string]LockfileEntry `json:"packages,omitempty"`
	Dependencies map[string]LockfileEntry `json:"dependencies,omitempty"`
	GeneratedAt  string                   `json:"generated_at,omitempty"`
}

func NewLockfile() *Lockfile {
	return &Lockfile{
		Version:      "2.0",
		DirectDeps:   map[string]string{},
		Packages:     map[string]LockfileEntry{},
		Dependencies: map[string]LockfileEntry{},
		GeneratedAt:  time.Now().Format(time.RFC3339),
	}
}

func LoadLockfile(projectRoot string) (*Lockfile, error) {
	path := filepath.Join(projectRoot, LockfileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return NewLockfile(), nil
		}
		return nil, fmt.Errorf("read lockfile: %w", err)
	}
	type rawLockfile struct {
		Version      string                   `json:"version"`
		DirectDeps   json.RawMessage          `json:"direct_deps"`
		Packages     map[string]LockfileEntry `json:"packages"`
		Dependencies map[string]LockfileEntry `json:"dependencies"`
		GeneratedAt  string                   `json:"generated_at,omitempty"`
	}
	var raw rawLockfile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse lockfile: %w", err)
	}

	directDeps, err := decodeDirectDeps(raw.DirectDeps)
	if err != nil {
		return nil, fmt.Errorf("parse lockfile direct_deps: %w", err)
	}

	lock := &Lockfile{
		Version:      raw.Version,
		DirectDeps:   directDeps,
		Packages:     normalizePackageEntries(raw.Packages),
		Dependencies: normalizePackageEntries(raw.Dependencies),
		GeneratedAt:  raw.GeneratedAt,
	}
	if lock.Version == "" {
		lock.Version = "2.0"
	}
	if lock.Packages == nil {
		lock.Packages = map[string]LockfileEntry{}
	}
	if lock.Dependencies == nil {
		lock.Dependencies = map[string]LockfileEntry{}
	}
	if lock.Version == "" || lock.Version == "1.0" {
		lock.Version = "2.0"
	}
	if len(lock.Packages) == 0 && len(lock.Dependencies) > 0 {
		lock.Packages = copyEntries(lock.Dependencies)
	}
	if len(lock.Dependencies) == 0 && len(lock.Packages) > 0 {
		lock.Dependencies = copyEntries(lock.Packages)
	}
	lock.DirectDeps = normalizeDirectDeps(lock.DirectDeps, lock.Packages)
	reconcileDirectFlags(lock)
	if lock.DirectDeps == nil {
		lock.DirectDeps = map[string]string{}
	}
	return lock, nil
}

func SaveLockfile(projectRoot string, lock *Lockfile) error {
	if lock == nil {
		return fmt.Errorf("nil lockfile")
	}
	if lock.DirectDeps == nil {
		lock.DirectDeps = map[string]string{}
	}
	if lock.Packages == nil {
		lock.Packages = map[string]LockfileEntry{}
	}
	if lock.Dependencies == nil {
		lock.Dependencies = map[string]LockfileEntry{}
	}
	if len(lock.Packages) == 0 && len(lock.Dependencies) > 0 {
		lock.Packages = copyEntries(lock.Dependencies)
	}
	if len(lock.Dependencies) == 0 && len(lock.Packages) > 0 {
		lock.Dependencies = copyEntries(lock.Packages)
	}
	reconcileDirectFlags(lock)
	lock.GeneratedAt = time.Now().Format(time.RFC3339)

	pkgKeys := make([]string, 0, len(lock.Packages))
	for key := range lock.Packages {
		pkgKeys = append(pkgKeys, key)
	}
	sort.Strings(pkgKeys)
	sortedPackages := make(map[string]LockfileEntry, len(lock.Packages))
	for _, key := range pkgKeys {
		sortedPackages[key] = lock.Packages[key]
	}

	directKeys := make([]string, 0, len(lock.DirectDeps))
	for key := range lock.DirectDeps {
		directKeys = append(directKeys, key)
	}
	sort.Strings(directKeys)
	sortedDirect := make(map[string]string, len(lock.DirectDeps))
	for _, key := range directKeys {
		sortedDirect[key] = lock.DirectDeps[key]
	}

	out := &Lockfile{
		Version:     lock.Version,
		DirectDeps:  sortedDirect,
		Packages:    sortedPackages,
		GeneratedAt: lock.GeneratedAt,
	}

	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal lockfile: %w", err)
	}
	path := filepath.Join(projectRoot, LockfileName)
	return os.WriteFile(path, data, 0o644)
}

func (l *Lockfile) SetDependency(key string, entry LockfileEntry) {
	if l == nil {
		return
	}
	if l.Packages == nil {
		l.Packages = make(map[string]LockfileEntry)
	}
	if l.Dependencies == nil {
		l.Dependencies = make(map[string]LockfileEntry)
	}
	l.Packages[key] = entry
	l.Dependencies[key] = entry
}

func (l *Lockfile) GetDependency(key string) (LockfileEntry, bool) {
	if l == nil {
		return LockfileEntry{}, false
	}
	if entry, ok := l.Packages[key]; ok {
		return entry, true
	}
	entry, ok := l.Dependencies[key]
	return entry, ok
}

func (l *Lockfile) RemoveDependency(key string) {
	if l == nil {
		return
	}
	entry, found := l.GetDependency(key)
	if found {
		for _, child := range entry.Dependencies {
			l.RemoveUsedBy(child, key)
		}
	}
	if l.Packages != nil {
		delete(l.Packages, key)
	}
	if l.Dependencies == nil {
		l.Dependencies = make(map[string]LockfileEntry)
	}
	delete(l.Dependencies, key)
	entries := l.Packages
	if len(entries) == 0 {
		entries = l.Dependencies
	}
	for depKey, depEntry := range entries {
		depChanged := false
		depEntry.Dependencies = filterOut(depEntry.Dependencies, key, &depChanged)
		depEntry.UsedBy = filterOut(depEntry.UsedBy, key, &depChanged)
		if depChanged {
			l.SetDependency(depKey, depEntry)
		}
	}
	if l.DirectDeps == nil {
		return
	}
	for alias, dep := range l.DirectDeps {
		if dep == key || alias == key {
			delete(l.DirectDeps, alias)
		}
	}
}

func (l *Lockfile) RemoveDirectDependency(alias string) {
	if l == nil || l.DirectDeps == nil {
		return
	}
	packageID, ok := l.DirectDeps[alias]
	if !ok {
		return
	}
	delete(l.DirectDeps, alias)
	entry, found := l.GetDependency(packageID)
	if !found {
		return
	}
	stillDirect := false
	for _, depID := range l.DirectDeps {
		if depID == packageID {
			stillDirect = true
			break
		}
	}
	if !stillDirect {
		entry.Direct = false
		l.SetDependency(packageID, entry)
	}
}

func (l *Lockfile) AddDirectDependency(key string) {
	if l == nil {
		return
	}
	if l.DirectDeps == nil {
		l.DirectDeps = make(map[string]string)
	}
	l.DirectDeps[key] = key
}

func (l *Lockfile) SetDirectDependency(alias, packageID string) {
	if l == nil || alias == "" || packageID == "" {
		return
	}
	if l.DirectDeps == nil {
		l.DirectDeps = make(map[string]string)
	}
	if previous, ok := l.DirectDeps[alias]; ok && previous != packageID {
		if entry, found := l.GetDependency(previous); found {
			entry.Direct = false
			l.SetDependency(previous, entry)
		}
	}
	l.DirectDeps[alias] = packageID
	if entry, found := l.GetDependency(packageID); found {
		entry.Direct = true
		l.SetDependency(packageID, entry)
	}
}

func (l *Lockfile) GetDirectDependency(alias string) (string, bool) {
	if l == nil || alias == "" {
		return "", false
	}
	packageID, ok := l.DirectDeps[alias]
	return packageID, ok
}

func (l *Lockfile) FindPackageIDsByRepo(repo string) []string {
	if l == nil || repo == "" {
		return nil
	}
	out := make([]string, 0)
	seen := make(map[string]struct{})
	entries := l.Packages
	if len(entries) == 0 {
		entries = l.Dependencies
	}
	for key, entry := range entries {
		entryRepo := entry.ResolvedURL
		if entryRepo == "" {
			entryRepo = repoFromPackageKey(key)
		}
		if entryRepo != repo {
			continue
		}
		packageID := key
		if _, _, ok := SplitPackageID(packageID); !ok {
			if entry.Version == "" {
				continue
			}
			packageID = PackageID(entryRepo, entry.Version)
		}
		if _, ok := seen[packageID]; ok {
			continue
		}
		seen[packageID] = struct{}{}
		out = append(out, packageID)
	}
	sort.Strings(out)
	return out
}

func (l *Lockfile) AddUsedBy(depKey, parentKey string) {
	entry, ok := l.GetDependency(depKey)
	if !ok {
		return
	}
	for _, usedBy := range entry.UsedBy {
		if usedBy == parentKey {
			return
		}
	}
	entry.UsedBy = append(entry.UsedBy, parentKey)
	l.SetDependency(depKey, entry)
}

func (l *Lockfile) RemoveUsedBy(depKey, parentKey string) {
	entry, ok := l.GetDependency(depKey)
	if !ok {
		return
	}
	filtered := make([]string, 0, len(entry.UsedBy))
	for _, usedBy := range entry.UsedBy {
		if usedBy != parentKey {
			filtered = append(filtered, usedBy)
		}
	}
	entry.UsedBy = filtered
	l.SetDependency(depKey, entry)
}

func (l *Lockfile) GetUnusedDependencies() []string {
	if l == nil {
		return nil
	}
	unused := make([]string, 0)
	entries := l.Dependencies
	if len(l.Packages) > 0 {
		entries = l.Packages
	}
	for key, entry := range entries {
		if !entry.Direct && len(entry.UsedBy) == 0 {
			unused = append(unused, key)
		}
	}
	sort.Strings(unused)
	return unused
}

func (l *Lockfile) UpdateDependencyEdges(parentKey string, dependencies []string) {
	if l == nil || parentKey == "" {
		return
	}
	parentEntry, found := l.GetDependency(parentKey)
	if !found {
		return
	}
	next := uniqueStrings(dependencies)
	prevSet := make(map[string]struct{}, len(parentEntry.Dependencies))
	for _, dep := range parentEntry.Dependencies {
		prevSet[dep] = struct{}{}
	}
	nextSet := make(map[string]struct{}, len(next))
	for _, dep := range next {
		nextSet[dep] = struct{}{}
	}
	for dep := range prevSet {
		if _, keep := nextSet[dep]; !keep {
			l.RemoveUsedBy(dep, parentKey)
		}
	}
	for dep := range nextSet {
		if _, had := prevSet[dep]; !had {
			l.AddUsedBy(dep, parentKey)
		}
	}
	parentEntry.Dependencies = next
	l.SetDependency(parentKey, parentEntry)
}

func filterOut(values []string, target string, changed *bool) []string {
	if len(values) == 0 {
		return values
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == target {
			*changed = true
			continue
		}
		out = append(out, value)
	}
	return out
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func decodeDirectDeps(raw json.RawMessage) (map[string]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]string{}, nil
	}

	var byAlias map[string]string
	if err := json.Unmarshal(raw, &byAlias); err == nil {
		return byAlias, nil
	}

	var asList []string
	if err := json.Unmarshal(raw, &asList); err == nil {
		converted := make(map[string]string, len(asList))
		for _, dep := range asList {
			converted[dep] = dep
		}
		return converted, nil
	}
	return nil, fmt.Errorf("expected object or array")
}

func copyEntries(src map[string]LockfileEntry) map[string]LockfileEntry {
	if src == nil {
		return nil
	}
	dst := make(map[string]LockfileEntry, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}

func normalizePackageEntries(src map[string]LockfileEntry) map[string]LockfileEntry {
	if src == nil {
		return nil
	}
	dst := make(map[string]LockfileEntry, len(src))
	for key, entry := range src {
		normalizedKey := key
		if _, _, ok := SplitPackageID(key); !ok {
			repo := entry.ResolvedURL
			if repo == "" {
				repo = key
			}
			if entry.Version != "" {
				normalizedKey = PackageID(repo, entry.Version)
			}
		}
		if entry.ResolvedURL == "" {
			entry.ResolvedURL = repoFromPackageKey(normalizedKey)
		}
		dst[normalizedKey] = entry
	}
	return dst
}

func normalizeDirectDeps(src map[string]string, packages map[string]LockfileEntry) map[string]string {
	if src == nil {
		return nil
	}
	out := make(map[string]string, len(src))
	for key, value := range src {
		alias := key
		packageID := value
		if packageID == "" {
			packageID = key
		}
		if _, _, ok := SplitPackageID(packageID); !ok {
			if entry, ok := packages[packageID]; ok && entry.Version != "" {
				repo := entry.ResolvedURL
				if repo == "" {
					repo = repoFromPackageKey(packageID)
				}
				packageID = PackageID(repo, entry.Version)
			} else if resolved, ok := pickPackageIDForRepo(packages, packageID); ok {
				packageID = resolved
			}
		}
		out[alias] = packageID
	}
	return out
}

func PackageID(repo, version string) string {
	return strings.TrimSpace(repo) + "@" + strings.TrimSpace(version)
}

func SplitPackageID(packageID string) (repo, version string, ok bool) {
	trimmed := strings.TrimSpace(packageID)
	idx := strings.LastIndex(trimmed, "@")
	if idx <= 0 || idx >= len(trimmed)-1 {
		return "", "", false
	}
	return trimmed[:idx], trimmed[idx+1:], true
}

func repoFromPackageKey(key string) string {
	repo, _, ok := SplitPackageID(key)
	if ok {
		return repo
	}
	return key
}

func pickPackageIDForRepo(packages map[string]LockfileEntry, repo string) (string, bool) {
	best := ""
	for key, entry := range packages {
		entryRepo := entry.ResolvedURL
		if entryRepo == "" {
			entryRepo = repoFromPackageKey(key)
		}
		if entryRepo != repo {
			continue
		}
		packageID := key
		if _, _, ok := SplitPackageID(packageID); !ok {
			if entry.Version == "" {
				continue
			}
			packageID = PackageID(entryRepo, entry.Version)
		}
		if packageID > best {
			best = packageID
		}
	}
	if best == "" {
		return "", false
	}
	return best, true
}

func reconcileDirectFlags(lock *Lockfile) {
	if lock == nil {
		return
	}
	if lock.Packages == nil {
		lock.Packages = map[string]LockfileEntry{}
	}
	if lock.Dependencies == nil {
		lock.Dependencies = map[string]LockfileEntry{}
	}
	for key, entry := range lock.Packages {
		entry.Direct = false
		lock.Packages[key] = entry
		lock.Dependencies[key] = entry
	}
	for _, packageID := range lock.DirectDeps {
		entry, ok := lock.Packages[packageID]
		if !ok {
			continue
		}
		entry.Direct = true
		lock.Packages[packageID] = entry
		lock.Dependencies[packageID] = entry
	}
}
