package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/daemon"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

var errPolicyUsage = errors.New(`usage:
  orkestar policy audit [--workspace=<id>] [--json]
  orkestar policy check <adapter> <tool> [target] [--workspace=<id>]`)

func runPolicy(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errPolicyUsage
	}
	switch args[0] {
	case "audit":
		return policyAudit(paths, args[1:])
	case "check":
		return policyCheck(paths, args[1:])
	default:
		return errPolicyUsage
	}
}

// policyAudit prints the permission decisions a policy made, so "why was this
// approved" can be answered after the fact.
func policyAudit(paths runtimepath.Paths, args []string) error {
	workspaceID := ""
	asJSON := false
	for _, argument := range args {
		switch {
		case argument == "--json":
			asJSON = true
		case strings.HasPrefix(argument, "--workspace="):
			workspaceID = strings.TrimPrefix(argument, "--workspace=")
		default:
			return fmt.Errorf("unknown flag %q\n\n%s", argument, errPolicyUsage.Error())
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var report daemon.PolicyAuditReport
	if err := ipc.NewClient(paths.Socket).Call(ctx, "policy.audit", map[string]string{
		"workspace_id": workspaceID,
	}, &report); err != nil {
		return err
	}
	if asJSON {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	if report.Error != "" {
		fmt.Fprintf(os.Stderr, "policy problem: %s\n", report.Error)
	}
	if len(report.Entries) == 0 {
		fmt.Println("no policy decisions recorded")
		return nil
	}
	for _, entry := range report.Entries {
		target := entry.Target
		if target == "" {
			target = "-"
		}
		fmt.Printf("%s\t%s\t%s\t%s\t%s\t%s\n",
			entry.At.Format(time.RFC3339), entry.Decision, entry.Adapter, entry.Tool, target, entry.Rule)
	}
	return nil
}

// policyCheck answers what the policy would decide for a request, which is
// also how a file is validated before it is relied on.
func policyCheck(paths runtimepath.Paths, args []string) error {
	workspaceID := ""
	var positional []string
	for _, argument := range args {
		if strings.HasPrefix(argument, "--workspace=") {
			workspaceID = strings.TrimPrefix(argument, "--workspace=")
			continue
		}
		if strings.HasPrefix(argument, "--") {
			return fmt.Errorf("unknown flag %q\n\n%s", argument, errPolicyUsage.Error())
		}
		positional = append(positional, argument)
	}
	if len(positional) < 2 || len(positional) > 3 {
		return errPolicyUsage
	}
	target := ""
	if len(positional) == 3 {
		target = positional[2]
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var result map[string]string
	if err := ipc.NewClient(paths.Socket).Call(ctx, "policy.check", map[string]string{
		"workspace_id": workspaceID, "adapter": positional[0], "tool": positional[1], "target": target,
	}, &result); err != nil {
		return err
	}
	fmt.Printf("%s\t%s\n", result["decision"], result["rule"])
	if result["error"] != "" {
		fmt.Fprintf(os.Stderr, "policy problem: %s\n", result["error"])
	}
	return nil
}
