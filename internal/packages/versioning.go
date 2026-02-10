package packages

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Version represents a semantic version
type Version struct {
	Major int
	Minor int
	Patch int
	Tag   string // e.g., "v1.2.3"
}

// ParseVersion parses a version string like "v1.2.3"
func ParseVersion(versionStr string) (*Version, error) {
	versionStr = strings.TrimSpace(versionStr)
	if versionStr == "" || versionStr == "latest" {
		return nil, fmt.Errorf("invalid version: %s", versionStr)
	}

	// Match vX.Y.Z or X.Y.Z
	re := regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)
	matches := re.FindStringSubmatch(versionStr)
	if matches == nil {
		return nil, fmt.Errorf("invalid version format: %s", versionStr)
	}

	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])

	return &Version{
		Major: major,
		Minor: minor,
		Patch: patch,
		Tag:   versionStr,
	}, nil
}

// String returns the version as a string
func (v *Version) String() string {
	return v.Tag
}

// Compare compares two versions
// Returns: -1 if v < other, 0 if v == other, 1 if v > other
func (v *Version) Compare(other *Version) int {
	if v.Major != other.Major {
		if v.Major < other.Major {
			return -1
		}
		return 1
	}
	if v.Minor != other.Minor {
		if v.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if v.Patch != other.Patch {
		if v.Patch < other.Patch {
			return -1
		}
		return 1
	}
	return 0
}

// MatchesConstraint checks if the version matches a constraint
// Supports: ^v1.0.0 (compatible), ~v1.2.0 (patch), v1.2.3 (exact), >=v1.0.0, >v1.0.0, <=v1.0.0, <v1.0.0, latest
func MatchesConstraint(version, constraint string) (bool, error) {
	if constraint == "latest" {
		return true, nil
	}

	v, err := ParseVersion(version)
	if err != nil {
		return false, err
	}

	constraint = strings.TrimSpace(constraint)

	// Greater than or equal (>=)
	if strings.HasPrefix(constraint, ">=") {
		c, err := ParseVersion(strings.TrimPrefix(constraint, ">="))
		if err != nil {
			return false, err
		}
		return v.Compare(c) >= 0, nil
	}

	// Greater than (>)
	if strings.HasPrefix(constraint, ">") {
		c, err := ParseVersion(strings.TrimPrefix(constraint, ">"))
		if err != nil {
			return false, err
		}
		return v.Compare(c) > 0, nil
	}

	// Less than or equal (<=)
	if strings.HasPrefix(constraint, "<=") {
		c, err := ParseVersion(strings.TrimPrefix(constraint, "<="))
		if err != nil {
			return false, err
		}
		return v.Compare(c) <= 0, nil
	}

	// Less than (<)
	if strings.HasPrefix(constraint, "<") {
		c, err := ParseVersion(strings.TrimPrefix(constraint, "<"))
		if err != nil {
			return false, err
		}
		return v.Compare(c) < 0, nil
	}

	// Caret (^) - compatible version (same major)
	if strings.HasPrefix(constraint, "^") {
		c, err := ParseVersion(strings.TrimPrefix(constraint, "^"))
		if err != nil {
			return false, err
		}
		return v.Major == c.Major && v.Compare(c) >= 0, nil
	}

	// Tilde (~) - patch version (same major and minor)
	if strings.HasPrefix(constraint, "~") {
		c, err := ParseVersion(strings.TrimPrefix(constraint, "~"))
		if err != nil {
			return false, err
		}
		return v.Major == c.Major && v.Minor == c.Minor && v.Compare(c) >= 0, nil
	}

	// Exact match
	c, err := ParseVersion(constraint)
	if err != nil {
		return false, err
	}
	return v.Compare(c) == 0, nil
}

// FindBestMatch finds the best version from a list that matches the constraint
func FindBestMatch(versions []string, constraint string) (string, error) {
	if constraint == "latest" && len(versions) > 0 {
		// Return the last version (assumes versions are sorted)
		return versions[len(versions)-1], nil
	}

	var bestMatch *Version
	var bestMatchStr string

	for _, ver := range versions {
		matches, err := MatchesConstraint(ver, constraint)
		if err != nil {
			continue
		}
		if !matches {
			continue
		}

		v, err := ParseVersion(ver)
		if err != nil {
			continue
		}

		if bestMatch == nil || v.Compare(bestMatch) > 0 {
			bestMatch = v
			bestMatchStr = ver
		}
	}

	if bestMatch == nil {
		return "", fmt.Errorf("no version found matching constraint: %s", constraint)
	}

	return bestMatchStr, nil
}

// FindBestMatchMultipleConstraints finds the best version that satisfies all constraints
// Returns error if no version satisfies all constraints (version conflict)
func FindBestMatchMultipleConstraints(versions []string, constraints []string) (string, error) {
	if len(constraints) == 0 {
		if len(versions) > 0 {
			return versions[len(versions)-1], nil
		}
		return "", fmt.Errorf("no versions available")
	}

	// Handle single constraint case
	if len(constraints) == 1 {
		return FindBestMatch(versions, constraints[0])
	}

	var bestMatch *Version
	var bestMatchStr string

	// Check each version against all constraints
	for _, verStr := range versions {
		v, err := ParseVersion(verStr)
		if err != nil {
			continue // Skip invalid versions
		}

		// Check if this version satisfies ALL constraints
		satisfiesAll := true
		for _, constraint := range constraints {
			matches, err := MatchesConstraint(verStr, constraint)
			if err != nil || !matches {
				satisfiesAll = false
				break
			}
		}

		if satisfiesAll {
			if bestMatch == nil || v.Compare(bestMatch) > 0 {
				bestMatch = v
				bestMatchStr = verStr
			}
		}
	}

	if bestMatch == nil {
		return "", fmt.Errorf("version conflict: no version satisfies all constraints %v", constraints)
	}

	return bestMatchStr, nil
}
