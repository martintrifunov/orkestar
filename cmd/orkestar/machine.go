package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/martintrifunov/orkestar/internal/federation"
	"github.com/martintrifunov/orkestar/internal/ipc"
	"github.com/martintrifunov/orkestar/internal/machine"
	"github.com/martintrifunov/orkestar/internal/runtimepath"
)

var errMachineUsage = errors.New(`usage:
  orkestar machine add <[user@]host> [--label NAME] [--remote-session NAME]
  orkestar machine list [--json]
  orkestar machine status
  orkestar machine board
  orkestar machine call <machine-id> <method> [json-params]
  orkestar machine rename <machine-id> <label>
  orkestar machine enable|disable <machine-id>
  orkestar machine remove <machine-id>`)

// runMachine manages the saved ssh machines and the combined view across them.
// Everything local here is configuration; connecting to a machine is what
// --remote does, and the catalog never holds a credential.
func runMachine(paths runtimepath.Paths, args []string) error {
	if len(args) == 0 {
		return errMachineUsage
	}
	path, err := runtimepath.MachineCatalogPath()
	if err != nil {
		return err
	}
	catalog, err := machine.Load(path)
	if err != nil {
		return err
	}

	switch args[0] {
	case "add":
		if len(args) < 2 {
			return errMachineUsage
		}
		host := args[1]
		label, session := "", ""
		for index := 2; index < len(args); index++ {
			if value, ok := strings.CutPrefix(args[index], "--label="); ok {
				if value == "" {
					return errors.New("--label needs a value")
				}
				label = value
				continue
			}
			if value, ok := strings.CutPrefix(args[index], "--remote-session="); ok {
				if value == "" {
					return errors.New("--remote-session needs a value")
				}
				session = value
				continue
			}
			switch args[index] {
			case "--label":
				if index+1 >= len(args) {
					return errors.New("--label needs a value")
				}
				label = args[index+1]
				index++
			case "--remote-session":
				if index+1 >= len(args) {
					return errors.New("--remote-session needs a value")
				}
				session = args[index+1]
				index++
			default:
				return fmt.Errorf("unknown flag %q\n\n%s", args[index], errMachineUsage.Error())
			}
		}
		added, err := catalog.Add(label, host, session)
		if err != nil {
			return err
		}
		if err := catalog.Save(); err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", added.ID, added.Label, added.Host)
		return nil
	case "list":
		asJSON := len(args) == 2 && args[1] == "--json"
		if len(args) > 2 || (len(args) == 2 && !asJSON) {
			return errMachineUsage
		}
		if asJSON {
			return json.NewEncoder(os.Stdout).Encode(catalog.List())
		}
		for _, saved := range catalog.List() {
			state := "disabled"
			if saved.Enabled {
				state = "enabled"
			}
			session := saved.Session
			if session == "" {
				session = "-"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", saved.ID, saved.Label, state, saved.Host, session)
		}
		return nil
	case "rename":
		if len(args) != 3 {
			return errMachineUsage
		}
		updated, err := catalog.Rename(args[1], args[2])
		if err != nil {
			return err
		}
		if err := catalog.Save(); err != nil {
			return err
		}
		fmt.Printf("%s\t%s\n", updated.ID, updated.Label)
		return nil
	case "enable", "disable":
		if len(args) != 2 {
			return errMachineUsage
		}
		updated, err := catalog.SetEnabled(args[1], args[0] == "enable")
		if err != nil {
			return err
		}
		if err := catalog.Save(); err != nil {
			return err
		}
		fmt.Printf("%s\tenabled=%v\n", updated.ID, updated.Enabled)
		return nil
	case "remove":
		if len(args) != 2 {
			return errMachineUsage
		}
		if err := catalog.Remove(args[1]); err != nil {
			return err
		}
		if err := catalog.Save(); err != nil {
			return err
		}
		fmt.Printf("%s removed\n", args[1])
		return nil
	case "status":
		if len(args) != 1 {
			return errMachineUsage
		}
		manager := federation.New("Local", ipc.NewClient(paths.Socket), nil)
		manager.SetMachines(catalog.List())
		defer manager.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		manager.Refresh(ctx)
		for _, status := range manager.Status() {
			host := status.Machine.Host
			if status.Local {
				host = "-"
			}
			fmt.Printf("%s\t%s\t%s\t%s\t%s\n", status.Machine.Label, status.Machine.ID, status.State, host, status.Err)
		}
		return nil
	case "board":
		if len(args) != 1 {
			return errMachineUsage
		}
		manager := federation.New("Local", ipc.NewClient(paths.Socket), nil)
		manager.SetMachines(catalog.List())
		defer manager.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		manager.Refresh(ctx)
		return json.NewEncoder(os.Stdout).Encode(manager.Board())
	case "call":
		if len(args) < 3 || len(args) > 4 {
			return errMachineUsage
		}
		saved, err := catalog.Find(args[1])
		if err != nil {
			return err
		}
		var params any = map[string]any{}
		if len(args) == 4 {
			if err := json.Unmarshal([]byte(args[3]), &params); err != nil {
				return fmt.Errorf("params are not valid JSON: %w", err)
			}
		}
		client, err := federation.RemoteDialer(context.Background(), saved)
		if err != nil {
			return err
		}
		defer client.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var result any
		if err := client.Call(ctx, args[2], params, &result); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	default:
		return errMachineUsage
	}
}
