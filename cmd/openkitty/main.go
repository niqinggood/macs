// Command openkitty preserves the legacy entry point. New builds use cmd/macs.
package main

import "mac/internal/app"

// CurrentVersion is overridden by release builds through -ldflags.
var CurrentVersion = "2.0.0"

func main() {
	app.CurrentVersion = CurrentVersion
	app.Main()
}
