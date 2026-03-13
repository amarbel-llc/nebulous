package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"

	"github.com/amarbel-llc/purse-first/libs/go-mcp/server"
	"github.com/amarbel-llc/purse-first/libs/go-mcp/transport"
	"github.com/friedenberg/nebulous/internal/newsblur"
	"github.com/friedenberg/nebulous/internal/tools"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "nebulous — a NewsBlur MCP server\n\n")
		fmt.Fprintf(os.Stderr, "Usage:\n")
		fmt.Fprintf(os.Stderr, "  nebulous [flags]\n\n")
		fmt.Fprintf(os.Stderr, "Environment:\n")
		fmt.Fprintf(os.Stderr, "  NEWSBLUR_TOKEN  NewsBlur session cookie (required)\n\n")
		fmt.Fprintf(os.Stderr, "Flags:\n")
		flag.PrintDefaults()
	}

	flag.Parse()

	token := os.Getenv("NEWSBLUR_TOKEN")
	if token == "" {
		log.Fatal("NEWSBLUR_TOKEN environment variable is required")
	}

	client := newsblur.NewClient(token)
	app := tools.RegisterAll(client)

	if flag.NArg() >= 1 && flag.Arg(0) == "generate-plugin" {
		if err := app.HandleGeneratePlugin(flag.Args()[1:], os.Stdout); err != nil {
			log.Fatalf("generating plugin: %v", err)
		}
		return
	}

	if flag.NArg() >= 1 && flag.Arg(0) == "hook" {
		if err := app.HandleHook(os.Stdin, os.Stdout); err != nil {
			log.Fatalf("handling hook: %v", err)
		}
		return
	}

	if flag.NArg() > 0 {
		fmt.Fprintf(os.Stderr, "nebulous: unexpected arguments: %v\n", flag.Args())
		flag.Usage()
		os.Exit(1)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	t := transport.NewStdio(os.Stdin, os.Stdout)

	registry := server.NewToolRegistryV1()
	app.RegisterMCPToolsV1(registry)

	srv, err := server.New(t, server.Options{
		ServerName:    app.Name,
		ServerVersion: app.Version,
		Instructions:  "NewsBlur MCP server. Provides tools for reading feeds, managing stories, subscriptions, folders, and OPML import/export.",
		Tools:         registry,
	})
	if err != nil {
		log.Fatalf("creating server: %v", err)
	}

	if err := srv.Run(ctx); err != nil {
		log.Fatalf("server error: %v", err)
	}
}
