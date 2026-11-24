package fbs

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestGenerateInvokesFlatc(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	c := newTestCompiler(runner)
	outDir := t.TempDir()

	err := c.Generate(context.Background(), GenerateOptions{
		Schema:      "/schemas/protocol.fbs",
		Language:    Go,
		OutDir:      outDir,
		IncludeDirs: []string{"/include/a", ""},
	})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	want := []string{"--go", "-o", outDir, "-I", "/include/a", "/schemas/protocol.fbs"}
	assertCall(t, runner, "/opt/flatc", want)
}

func TestGenerateLanguageFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		lang Language
		flag string
	}{
		{Go, "--go"},
		{Rust, "--rust"},
	}

	for _, tt := range tests {
		t.Run(string(tt.lang), func(t *testing.T) {
			t.Parallel()

			runner := &fakeRunner{}
			err := newTestCompiler(runner).Generate(context.Background(), GenerateOptions{
				Schema:   "protocol.fbs",
				Language: tt.lang,
				OutDir:   t.TempDir(),
			})
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}

			if got := runner.calls[0].args[0]; got != tt.flag {
				t.Fatalf("first flatc arg = %q, want %q", got, tt.flag)
			}
		})
	}
}

func TestGenerateRejectsUnsupportedLanguage(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	err := newTestCompiler(runner).Generate(context.Background(), GenerateOptions{
		Schema:   "protocol.fbs",
		Language: Language("python"),
		OutDir:   t.TempDir(),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported language") {
		t.Fatalf("err = %v, want unsupported language error", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("flatc was invoked %d times, want 0", len(runner.calls))
	}
}

func TestGenerateRequiresSchemaAndOutput(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	c := newTestCompiler(runner)
	ctx := context.Background()

	if err := c.Generate(ctx, GenerateOptions{Language: Go, OutDir: "out"}); err == nil {
		t.Fatal("Generate without schema returned nil error")
	}
	if err := c.Generate(ctx, GenerateOptions{Language: Go, Schema: "s.fbs"}); err == nil {
		t.Fatal("Generate without output directory returned nil error")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("flatc was invoked %d times, want 0", len(runner.calls))
	}
}

func TestCompileBinarySchema(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	outDir := t.TempDir()

	got, err := newTestCompiler(runner).CompileBinarySchema(
		context.Background(),
		"/schemas/protocol.fbs",
		outDir,
		"/include/a",
	)
	if err != nil {
		t.Fatalf("CompileBinarySchema: %v", err)
	}

	wantPath := filepath.Join(outDir, "protocol.bfbs")
	if got != wantPath {
		t.Fatalf("path = %q, want %q", got, wantPath)
	}

	wantArgs := []string{"--binary", "--schema", "-o", outDir, "-I", "/include/a", "/schemas/protocol.fbs"}
	assertCall(t, runner, "/opt/flatc", wantArgs)
}

func TestConformPasses(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	if err := newTestCompiler(runner).Conform(context.Background(), "old.fbs", "new.fbs"); err != nil {
		t.Fatalf("Conform: %v", err)
	}

	assertCall(t, runner, "/opt/flatc", []string{"--conform", "old.fbs", "new.fbs"})
}

func TestConformReportsBreakingChange(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{
		stderr: "error: field 'x' removed or id altered",
		err: &CommandError{
			Command:  "/opt/flatc",
			Args:     []string{"--conform", "old.fbs", "new.fbs"},
			Stderr:   "error: field 'x' removed or id altered",
			ExitCode: 1,
			Err:      errors.New("exit status 1"),
		},
	}

	err := newTestCompiler(runner).Conform(context.Background(), "old.fbs", "new.fbs")
	if err == nil {
		t.Fatal("Conform returned nil error for a breaking change")
	}

	var conformErr *ConformError
	if !errors.As(err, &conformErr) {
		t.Fatalf("err = %T, want *ConformError", err)
	}
	if !strings.Contains(conformErr.Diagnostics, "field 'x' removed") {
		t.Fatalf("diagnostics = %q, want flatc stderr", conformErr.Diagnostics)
	}

	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("err = %T, want wrapped *CommandError", err)
	}
	if cmdErr.ExitCode != 1 {
		t.Fatalf("ExitCode = %d, want 1", cmdErr.ExitCode)
	}
}

func TestConformRequiresBothSchemas(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{}
	c := newTestCompiler(runner)

	if err := c.Conform(context.Background(), "", "new.fbs"); err == nil {
		t.Fatal("Conform with empty old schema returned nil error")
	}
	if err := c.Conform(context.Background(), "old.fbs", ""); err == nil {
		t.Fatal("Conform with empty new schema returned nil error")
	}
	if len(runner.calls) != 0 {
		t.Fatalf("flatc was invoked %d times, want 0", len(runner.calls))
	}
}

func TestVersion(t *testing.T) {
	t.Parallel()

	runner := &fakeRunner{stdout: "flatc version 25.2.10\n"}

	got, err := newTestCompiler(runner).Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if got != "flatc version 25.2.10" {
		t.Fatalf("Version = %q", got)
	}
}

func TestCompilerPath(t *testing.T) {
	t.Parallel()

	if got := NewCompiler("").Path(); got != "flatc" {
		t.Fatalf("Path() = %q, want %q", got, "flatc")
	}
	if got := NewCompiler("/opt/flatc").Path(); got != "/opt/flatc" {
		t.Fatalf("Path() = %q, want %q", got, "/opt/flatc")
	}
}

func TestZeroValueCompilerFallsBackToExec(t *testing.T) {
	t.Parallel()

	c := &Compiler{path: filepath.Join(t.TempDir(), "missing-flatc")}
	if _, err := c.Version(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

func TestCommandErrorFormatting(t *testing.T) {
	t.Parallel()

	withStderr := &CommandError{
		Command: "flatc",
		Args:    []string{"--conform", "old.fbs", "new.fbs"},
		Stderr:  "field removed",
		Err:     errors.New("exit status 1"),
	}
	if msg := withStderr.Error(); !strings.Contains(msg, "field removed") || !strings.Contains(msg, "--conform") {
		t.Fatalf("Error() = %q, want command args and stderr", msg)
	}

	withoutStderr := &CommandError{Command: "flatc", Err: errors.New("exit status 1")}
	if msg := withoutStderr.Error(); !strings.Contains(msg, "exit status 1") {
		t.Fatalf("Error() = %q, want underlying error", msg)
	}
}

func TestConformErrorFallsBackToCause(t *testing.T) {
	t.Parallel()

	err := &ConformError{OldSchema: "old.fbs", NewSchema: "new.fbs", Err: errors.New("exit status 1")}
	if msg := err.Error(); !strings.Contains(msg, "exit status 1") {
		t.Fatalf("Error() = %q, want underlying cause", msg)
	}
}

func TestMissingBinary(t *testing.T) {
	t.Parallel()

	c := NewCompiler(filepath.Join(t.TempDir(), "no-such-flatc"))
	if _, err := c.Version(context.Background()); !errors.Is(err, ErrNotInstalled) {
		t.Fatalf("err = %v, want ErrNotInstalled", err)
	}
}

func TestCommandErrorFromProcess(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("test invokes a POSIX shell")
	}

	_, _, err := runCommand(context.Background(), "/bin/sh", []string{"-c", "echo boom >&2; exit 3"})

	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		t.Fatalf("err = %T, want *CommandError", err)
	}
	if cmdErr.ExitCode != 3 {
		t.Fatalf("ExitCode = %d, want 3", cmdErr.ExitCode)
	}
	if cmdErr.Stderr != "boom" {
		t.Fatalf("Stderr = %q, want %q", cmdErr.Stderr, "boom")
	}
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("test invokes a POSIX shell")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, _, err := runCommand(ctx, "/bin/sh", []string{"-c", "exec sleep 5"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context.DeadlineExceeded", err)
	}
}

type fakeCall struct {
	path string
	args []string
}

type fakeRunner struct {
	calls  []fakeCall
	stdout string
	stderr string
	err    error
}

func (f *fakeRunner) run(_ context.Context, path string, args []string) (string, string, error) {
	f.calls = append(f.calls, fakeCall{path: path, args: slices.Clone(args)})
	return f.stdout, f.stderr, f.err
}

func newTestCompiler(runner *fakeRunner) *Compiler {
	c := NewCompiler("/opt/flatc")
	c.run = runner.run
	return c
}

func assertCall(t *testing.T, runner *fakeRunner, wantPath string, wantArgs []string) {
	t.Helper()

	if len(runner.calls) != 1 {
		t.Fatalf("flatc invoked %d times, want 1", len(runner.calls))
	}
	call := runner.calls[0]
	if call.path != wantPath {
		t.Fatalf("flatc path = %q, want %q", call.path, wantPath)
	}
	if !slices.Equal(call.args, wantArgs) {
		t.Fatalf("flatc args = %q, want %q", call.args, wantArgs)
	}
}
