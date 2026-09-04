// One-off probe: which config file does the loader pick, and what
// are the resolved values (masked).
package main

import (
	"fmt"

	"github.com/km269/wukong/internal/config"
)

func mask(s string) string {
	if len(s) == 0 {
		return "<EMPTY>"
	}
	if len(s) <= 8 {
		return "****"
	}
	return s[:6] + "****" + s[len(s)-4:]
}

func main() {
	loader, err := config.NewLoader("")
	if err != nil {
		fmt.Println("loader:", err)
		return
	}
	fmt.Println("config file used:", loader.ConfigFileUsed())
	cfg, err := loader.LoadAndValidate()
	if err != nil {
		fmt.Println("load:", err)
		return
	}
	fmt.Println("default_provider:", cfg.DefaultProvider)
	for _, p := range cfg.Providers {
		fmt.Printf("%-12s base=%-55s key=%s\n", p.Name, p.BaseURL, mask(p.APIKey))
	}
	fmt.Println("memory.extractor_provider:", cfg.Memory.ExtractorProvider)
}
