package main

import (
	"fmt"
	"os"

	"github.com/sipeed/suprclaw/cmd/suprclaw-launcher-tui/internal/ui"
)

func main() {
	if err := ui.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
