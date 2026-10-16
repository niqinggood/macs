package main

import "mac/internal/app"

// CurrentVersion is overridden by release builds through -ldflags.
var CurrentVersion = "1.1.8"

func main() {
	app.CurrentVersion = CurrentVersion
	app.Main()
}
