package packages

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type Version struct {
	Major int
	Minor int
	Patch int
	Tag   string
}

func ParseVersion(text string) (*Version, error) {
	text = strings.TrimSpace(text)
	if text == "" || text == "latest" {
		return nil, fmt.Errorf("invalid version %q", text)
	}
	matches := regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`).FindStringSubmatch(text)
	if matches == nil {
		return nil, fmt.Errorf("invalid version format %q", text)
	}
	major, _ := strconv.Atoi(matches[1])
	minor, _ := strconv.Atoi(matches[2])
	patch, _ := strconv.Atoi(matches[3])
	return &Version{Major: major, Minor: minor, Patch: patch, Tag: text}, nil
}

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

func MatchesConstraint(version, constraint string) (bool, error) {
	if constraint == "latest" {
		return true, nil
	}
	v, err := ParseVersion(version)
	if err != nil {
		return false, err
	}
	constraint = strings.TrimSpace(constraint)

	parseAndCompare := func(prefix string, check func(int) bool) (bool, error) {
		reference, err := ParseVersion(strings.TrimPrefix(constraint, prefix))
		if err != nil {
			return false, err
		}
		return check(v.Compare(reference)), nil
	}

	if strings.HasPrefix(constraint, ">=") {
		return parseAndCompare(">=", func(cmp int) bool { return cmp >= 0 })
	}
	if strings.HasPrefix(constraint, ">") {
		return parseAndCompare(">", func(cmp int) bool { return cmp > 0 })
	}
	if strings.HasPrefix(constraint, "<=") {
		return parseAndCompare("<=", func(cmp int) bool { return cmp <= 0 })
	}
	if strings.HasPrefix(constraint, "<") {
		return parseAndCompare("<", func(cmp int) bool { return cmp < 0 })
	}
	if strings.HasPrefix(constraint, "^") {
		reference, err := ParseVersion(strings.TrimPrefix(constraint, "^"))
		if err != nil {
			return false, err
		}
		return v.Major == reference.Major && v.Compare(reference) >= 0, nil
	}

	if after, ok := strings.CutPrefix(constraint, "~"); ok {
		reference, err := ParseVersion(after)
		if err != nil {
			return false, err
		}
		return v.Major == reference.Major && v.Minor == reference.Minor && v.Compare(reference) >= 0, nil
	}
	reference, err := ParseVersion(constraint)
	if err != nil {
		return false, err
	}
	return v.Compare(reference) == 0, nil
}

func FindBestMatch(versions []string, constraint string) (string, error) {
	if constraint == "latest" {
		best := ""
		for _, candidate := range versions {
			if best == "" {
				best = candidate
				continue
			}
			left, leftErr := ParseVersion(best)
			right, rightErr := ParseVersion(candidate)
			if leftErr == nil && rightErr == nil && right.Compare(left) > 0 {
				best = candidate
			}
		}
		if best == "" {
			return "", fmt.Errorf("no versions available")
		}
		return best, nil
	}

	var bestVersion *Version
	bestTag := ""
	for _, candidate := range versions {
		ok, err := MatchesConstraint(candidate, constraint)
		if err != nil || !ok {
			continue
		}
		parsed, err := ParseVersion(candidate)
		if err != nil {
			continue
		}
		if bestVersion == nil || parsed.Compare(bestVersion) > 0 {
			bestVersion = parsed
			bestTag = candidate
		}
	}
	if bestVersion == nil {
		return "", fmt.Errorf("no version matches %q", constraint)
	}
	return bestTag, nil
}

func FindBestMatchMultipleConstraints(versions []string, constraints []string) (string, error) {
	if len(constraints) == 0 {
		return FindBestMatch(versions, "latest")
	}
	if len(constraints) == 1 {
		return FindBestMatch(versions, constraints[0])
	}

	var bestVersion *Version
	bestTag := ""
	for _, candidate := range versions {
		allMatched := true
		for _, constraint := range constraints {
			ok, err := MatchesConstraint(candidate, constraint)
			if err != nil || !ok {
				allMatched = false
				break
			}
		}
		if !allMatched {
			continue
		}
		parsed, err := ParseVersion(candidate)
		if err != nil {
			continue
		}
		if bestVersion == nil || parsed.Compare(bestVersion) > 0 {
			bestVersion = parsed
			bestTag = candidate
		}
	}
	if bestVersion == nil {
		return "", fmt.Errorf("version conflict for constraints %v", constraints)
	}
	return bestTag, nil
}
