package main

import (
	"os"

	"github.com/metadist/synaplan-opencloud/pkg/command"
	"github.com/metadist/synaplan-opencloud/pkg/config/defaults"
)

func main() {
	cfg := defaults.FullDefaultConfig()
	if err := command.Execute(cfg); err != nil {
		os.Exit(1)
	}
}
