package main

import (
	"flag"
	"fmt"
	"os"

	"golang.org/x/term"

	"github.com/friedenberg/nebulous/internal/alfa/archivelist"
)

// archiveListMain is the `nebulous archive-list` subcommand entry
// point. Walks the archive root, projects each record to a Summary,
// optionally filters by a subject prefix, and renders via the
// caller-selected format (auto, table, jsonl).
func archiveListMain(args []string) int {
	fs := flag.NewFlagSet("archive-list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		archiveRoot = fs.String("archive-root", defaultArchiveRoot(), "directory for archive records")
		format      = fs.String("format", "auto", "output format: auto, table, jsonl")
	)

	if err := fs.Parse(args); err != nil {
		return 3
	}

	var prefix string
	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "archive-list: at most one positional argument (subject prefix) is allowed")
		return 3
	}
	if fs.NArg() == 1 {
		prefix = fs.Arg(0)
	}

	summaries, err := archivelist.Walk(archivelist.Options{
		Root:          *archiveRoot,
		SubjectPrefix: prefix,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "archive-list: %v\n", err)
		return 3
	}

	switch resolveFormat(*format, os.Stdout) {
	case "table":
		if err := archivelist.WriteTable(os.Stdout, summaries); err != nil {
			fmt.Fprintf(os.Stderr, "archive-list: %v\n", err)
			return 3
		}
	case "jsonl":
		if err := archivelist.WriteJSONL(os.Stdout, summaries); err != nil {
			fmt.Fprintf(os.Stderr, "archive-list: %v\n", err)
			return 3
		}
	default:
		fmt.Fprintf(os.Stderr, "archive-list: unknown --format %q (want auto, table, or jsonl)\n", *format)
		return 3
	}
	return 0
}

// resolveFormat maps the --format flag value plus stdout's TTY-ness
// to a concrete format name. "auto" picks table for TTY, jsonl
// otherwise. Unknown values pass through so the caller can report
// them.
func resolveFormat(requested string, stdout *os.File) string {
	if requested != "auto" {
		return requested
	}
	if term.IsTerminal(int(stdout.Fd())) {
		return "table"
	}
	return "jsonl"
}
