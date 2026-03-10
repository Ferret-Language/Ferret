package toml

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type ParseError struct {
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("line %d: %s", e.Line, e.Message)
}

func ParseFile(filename string) (Data, error) {
	file, err := os.Open(filename)
	if err != nil {
		return Data{}, err
	}
	defer file.Close()

	data := NewData()
	currentSection := ""

	scanner := bufio.NewScanner(file)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.TrimSpace(line[1 : len(line)-1])
			if currentSection == "" {
				return Data{}, &ParseError{Line: lineNo, Message: "empty section name"}
			}
			if err := ensureSection(&data, currentSection); err != nil {
				return Data{}, &ParseError{Line: lineNo, Message: err.Error()}
			}
			continue
		}
		if err := parseKeyValue(&data, currentSection, lineNo, line); err != nil {
			return Data{}, err
		}
	}
	if err := scanner.Err(); err != nil {
		return Data{}, err
	}
	return data, nil
}

func ensureSection(data *Data, section string) error {
	if _, ok := data.Sections[section]; ok {
		return fmt.Errorf("duplicate section %q", section)
	}
	data.Sections[section] = make(Table)
	data.SectionOrder = append(data.SectionOrder, section)
	return nil
}

func parseKeyValue(data *Data, currentSection string, lineNo int, line string) error {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return &ParseError{Line: lineNo, Message: fmt.Sprintf("invalid key/value pair: %s", line)}
	}
	key := strings.TrimSpace(parts[0])
	if key == "" {
		return &ParseError{Line: lineNo, Message: "empty key"}
	}
	valueText := stripInlineComment(strings.TrimSpace(parts[1]))
	section := currentSection
	if section == "" {
		section = "default"
	}
	if _, ok := data.Sections[section]; !ok {
		if err := ensureSection(data, section); err != nil {
			return &ParseError{Line: lineNo, Message: err.Error()}
		}
	}
	if _, exists := data.Sections[section][key]; exists {
		return &ParseError{Line: lineNo, Message: fmt.Sprintf("duplicate key %q in section %q", key, section)}
	}
	value, err := parseValue(valueText)
	if err != nil {
		return &ParseError{Line: lineNo, Message: err.Error()}
	}
	data.KeyOrder[section] = append(data.KeyOrder[section], key)
	data.Sections[section][key] = value
	return nil
}

func stripInlineComment(valueText string) string {
	var out strings.Builder
	inQuotes := false
	escaped := false
	for _, ch := range valueText {
		if escaped {
			out.WriteRune(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			escaped = true
			out.WriteRune(ch)
		case '"':
			inQuotes = !inQuotes
			out.WriteRune(ch)
		case '#':
			if !inQuotes {
				return strings.TrimSpace(out.String())
			}
			out.WriteRune(ch)
		default:
			out.WriteRune(ch)
		}
	}
	return strings.TrimSpace(out.String())
}

func parseValue(text string) (Value, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return "", fmt.Errorf("missing value")
	}
	if strings.HasPrefix(text, `"`) && strings.HasSuffix(text, `"`) {
		return unquote(text)
	}
	if strings.HasPrefix(text, "{") && strings.HasSuffix(text, "}") {
		return parseInlineTable(text)
	}
	if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
		return parseArray(text)
	}
	if text == "true" || text == "false" {
		return text == "true", nil
	}
	if i, err := strconv.Atoi(text); err == nil {
		return i, nil
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		return f, nil
	}
	return text, nil
}

func parseInlineTable(text string) (Table, error) {
	body := strings.TrimSpace(text[1 : len(text)-1])
	table := make(Table)
	if body == "" {
		return table, nil
	}
	parts, err := splitTopLevel(body, ',')
	if err != nil {
		return nil, err
	}
	for _, part := range parts {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid inline table entry %q", part)
		}
		key := strings.TrimSpace(kv[0])
		if key == "" {
			return nil, fmt.Errorf("empty inline table key")
		}
		if _, exists := table[key]; exists {
			return nil, fmt.Errorf("duplicate inline table key %q", key)
		}
		value, err := parseValue(strings.TrimSpace(kv[1]))
		if err != nil {
			return nil, err
		}
		table[key] = value
	}
	return table, nil
}

func parseArray(text string) ([]Value, error) {
	body := strings.TrimSpace(text[1 : len(text)-1])
	if body == "" {
		return []Value{}, nil
	}
	parts, err := splitTopLevel(body, ',')
	if err != nil {
		return nil, err
	}
	values := make([]Value, 0, len(parts))
	for _, part := range parts {
		value, err := parseValue(strings.TrimSpace(part))
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	return values, nil
}

func splitTopLevel(text string, delim rune) ([]string, error) {
	var parts []string
	var current strings.Builder
	inQuotes := false
	escaped := false
	braceDepth := 0
	bracketDepth := 0
	for _, ch := range text {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		switch ch {
		case '\\':
			escaped = true
			current.WriteRune(ch)
		case '"':
			inQuotes = !inQuotes
			current.WriteRune(ch)
		case '{':
			if !inQuotes {
				braceDepth++
			}
			current.WriteRune(ch)
		case '}':
			if !inQuotes {
				braceDepth--
				if braceDepth < 0 {
					return nil, fmt.Errorf("unbalanced '}'")
				}
			}
			current.WriteRune(ch)
		case '[':
			if !inQuotes {
				bracketDepth++
			}
			current.WriteRune(ch)
		case ']':
			if !inQuotes {
				bracketDepth--
				if bracketDepth < 0 {
					return nil, fmt.Errorf("unbalanced ']'")
				}
			}
			current.WriteRune(ch)
		default:
			if ch == delim && !inQuotes && braceDepth == 0 && bracketDepth == 0 {
				parts = append(parts, strings.TrimSpace(current.String()))
				current.Reset()
				continue
			}
			current.WriteRune(ch)
		}
	}
	if inQuotes || braceDepth != 0 || bracketDepth != 0 {
		return nil, fmt.Errorf("unterminated composite value")
	}
	if current.Len() > 0 {
		parts = append(parts, strings.TrimSpace(current.String()))
	}
	return parts, nil
}

func unquote(text string) (string, error) {
	value, err := strconv.Unquote(text)
	if err != nil {
		return "", fmt.Errorf("invalid quoted string %q", text)
	}
	return value, nil
}
