// Command versionctl manages content-addressed protocol versions.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"boundary-mcp/internal/fbs"
	"boundary-mcp/internal/governance"
	"boundary-mcp/internal/versioning"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "snapshot":
		err = runSnapshot(os.Args[2:])
	case "compat":
		err = runCompat(os.Args[2:])
	case "list":
		err = runList(os.Args[2:])
	case "show":
		err = runShow(os.Args[2:])
	case "alias":
		err = runAlias(os.Args[2:])
	case "pin":
		err = runPin(os.Args[2:])
	case "help", "-h", "--help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintln(os.Stderr, "versionctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stdout, `versionctl manages content-addressed protocol versions.

Usage:
  versionctl <command> [flags]

Commands:
  snapshot   Snapshot the current governance bundle as an immutable version.
  compat     Compare the current bundle with a stored base version.
  list       List versions, aliases and actor adoption.
  show       Show a version's metadata.
  alias      Bind a human alias to a version.
  pin        Record that an actor adopted a version.

Run "versionctl <command> -h" for command flags.
`)
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("versionctl "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	return fs
}

func runSnapshot(args []string) error {
	fs := newFlagSet("snapshot")
	governance := fs.String("governance", "governance", "governance directory")
	alias := fs.String("alias", "", "optional human alias, e.g. v0")
	actor := fs.String("actor", "manual", "actor or author recorded in the metadata")
	intent := fs.String("intent", "", "short intent recorded in the metadata")
	base := fs.String("base", "", "optional base version (alias or hash)")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store := versioning.NewStore(*governance)
	bundle, err := store.LoadBundle(*governance)
	if err != nil {
		return err
	}

	baseHash := ""
	if strings.TrimSpace(*base) != "" {
		baseHash, err = store.Resolve(*base)
		if err != nil {
			return err
		}
	}

	version, err := store.Snapshot(bundle, versioning.Meta{
		Actor:  *actor,
		Intent: *intent,
		Base:   baseHash,
	}, *alias)
	if err != nil {
		return err
	}

	if *jsonOut {
		return printJSON(version)
	}
	aliases, _ := store.Aliases()
	fmt.Printf("version:   %s\n", version.Version)
	fmt.Printf("wire_hash: %s\n", version.WireHash)
	fmt.Printf("path:      %s\n", store.VersionDir(version.Version))
	if names := aliasesFor(aliases, version.Version); len(names) > 0 {
		fmt.Printf("aliases:   %s\n", strings.Join(names, ", "))
	}
	return nil
}

func runCompat(args []string) error {
	fs := newFlagSet("compat")
	governance := fs.String("governance", "governance", "governance directory")
	base := fs.String("base", "", "base version (alias or hash)")
	flatcPath := fs.String("flatc", "", "flatc binary (default: PATH)")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*base) == "" {
		return errors.New("-base is required")
	}

	store := versioning.NewStore(*governance)
	baseVersion, baseBundle, err := store.Load(*base)
	if err != nil {
		return err
	}
	candidate, err := store.LoadBundle(*governance)
	if err != nil {
		return err
	}

	compat, err := versioning.Analyze(
		context.Background(),
		fbs.NewCompiler(*flatcPath),
		store.VersionDir(baseVersion.Version),
		*governance,
	)
	if err != nil {
		if errors.Is(err, fbs.ErrNotInstalled) {
			return fmt.Errorf("%w (install flatc or pass -flatc)", err)
		}
		return err
	}
	compat.Base = baseVersion.Version
	compat.Candidate = candidate.VersionHash()
	versioning.AddBundleChanges(compat, baseBundle, candidate)

	path, err := store.WriteCompat(*compat)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(compat)
	}

	fmt.Printf("base:      %s\n", compat.Base)
	fmt.Printf("candidate: %s\n", compat.Candidate)
	fmt.Printf("wire:      %s\n", compat.Wire)
	fmt.Printf("class:     %s\n", compat.Class)
	fmt.Printf("review:    %t\n", compat.ReviewRequired)
	fmt.Printf("manifest:  %s\n", path)
	for _, change := range compat.Changes {
		line := "  - " + change.Kind
		if change.Object != "" {
			line += " " + change.Object
		}
		if change.Field != "" {
			line += "." + change.Field
		}
		if change.Enum != "" {
			line += " " + change.Enum
		}
		if change.Value != "" {
			line += " " + change.Value
		}
		if change.Detail != "" {
			line += " (" + change.Detail + ")"
		}
		fmt.Println(line)
	}
	if strings.TrimSpace(compat.Evidence.Diagnostics) != "" {
		fmt.Println("diagnostics:")
		fmt.Println(compat.Evidence.Diagnostics)
	}
	return nil
}

func runList(args []string) error {
	fs := newFlagSet("list")
	governance := fs.String("governance", "governance", "governance directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}

	store := versioning.NewStore(*governance)
	versions, err := store.List()
	if err != nil {
		return err
	}
	aliases, err := store.Aliases()
	if err != nil {
		return err
	}
	pins, err := store.Actors()
	if err != nil {
		return err
	}

	adopters := make(map[string][]string, len(versions))
	for _, pin := range pins {
		adopters[pin.Version] = append(adopters[pin.Version], pin.Actor)
	}

	if *jsonOut {
		return printJSON(struct {
			Aliases  map[string]string     `json:"aliases"`
			Versions []versioning.Version  `json:"versions"`
			Actors   []versioning.ActorPin `json:"actors"`
		}{
			Aliases:  aliases,
			Versions: versions,
			Actors:   pins,
		})
	}

	if len(versions) == 0 {
		fmt.Println("no versions")
		return nil
	}
	fmt.Printf("%-16s %-22s %-22s %-5s %s\n", "ALIASES", "VERSION", "CREATED", "SIDES", "ADOPTERS")
	for _, version := range versions {
		names := aliasesFor(aliases, version.Version)
		adopted := adopters[version.Version]
		fmt.Printf("%-16s %-22s %-22s %-5d %s\n",
			strings.Join(names, ","),
			shortHash(version.Version),
			version.CreatedAt.Format(time.RFC3339),
			len(adopted),
			strings.Join(adopted, ","),
		)
	}
	return nil
}

func runShow(args []string) error {
	fs := newFlagSet("show")
	governance := fs.String("governance", "governance", "governance directory")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: versionctl show [flags] <alias|hash>")
	}

	store := versioning.NewStore(*governance)
	version, _, err := store.Load(fs.Arg(0))
	if err != nil {
		return err
	}
	aliases, err := store.Aliases()
	if err != nil {
		return err
	}
	names := aliasesFor(aliases, version.Version)

	if *jsonOut {
		return printJSON(struct {
			Version versioning.Version `json:"version"`
			Aliases []string           `json:"aliases,omitempty"`
		}{Version: version, Aliases: names})
	}
	fmt.Printf("version:   %s\n", version.Version)
	fmt.Printf("wire_hash: %s\n", version.WireHash)
	fmt.Printf("created:   %s\n", version.CreatedAt.Format(time.RFC3339))
	if version.Actor != "" {
		fmt.Printf("actor:     %s\n", version.Actor)
	}
	if version.Intent != "" {
		fmt.Printf("intent:    %s\n", version.Intent)
	}
	if version.Base != "" {
		fmt.Printf("base:      %s\n", version.Base)
	}
	if len(names) > 0 {
		fmt.Printf("aliases:   %s\n", strings.Join(names, ", "))
	}
	fmt.Println("files:")
	for _, file := range version.Files {
		fmt.Printf("  %-20s %s (%d bytes)\n", file.Name, file.SHA256, file.Size)
	}
	return nil
}

func runAlias(args []string) error {
	fs := newFlagSet("alias")
	governance := fs.String("governance", "governance", "governance directory")
	force := fs.Bool("force", false, "move an existing alias")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return errors.New("usage: versionctl alias [flags] <alias> <version>")
	}

	store := versioning.NewStore(*governance)
	alias, ref := fs.Arg(0), fs.Arg(1)
	if err := store.SetAlias(alias, ref, *force); err != nil {
		return err
	}
	version, err := store.Resolve(alias)
	if err != nil {
		return err
	}
	fmt.Printf("%s -> %s\n", alias, version)
	return nil
}

func runPin(args []string) error {
	fs := newFlagSet("pin")
	governanceDir := fs.String("governance", "governance", "governance directory")
	actor := fs.String("actor", "", "actor name")
	role := fs.String("role", "", "actor role, e.g. producer or consumer")
	ref := fs.String("version", "", "version (alias or hash)")
	status := fs.String("status", "active", "adoption status")
	jsonOut := fs.Bool("json", false, "print JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*actor) == "" {
		return errors.New("-actor is required")
	}
	if strings.TrimSpace(*ref) == "" {
		return errors.New("-version is required")
	}

	purpose, err := governance.LoadPurpose(filepath.Join(*governanceDir, "purpose.md"))
	if err != nil {
		return err
	}
	if !purpose.HasActor(*actor) {
		return fmt.Errorf("actor %q is not listed in purpose.md", *actor)
	}

	store := versioning.NewStore(*governanceDir)
	pin, err := store.PinActor(*actor, *role, *ref, *status)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(pin)
	}
	fmt.Printf("actor:   %s\n", pin.Actor)
	if pin.Role != "" {
		fmt.Printf("role:    %s\n", pin.Role)
	}
	fmt.Printf("version: %s\n", pin.Version)
	if pin.Alias != "" {
		fmt.Printf("alias:   %s\n", pin.Alias)
	}
	fmt.Printf("status:  %s\n", pin.Status)
	if pins, err := store.Actors(); err == nil {
		active := 0
		for _, other := range pins {
			if other.Version == pin.Version && other.Status == "active" {
				active++
			}
		}
		if active > 1 {
			fmt.Fprintf(os.Stderr, "note: %s is shared by %d actors; future changes require compatibility review\n", shortHash(pin.Version), active)
		}
	}
	return nil
}

func aliasesFor(aliases map[string]string, version string) []string {
	var names []string
	for name, hash := range aliases {
		if hash == version {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func shortHash(version string) string {
	raw := strings.TrimPrefix(version, "sha256:")
	if len(raw) > 12 {
		raw = raw[:12]
	}
	return "sha256:" + raw
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}
