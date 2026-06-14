// Command skillm manages AI-agent Skills: it keeps every skill in one central
// Home and links them (via symlinks) into the skill folders agents read.
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/charmbracelet/fang"

	"github.com/ultrakorne/skillm/cmd"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	if err := fang.Execute(ctx, cmd.Root(), fang.WithVersion(cmd.Version())); err != nil {
		os.Exit(1)
	}
}
