package source

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

type Location struct {
	Filename *string
	Start    *Position
	End      *Position
}

func NewLocation(file string, start, end Position) Location {
	startCopy := start
	endCopy := end
	filename := file
	return Location{
		Filename: &filename,
		Start:    &startCopy,
		End:      &endCopy,
	}
}

func (l Location) String() string {
	if l.Start == nil || l.End == nil {
		return "location(unknown)"
	}
	if *l.Filename == "" {
		return fmt.Sprintf("%s-%s", l.Start, l.End)
	}
	return fmt.Sprintf("%s:%s-%s", *l.Filename, l.Start, l.End)
}

type SourceCache interface {
	GetLinesRange(filepath string, startLine, endLine int) ([]string, bool)
}

func (l *Location) GetText(cache SourceCache) string {
	if l == nil || l.Filename == nil || l.Start == nil || l.End == nil {
		return ""
	}
	lines, err := GetSourceLinesRange(*l.Filename, l.Start.Line, l.End.Line, cache)
	if err != nil || len(lines) == 0 {
		return ""
	}
	if l.Start.Line == l.End.Line {
		line := lines[0]
		if l.Start.Column < 1 || l.End.Column < l.Start.Column || l.End.Column > len(line)+1 {
			return ""
		}
		return line[l.Start.Column-1 : l.End.Column-1]
	}
	var result strings.Builder
	for i, line := range lines {
		lineNum := l.Start.Line + i
		switch lineNum {
		case l.Start.Line:
			if l.Start.Column >= 1 && l.Start.Column <= len(line)+1 {
				result.WriteString(line[l.Start.Column-1:])
			}
		case l.End.Line:
			if l.End.Column >= 1 && l.End.Column <= len(line)+1 {
				result.WriteString("\n" + line[:l.End.Column-1])
			}
		default:
			result.WriteString("\n" + line)
		}
	}
	return result.String()
}

func GetSourceLines(filepath string) ([]string, error) {
	content, err := os.ReadFile(filepath)
	if err != nil {
		return nil, err
	}
	if len(content) == 0 {
		return []string{}, nil
	}
	lines := make([]string, 0)
	start := 0
	for i := range content {
		if content[i] == '\n' {
			lines = append(lines, string(content[start:i]))
			start = i + 1
		}
	}
	if start < len(content) {
		lines = append(lines, string(content[start:]))
	}
	return lines, nil
}

func GetSourceLinesRange(filepath string, startLine, endLine int, cache SourceCache) ([]string, error) {
	if startLine < 1 || endLine < startLine {
		return nil, fmt.Errorf("invalid line range: %d-%d", startLine, endLine)
	}
	if cache != nil {
		if lines, ok := cache.GetLinesRange(filepath, startLine, endLine); ok {
			return lines, nil
		}
	}
	file, err := os.Open(filepath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	lines := make([]string, 0, endLine-startLine+1)
	currentLine := 0
	for scanner.Scan() {
		currentLine++
		if currentLine < startLine {
			continue
		}
		if currentLine > endLine {
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(lines) == 0 && currentLine < startLine {
		return nil, fmt.Errorf("line %d out of range (file has %d lines)", startLine, currentLine)
	}
	return lines, nil
}
