package main

import "github.com/vector76/tgask/cmd/tgask/cmd"

var version = "dev" // overwritten at compile time by -ldflags "-X main.version=..."

func main() {
	cmd.Execute(version)
}
