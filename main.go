package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// version and commit are stamped by scripts/build-release.sh with
// -X main.version=<tag> -X main.commit=<sha>.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "version") {
		fmt.Printf("cordglean %s (%s)\n", version, commit)
		return
	}
	audit := log.New(os.Stderr, "", 0)
	c, err := loadConfig(os.Getenv)
	if err != nil {
		audit.Print(err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newServer(newDiscordClient(c, nil), audit).Run(ctx, &mcp.StdioTransport{}); err != nil {
		audit.Print("cordglean stdio session failed")
		os.Exit(1)
	}
}
