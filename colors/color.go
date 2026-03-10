package colors

//import "fmt"

// ANSI color escape codes
type COLOR string

const (
	RESET COLOR = "\033[0m"

	// Basic Colors
	BLACK  COLOR = "\033[30m"
	RED    COLOR = "\033[31m"
	GREEN  COLOR = "\033[32m"
	YELLOW COLOR = "\033[33m"
	BLUE   COLOR = "\033[34m"
	PURPLE COLOR = "\033[35m"
	CYAN   COLOR = "\033[36m"
	WHITE  COLOR = "\033[37m"
	GREY   COLOR = "\033[90m"
	GRAY   COLOR = "\033[90m"

	// Bright Colors
	BRIGHT_RED    COLOR = "\033[91m"
	BRIGHT_GREEN  COLOR = "\033[92m"
	BRIGHT_YELLOW COLOR = "\033[93m"
	BRIGHT_BLUE   COLOR = "\033[94m"
	BRIGHT_PURPLE COLOR = "\033[95m"
	BRIGHT_CYAN   COLOR = "\033[96m"
	BRIGHT_WHITE  COLOR = "\033[97m"

	// Bold Variants
	BOLD        COLOR = "\033[1m"
	BOLD_RED    COLOR = "\033[1;31m"
	BOLD_GREEN  COLOR = "\033[1;32m"
	BOLD_YELLOW COLOR = "\033[1;33m"
	BOLD_BLUE   COLOR = "\033[1;34m"
	BOLD_PURPLE COLOR = "\033[1;35m"
	BOLD_CYAN   COLOR = "\033[1;36m"
	BOLD_WHITE  COLOR = "\033[1;37m"

	// Extended 256-color shades
	ORANGE       COLOR = "\033[38;5;208m"
	BROWN        COLOR = "\033[38;5;130m"
	BRIGHT_BROWN COLOR = "\033[38;5;136m"
	PINK         COLOR = "\033[38;5;213m"
	TEAL         COLOR = "\033[38;5;37m"
	AQUA         COLOR = "\033[38;5;87m"
	MAGENTA      COLOR = "\033[38;5;201m"
	LIGHT_GREY   COLOR = "\033[38;5;250m"
	DARK_GREY    COLOR = "\033[38;5;240m"
	//light orange
	LIGHT_ORANGE COLOR = "\033[38;5;215m"
	LIGHT_BLUE   COLOR = "\033[38;5;81m"
	LIGHT_GREEN  COLOR = "\033[38;5;120m"
	LIGHT_YELLOW COLOR = "\033[38;5;229m"
)

// list all colors for debug purposes
// var Colors = []COLOR{
// 	BLACK, RED, GREEN, YELLOW, BLUE, PURPLE, CYAN, WHITE,
// 	BRIGHT_RED, BRIGHT_GREEN, BRIGHT_YELLOW, BRIGHT_BLUE, BRIGHT_PURPLE, BRIGHT_CYAN, BRIGHT_WHITE,
// 	BOLD, BOLD_RED, BOLD_GREEN, BOLD_YELLOW, BOLD_BLUE, BOLD_PURPLE, BOLD_CYAN, BOLD_WHITE,
// 	ORANGE, BROWN, BRIGHT_BROWN, PINK, TEAL, AQUA, MAGENTA,
// 	LIGHT_GREY, DARK_GREY, LIGHT_ORANGE, LIGHT_BLUE, LIGHT_GREEN, LIGHT_YELLOW,
// }

// func PrintAllColors() {

// 	for _, color := range Colors {
// 		fmt.Printf("%sThis is a sample text in color %s%s\n", color, color, RESET)
// 	}
// }
