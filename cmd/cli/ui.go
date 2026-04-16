package cli

import "fmt"

func printHeader(text string) {
	fmt.Printf("\n== %s ==\n", text)
}

func printSuccess(text string) {
	fmt.Printf("✓ %s\n", text)
}

func printInfo(text string) {
	fmt.Printf("ℹ %s\n", text)
}

func printWarning(text string) {
	fmt.Printf("⚠ %s\n", text)
}

func printError(text string) {
	fmt.Printf("✗ %s\n", text)
}

func printUpdate(text string) {
	fmt.Printf("↑ %s\n", text)
}

func printPackage(name, version string) {
	fmt.Printf("📦 %s @%s\n", name, version)
}

func printDim(text string) {
	fmt.Println(text)
}

func printDownload(text string) {
	fmt.Printf("  ↓ %s\n", text)
}

func printCached() {
	fmt.Println("  ✓ cached")
}

func printTransitive(dep, version string) {
	fmt.Printf("  └─ %s@%s\n", dep, version)
}
