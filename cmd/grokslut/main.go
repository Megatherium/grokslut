package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Megatherium/grokslut/auth"
	"github.com/Megatherium/grokslut/exporter"
	"github.com/Megatherium/grokslut/gemini"
	"github.com/Megatherium/grokslut/grok"
	"github.com/Megatherium/grokslut/store"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, grok.ErrAuthExpired) {
			os.Exit(3)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		help()
		return nil
	}
	command := args[0]
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	provider := flags.String("provider", "grok", "history provider: grok or gemini")
	sessionPath := flags.String("session", "", "private session envelope JSON (defaults per provider)")
	baseURL := flags.String("base-url", "", "override provider base URL (for local test fixtures)")
	pageSize := flags.Int("page-size", 60, "conversation page size")
	cursor := flags.String("cursor", "", "pagination cursor")
	all := flags.Bool("all", false, "fetch every page")
	ids := flags.String("ids", "", "comma-separated conversation IDs")
	format := flags.String("format", "zip", "markdown, json, or zip")
	outDir := flags.String("out", "exports", "export directory")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if command != "list" && command != "verify" && command != "export" {
		return fmt.Errorf("unknown command %q", command)
	}
	if *provider != "grok" && *provider != "gemini" {
		return fmt.Errorf("provider must be grok or gemini")
	}
	if *sessionPath == "" {
		*sessionPath, _ = store.DefaultProviderSessionPath(*provider)
	}
	if *baseURL == "" {
		if *provider == "gemini" {
			*baseURL = "https://gemini.google.com"
		} else {
			*baseURL = "https://grok.com"
		}
	}
	session, err := auth.FromFile(*sessionPath)
	if err != nil {
		return err
	}
	var client commandClient
	if *provider == "gemini" {
		client, err = gemini.NewClient(session, *baseURL)
	} else {
		client, err = grok.NewClient(session, *baseURL)
	}
	if err != nil {
		return err
	}
	switch command {
	case "verify":
		if err := client.Verify(); err != nil {
			return err
		}
		return printJSON(map[string]any{"ok": true, "session": session.Summary()})
	case "list":
		if *all {
			conversations, err := client.ListAllConversations(*pageSize)
			if err != nil {
				return err
			}
			return printJSON(map[string]any{"conversations": conversations, "nextCursor": ""})
		}
		result, err := client.ListConversations(*pageSize, *cursor)
		if err != nil {
			return err
		}
		return printJSON(result)
	case "export":
		if *ids == "" {
			return errors.New("--ids ID,ID is required")
		}
		var selected []string
		for _, id := range strings.Split(*ids, ",") {
			if id = strings.TrimSpace(id); id != "" {
				selected = append(selected, id)
			}
		}
		result, err := (exporter.Exporter{Client: client}).Export(selected, exporter.Format(*format), filepath.Clean(*outDir), func(event exporter.Progress) {
			fmt.Fprintf(os.Stderr, "%s: %d/%d (%s)\n", event.Phase, event.Current, event.Total, event.ID)
		})
		if err != nil {
			return err
		}
		return printJSON(result)
	}
	return nil
}

type commandClient interface {
	exporter.HistoryClient
	Verify() error
	ListConversations(int, string) (grok.ListResult, error)
	ListAllConversations(int) ([]grok.ConversationSummary, error)
}

func printJSON(value any) error {
	output, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(output))
	return err
}
func help() {
	fmt.Print(`grokslut — personal Grok and Gemini conversation exporter

Usage:
  grokslut verify [--provider grok|gemini] [--session session.json]
  grokslut list [--provider grok|gemini] [--all] [--page-size 60]
  grokslut export [--provider grok|gemini] --ids ID,ID --format markdown|json|zip --out exports

The session envelope is private: it is not logged or persisted by the CLI.
`)
}
