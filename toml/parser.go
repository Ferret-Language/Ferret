package toml

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

func ParseTOMLFile(filename string) (TOMLData, error) {
	file, err := os.Open(filename)
	if err != nil {
		return TOMLData{}, err
	}
	defer file.Close()

	data := NewTOMLData()
	currentSection := ""

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if shouldSkipLine(line) {
			continue
		}

		if isSectionHeader(line) {
			currentSection = parseSectionHeader(line)
			ensureSectionExists(&data, currentSection)
			continue
		}

		if err := parseKeyValuePair(&data, line, currentSection); err != nil {
			return TOMLData{}, err
		}
	}

	if err := scanner.Err(); err != nil {
		return TOMLData{}, err
	}
	return data, nil
}

func shouldSkipLine(line string) bool {
	return line == "" || strings.HasPrefix(line, "#")
}

func isSectionHeader(line string) bool {
	return strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]")
}

func parseSectionHeader(line string) string {
	return strings.TrimSpace(line[1 : len(line)-1])
}

func ensureSectionExists(data *TOMLData, section string) {
	if _, exists := data.Sections[section]; !exists {
		data.Sections[section] = make(TOMLTable)
		data.SectionOrder = append(data.SectionOrder, section)
	}
}

func parseKeyValuePair(data *TOMLData, line, currentSection string) error {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("invalid line: %s", line)
	}

	key := strings.TrimSpace(parts[0])
	valueStr := strings.TrimSpace(parts[1])

	// Handle inline comments by finding the first # that's not inside quotes
	valueStr = stripInlineComment(valueStr)
	value := parseValue(valueStr)

	section := getEffectiveSection(currentSection)
	ensureSectionExists(data, section)

	// Track key order if this is a new key
	if _, exists := data.Sections[section][key]; !exists {
		data.KeyOrder[section] = append(data.KeyOrder[section], key)
	}

	data.Sections[section][key] = value

	return nil
}

// stripInlineComment removes inline comments from a value string
// It respects quoted strings and only removes comments outside of quotes
func stripInlineComment(valueStr string) string {
	var result strings.Builder
	inQuotes := false
	escaped := false

	for _, char := range valueStr {
		if escaped {
			result.WriteRune(char)
			escaped = false
			continue
		}

		if char == '\\' {
			escaped = true
			result.WriteRune(char)
			continue
		}

		if char == '"' {
			inQuotes = !inQuotes
			result.WriteRune(char)
			continue
		}

		if char == '#' && !inQuotes {
			// Found inline comment, return what we have so far
			return strings.TrimSpace(result.String())
		}

		result.WriteRune(char)
	}

	return strings.TrimSpace(result.String())
}

func getEffectiveSection(currentSection string) string {
	if currentSection == "" {
		return "default"
	}
	return currentSection
}

func parseValue(val string) TOMLValue {
	// String
	if strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`) {
		return strings.Trim(val, `"`)
	}

	// Boolean
	if val == "true" || val == "false" {
		return val == "true"
	}

	// Integer
	if i, err := strconv.Atoi(val); err == nil {
		return i
	}

	// Float
	if f, err := strconv.ParseFloat(val, 64); err == nil {
		return f
	}

	// Fallback
	return val
}
