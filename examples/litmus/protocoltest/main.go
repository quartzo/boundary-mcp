// Command protocoltest is the reference protocol-test runner for the litmus
// example. It expands the normative spec and checks the structural cases
// against the schema; semantic assertions are reported for the implementation
// and CI to test.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"boundary-mcp/internal/protocoltest"
)

func main() {
	governance := flag.String("governance", "examples/litmus/governance", "governance directory")
	flag.Parse()

	specs, err := protocoltest.LoadSpecDir(filepath.Join(*governance, "protocol_tests"))
	if err != nil {
		fail("%v", err)
	}
	if len(specs) == 0 {
		fail("no protocol test specs under %s", filepath.Join(*governance, "protocol_tests"))
	}

	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)

	failed := false
	for _, name := range names {
		report, err := protocoltest.Check(context.Background(), protocoltest.ExpandOptions{
			GovernanceDir: *governance,
		}, specs[name])
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: ERROR: %v\n", name, err)
			failed = true
			continue
		}

		status := "PASS"
		if !report.Pass() {
			status = "FAIL"
			failed = true
		}
		fmt.Printf("%s: %s (cases=%d accept=%d reject=%d assertions=%d)\n",
			name, status, report.Cases, report.Accept, report.Reject, report.Assertions)
		if report.Roundtrip {
			fmt.Println("  roundtrip: delegated to the implementation's tests")
		}
		for _, failure := range report.Failures {
			fmt.Printf("  %s\n", failure)
		}
	}

	if failed {
		os.Exit(1)
	}
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "protocoltest: "+format+"\n", args...)
	os.Exit(1)
}
