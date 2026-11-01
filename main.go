package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func main() {
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
