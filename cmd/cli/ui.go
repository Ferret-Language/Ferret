package cli

import (
	"compiler/colors"
	"fmt"
)

// UI helpers for friendly CLI output

func printHeader(text string) {
	fmt.Println()
	colors.BOLD_CYAN.Println("┏━━ " + text)
}

func printSuccess(text string) {
	colors.GREEN.Print("✓ ")
	fmt.Println(text)
}

func printInfo(text string) {
	colors.CYAN.Print("ℹ ")
	fmt.Println(text)
}

func printWarning(text string) {
	colors.YELLOW.Print("⚠ ")
	fmt.Println(text)
}

func printError(text string) {
	colors.RED.Print("✗ ")
	fmt.Println(text)
}

func printProgress(text string) {
	colors.BLUE.Print("→ ")
	fmt.Println(text)
}

func printUpdate(text string) {
	colors.YELLOW.Print("↑ ")
	fmt.Println(text)
}

func printPackage(name, version string) {
	colors.PURPLE.Print("📦 ")
	colors.BOLD.Print(name)
	colors.GREY.Printf(" @%s\n", version)
}

func printDim(text string) {
	colors.GREY.Println(text)
}

func printDownload(text string) {
	colors.BLUE.Print("  ↓ ")
	fmt.Println(text)
}

func printCached() {
	colors.GREY.Println("  ✓ cached")
}

func printTransitive(dep, version string) {
	colors.GREY.Print("  └─ ")
	colors.CYAN.Printf("%s@%s\n", dep, version)
}
