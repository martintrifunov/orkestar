package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

var errSearchUsage = errors.New(`usage: orkestar search <query> [--workspace=<id>] [--limit=N] [--json]`)

// searchOutput is what the daemon returns from search.query.
type searchOutput struct {
	Query   string                `json:"query"`
	Results []daemon.SearchResult `json:"results"`
}

func runSearch(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errSearchUsage
	}
	query := ""
	workspaceID := ""
	limit := 0
	asJSON := false
	for _, argument := range args {
		switch {
		case argument == "--json":
			asJSON = true
		case strings.HasPrefix(argument, "--workspace="):
			workspaceID = strings.TrimPrefix(argument, "--workspace=")
		case strings.HasPrefix(argument, "--limit="):
			parsed, err := strconv.Atoi(strings.TrimPrefix(argument, "--limit="))
			if err != nil || parsed <= 0 {
				return fmt.Errorf("--limit must be a positive number")
			}
			limit = parsed
		case strings.HasPrefix(argument, "--"):
			return fmt.Errorf("unknown flag %q\n\n%s", argument, errSearchUsage.Error())
		default:
			if query != "" {
				query += " "
			}
			query += argument
		}
	}
	if query == "" {
		return errSearchUsage
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output searchOutput
	if err := ipc.NewClient(paths.Socket).Call(ctx, "search.query", map[string]any{
		"query": query, "workspace_id": workspaceID, "limit": limit,
	}, &output); err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(output)
	}
	for _, result := range output.Results {
		location := result.ID
		if result.Line > 0 {
			location = fmt.Sprintf("%s:%d", result.ID, result.Line)
		}
		fmt.Printf("%s\t%s\t%s\n", result.Kind, location, result.Snippet)
	}
	if len(output.Results) == 0 {
		fmt.Println("no matches")
	}
	return nil
}
