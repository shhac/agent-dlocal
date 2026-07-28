package main

import (
	"github.com/shhac/agent-dlocal/internal/cli"
)

var version = "dev"

func main() {
	cli.Execute(version)
}
