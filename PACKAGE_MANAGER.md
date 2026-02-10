# Ferret Package Manager

A comprehensive package manager for the Ferret programming language with support for remote Git repositories and local neighbor packages.

## Features

### ✅ Completed

- **CLI Commands**
  - `ferret init [name]` - Create a new Ferret project with fer.ret
  - `ferret get` - Install all dependencies from fer.ret
  - `ferret get <pkg1> <pkg2>...` - Install specific packages
  - `ferret remove <package>` - Remove a dependency
  - `ferret list` - List all dependencies
  - `ferret update` - Update all dependencies to latest compatible versions
  - `ferret update <pkg1> <pkg2>...` - Update specific packages
  - `ferret cleanup` - Remove unused cached packages

- **Version Constraints**
  - `^v1.0.0` - Compatible version (same major)
  - `~v1.2.0` - Patch version (same major and minor)
  - `>=v1.0.0` - Greater than or equal
  - `>v1.0.0` - Greater than
  - `<=v1.0.0` - Less than or equal
  - `<v1.0.0` - Less than
  - `v1.2.3` - Exact version
  - `latest` - Latest available version

- **Dependency Resolution**
  - Transitive dependency installation
  - Multi-constraint version resolution
  - Conflict detection and reporting
  - Dependency graph tracking (used_by)

- **Git Provider Support**
  - GitHub API integration for version fetching
  - GitLab API integration
  - Bitbucket API integration
  - Tarball download and extraction
  - Automatic top-level directory stripping

- **Local Development**
  - Mock mode for local testing
  - Neighbor packages (relative paths)
  - Flexible mock directory structure

- **User Experience**
  - Colorful CLI with emojis
  - Progress indicators
  - Cache status display
  - Clear error messages
  - Version arrows (v1.0.0 → v1.9.0)

## File Formats

### fer.ret (Package Manifest)

```toml
[package]
name = "myapp"
version = "v1.0.0"
description = "My Ferret application"
author = "Your Name"

[dependencies]
# Remote packages
logger = "github.com/user/logger@^v1.0.0"
http-client = "github.com/user/http-client@v1.2.0"

# Neighbor packages
mathlib = "../mathlib"

[dev]
# Optional: for local testing with mock packages
mock_remote = true
mock_path = "../mock-packages"
```

### ferret.lock (Lockfile)

```json
{
  "version": "1.0",
  "direct_deps": [
    "github.com/user/logger",
    "github.com/user/http-client"
  ],
  "dependencies": {
    "github.com/user/logger": {
      "version": "v1.9.0",
      "resolved_url": "github.com/user/logger",
      "direct": true,
      "description": "logger",
      "dependencies": [],
      "used_by": ["github.com/user/http-client"]
    },
    "github.com/user/http-client": {
      "version": "v1.2.0",
      "resolved_url": "github.com/user/http-client",
      "direct": true,
      "description": "http-client",
      "dependencies": ["github.com/user/logger"],
      "used_by": []
    }
  },
  "generated_at": "2026-02-08T21:13:37+06:00"
}
```

## Usage Examples

### Create a New Project

```bash
ferret init myapp
cd myapp
```

### Add Dependencies

Edit `fer.ret`:
```toml
[dependencies]
logger = "github.com/user/logger@^v1.0.0"
```

Then install:
```bash
ferret get
```

### Install Specific Packages

```bash
ferret get github.com/user/logger@^v1.0.0 github.com/user/http-client@v1.2.0
```

### Update Dependencies

```bash
# Update all
ferret update

# Update specific packages
ferret update github.com/user/logger
```

### Remove a Package

```bash
ferret remove logger
```

### List Dependencies

```bash
ferret list
```

### Cleanup Unused Packages

```bash
ferret cleanup
```

## Cache Structure

Packages are cached in `.ferret/modules/`:

```
.ferret/
└── modules/
    ├── github.com/user/logger@v1.9.0/
    │   ├── fer.ret
    │   └── internal/
    │       └── logger.go
    └── github.com/user/http-client@v1.2.0/
        ├── fer.ret
        └── client.go
```

## Git Provider URLs

### GitHub
- API: `https://api.github.com/repos/{user}/{repo}/tags`
- Download: `https://github.com/{user}/{repo}/archive/refs/tags/{version}.tar.gz`

### GitLab
- API: `https://gitlab.com/api/v4/projects/{user}%2F{repo}/repository/tags`
- Download: `https://gitlab.com/{user}/{repo}/-/archive/{version}/{repo}-{version}.tar.gz`

### Bitbucket
- API: `https://api.bitbucket.org/2.0/repositories/{user}/{repo}/refs/tags`
- Download: `https://bitbucket.org/{user}/{repo}/get/{version}.tar.gz`

## Mock Mode

For local development and testing without Git access:

```toml
[dev]
mock_remote = true
mock_path = "../mock-packages"
```

Mock directory structure:
```
mock-packages/
├── github.com/
│   └── user/
│       ├── logger-v1.0.0/
│       ├── logger-v1.5.0/
│       └── logger-v1.9.0/
└── user/
    └── logger-v1.9.0/  # Alternative layout
```

## Implementation Details

### Version Resolution Algorithm

1. Collect all constraints for a package from:
   - Direct dependencies in fer.ret
   - Transitive dependencies from other packages
2. Find all available versions from Git provider
3. Filter versions matching ALL constraints
4. Select highest matching version
5. Report conflict if no version satisfies all constraints

### Selective Commands

**Selective Get:**
```bash
ferret get pkg1@v1 pkg2@v2
```
- Installs each package
- Adds to manifest only after successful installation
- Failed installs don't modify manifest

**Selective Update:**
```bash
ferret update pkg1 pkg2
```
- Updates only specified packages
- Checks constraints from manifest
- Downloads if new version available

### Manifest Save Timing

Packages are added to `fer.ret` **after** successful installation:
1. Parse package spec
2. Install package and dependencies
3. Save lockfile
4. Update manifest (only on success)

This ensures failed installations don't pollute the manifest.

## Error Handling

- Network failures: Clear error messages with retry suggestions
- Version conflicts: Shows all conflicting constraints
- Missing fer.ret: Downloads are rolled back automatically
- Invalid package specs: Detailed parsing errors
- API rate limits: Reports status codes

## Performance

- Caching: Already downloaded packages are reused
- Parallel downloads: Future enhancement
- Incremental updates: Only downloads changed packages
- Efficient archive extraction: Streams tar.gz without temporary extraction

## Testing

Mock mode allows comprehensive testing without network access:

```bash
# Create mock packages
mkdir -p mock-packages/github.com/user/logger-v1.0.0
echo '[package]\nname = "logger"\nversion = "v1.0.0"' > mock-packages/github.com/user/logger-v1.0.0/fer.ret

# Use in project
echo 'mock_remote = true\nmock_path = "../mock-packages"' >> fer.ret

# Test commands
ferret get
ferret update
```

## Future Enhancements

- [ ] Checksums and integrity verification
- [ ] Signature verification for security
- [ ] Private repository support with authentication
- [ ] Parallel downloads for faster installation
- [ ] Progress bars for large downloads
- [ ] Dependency graph visualization
- [ ] Lock file conflict resolution
- [ ] Package search/discovery
- [ ] Package registry
- [ ] Workspace support (monorepos)

## Architecture

```
cmd/cli/
├── init.go      - Project initialization
├── get.go       - Package installation
├── update.go    - Package updates
├── remove.go    - Package removal
├── list.go      - Dependency listing
├── cleanup.go   - Cache cleanup
└── ui.go        - CLI output helpers

internal/
├── manifest/
│   ├── manifest.go  - fer.ret parsing
│   └── lockfile.go  - ferret.lock management
└── packages/
    ├── download.go   - Git provider integration
    ├── versioning.go - Version constraint resolution
    ├── cache.go      - Cache management
    ├── resolver.go   - Dependency resolution
    └── security.go   - Security validation
```

## Credits

Built with ❤️ for the Ferret programming language.
