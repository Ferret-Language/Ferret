package colors

import (
	"fmt"
	"html"
	"strings"
	"sync"
)

type LogFormat string

const (
	LogFormatANSI   LogFormat = "ansi"
	LogFormatNormal LogFormat = "normal"
	LogFormatHTML   LogFormat = "html"
)

type Logger struct {
	mu     sync.RWMutex
	format LogFormat
}

var defaultLogger = &Logger{format: LogFormatANSI}

var htmlStyles = map[COLOR]string{
	BLACK:         "color: #000000",
	RED:           "color: #ef4444",
	GREEN:         "color: #10b981",
	YELLOW:        "color: #f59e0b",
	BLUE:          "color: #3b82f6",
	PURPLE:        "color: #c678dd; font-weight: bold",
	CYAN:          "color: #56b6c2",
	WHITE:         "color: #f3f4f6",
	GREY:          "color: #5c6370",
	BRIGHT_RED:    "color: #f87171",
	BRIGHT_GREEN:  "color: #34d399",
	BRIGHT_YELLOW: "color: #fbbf24",
	BRIGHT_BLUE:   "color: #60a5fa",
	BRIGHT_PURPLE: "color: #c084fc",
	BRIGHT_CYAN:   "color: #22d3ee",
	BRIGHT_WHITE:  "color: #f9fafb",
	BOLD:          "font-weight: bold",
	BOLD_RED:      "color: #ef4444; font-weight: bold",
	BOLD_GREEN:    "color: #10b981; font-weight: bold",
	BOLD_YELLOW:   "color: #f59e0b; font-weight: bold",
	BOLD_BLUE:     "color: #3b82f6; font-weight: bold",
	BOLD_PURPLE:   "color: #a855f7; font-weight: bold",
	BOLD_CYAN:     "color: #56b6c2; font-weight: bold",
	BOLD_WHITE:    "color: #f3f4f6; font-weight: bold",
	ORANGE:        "color: #ff8700",
	BROWN:         "color: #af5f00",
	BRIGHT_BROWN:  "color: #af8700",
	PINK:          "color: #ff87ff",
	TEAL:          "color: #00af87",
	AQUA:          "color: #5fffff",
	MAGENTA:       "color: #ff00ff",
	LIGHT_GREY:    "color: #bcbcbc",
	DARK_GREY:     "color: #585858",
	LIGHT_ORANGE:  "color: #d19a66",
	LIGHT_BLUE:    "color: #5fd7ff",
	LIGHT_GREEN:   "color: #87ff87",
	LIGHT_YELLOW:  "color: #ffffaf",
}

func ParseLogFormat(raw string) (LogFormat, error) {
	switch LogFormat(strings.ToLower(strings.TrimSpace(raw))) {
	case "", LogFormatANSI:
		return LogFormatANSI, nil
	case LogFormatNormal:
		return LogFormatNormal, nil
	case LogFormatHTML:
		return LogFormatHTML, nil
	default:
		return "", fmt.Errorf("invalid log format %q (expected ansi, normal, or html)", raw)
	}
}

func SetLogFormat(format LogFormat) {
	defaultLogger.SetFormat(format)
}

func SetLogFormatString(raw string) error {
	format, err := ParseLogFormat(raw)
	if err != nil {
		return err
	}
	SetLogFormat(format)
	return nil
}

func CurrentLogFormat() LogFormat {
	return defaultLogger.Format()
}

func (l *Logger) SetFormat(format LogFormat) {
	if l == nil {
		return
	}
	if format == "" {
		format = LogFormatANSI
	}
	l.mu.Lock()
	l.format = format
	l.mu.Unlock()
}

func (l *Logger) Format() LogFormat {
	if l == nil {
		return LogFormatANSI
	}
	l.mu.RLock()
	format := l.format
	l.mu.RUnlock()
	if format == "" {
		return LogFormatANSI
	}
	return format
}

func renderText(color COLOR, text string) string {
	return defaultLogger.Render(color, text)
}

func (l *Logger) Render(color COLOR, text string) string {
	switch l.Format() {
	case LogFormatNormal:
		return text
	case LogFormatHTML:
		return renderHTML(color, text)
	default:
		if color == "" {
			return text
		}
		return string(color) + text + string(RESET)
	}
}

func renderHTML(color COLOR, text string) string {
	escaped := formatHTMLText(text, false)
	if color == "" {
		return escaped
	}
	style := htmlColorStyle(color)
	if style == "" {
		return escaped
	}
	return `<span style="` + style + `">` + escaped + `</span>`
}

func formatHTMLText(text string, preserveLayout bool) string {
	escaped := html.EscapeString(text)
	if !preserveLayout {
		return escaped
	}
	escaped = strings.ReplaceAll(escaped, "\n", "<br>")
	escaped = strings.ReplaceAll(escaped, "  ", "&nbsp;&nbsp;")
	return escaped
}

func htmlColorStyle(color COLOR) string {
	if color == GRAY {
		color = GREY
	}
	return htmlStyles[color]
}
