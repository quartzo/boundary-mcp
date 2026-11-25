// Package fbs wraps the FlatBuffers compiler (flatc) behind a small,
// deterministic API.
//
// The wrapper never edits schemas: it only invokes flatc and surfaces its
// diagnostics verbatim. Schema drift is rejected by flatc itself via
// [Compiler.Conform], which keeps the gatekeeper independent of any LLM.
package fbs

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotInstalled is returned when the flatc binary cannot be located.
var ErrNotInstalled = errors.New("fbs: flatc binary not found")

// Language identifies a code generation target supported by flatc.
type Language string

const (
	// Go generates Go bindings (flatc --go).
	Go Language = "go"
	// Rust generates Rust bindings (flatc --rust).
	Rust Language = "rust"
)

func (l Language) flag() (string, error) {
	switch l {
	case Go:
		return "--go", nil
	case Rust:
		return "--rust", nil
	default:
		return "", fmt.Errorf("fbs: unsupported language %q", l)
	}
}

type runFunc func(ctx context.Context, path string, args []string) (stdout, stderr string, err error)

// Compiler invokes the system flatc binary.
//
// A Compiler is safe for concurrent use.
type Compiler struct {
	path string
	run  runFunc
}

// NewCompiler returns a Compiler for the flatc binary at path. An empty path
// resolves "flatc" from PATH when a command is executed.
func NewCompiler(path string) *Compiler {
	return &Compiler{path: path, run: runCommand}
}

// Path returns the configured binary path (defaults to "flatc").
func (c *Compiler) Path() string {
	if c.path == "" {
		return "flatc"
	}
	return c.path
}

// GenerateOptions controls a single code generation run.
type GenerateOptions struct {
	// Schema is the path to the .fbs file to compile.
	Schema string
	// Language selects the output bindings.
	Language Language
	// OutDir is the directory that receives the generated sources (-o).
	OutDir string
	// IncludeDirs adds schema search paths (-I).
	IncludeDirs []string
}

// Generate compiles Schema into bindings for the requested language.
func (c *Compiler) Generate(ctx context.Context, opt GenerateOptions) error {
	flag, err := opt.Language.flag()
	if err != nil {
		return err
	}
	if opt.Schema == "" {
		return errors.New("fbs: schema path is required")
	}
	if opt.OutDir == "" {
		return errors.New("fbs: output directory is required")
	}

	args := []string{flag, "-o", opt.OutDir}
	args = appendIncludes(args, opt.IncludeDirs)
	args = append(args, opt.Schema)

	_, _, err = c.exec(ctx, args...)
	return err
}

// CompileBinarySchema compiles schema into a self-describing .bfbs file
// (flatc --binary --schema) and returns its path.
//
// The returned path is derived from the schema file name and is not checked
// for existence; a successful call to flatc is sufficient.
func (c *Compiler) CompileBinarySchema(ctx context.Context, schema, outDir string, includeDirs ...string) (string, error) {
	if schema == "" {
		return "", errors.New("fbs: schema path is required")
	}
	if outDir == "" {
		return "", errors.New("fbs: output directory is required")
	}

	args := []string{"--binary", "--schema", "-o", outDir}
	args = appendIncludes(args, includeDirs)
	args = append(args, schema)

	if _, _, err := c.exec(ctx, args...); err != nil {
		return "", err
	}

	base := strings.TrimSuffix(filepath.Base(schema), filepath.Ext(schema))
	return filepath.Join(outDir, base+".bfbs"), nil
}

// Conform verifies that newSchema is backward compatible with oldSchema
// (flatc --conform oldSchema newSchema).
//
// On a breaking change it returns a [*ConformError] carrying flatc's
// diagnostics, so callers can forward them to the proposing agent unchanged.
func (c *Compiler) Conform(ctx context.Context, oldSchema, newSchema string) error {
	if oldSchema == "" || newSchema == "" {
		return errors.New("fbs: both old and new schema paths are required")
	}

	_, _, err := c.exec(ctx, "--conform", oldSchema, newSchema)
	if err == nil {
		return nil
	}

	var cmdErr *CommandError
	if errors.As(err, &cmdErr) {
		return &ConformError{
			OldSchema:   oldSchema,
			NewSchema:   newSchema,
			Diagnostics: cmdErr.Stderr,
			Err:         err,
		}
	}
	return err
}

// Version returns the output of flatc --version.
func (c *Compiler) Version(ctx context.Context) (string, error) {
	stdout, _, err := c.exec(ctx, "--version")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(stdout), nil
}

// CommandError reports a flatc invocation that exited unsuccessfully.
type CommandError struct {
	Command  string
	Args     []string
	Stderr   string
	ExitCode int
	Err      error
}

func (e *CommandError) Error() string {
	invocation := strings.TrimSpace(e.Command + " " + strings.Join(e.Args, " "))
	if e.Stderr != "" {
		return fmt.Sprintf("fbs: %s: %v: %s", invocation, e.Err, e.Stderr)
	}
	return fmt.Sprintf("fbs: %s: %v", invocation, e.Err)
}

// Unwrap exposes the underlying process error.
func (e *CommandError) Unwrap() error { return e.Err }

// ConformError reports a schema that is not backward compatible.
type ConformError struct {
	OldSchema   string
	NewSchema   string
	Diagnostics string
	Err         error
}

func (e *ConformError) Error() string {
	msg := fmt.Sprintf("fbs: %s is not conformant with %s", e.NewSchema, e.OldSchema)
	switch {
	case e.Diagnostics != "":
		msg += ": " + e.Diagnostics
	case e.Err != nil:
		msg += ": " + e.Err.Error()
	}
	return msg
}

// Unwrap exposes the underlying command error.
func (e *ConformError) Unwrap() error { return e.Err }

func (c *Compiler) exec(ctx context.Context, args ...string) (string, string, error) {
	run := c.run
	if run == nil {
		run = runCommand
	}
	return run(ctx, c.Path(), args)
}

func runCommand(ctx context.Context, path string, args []string) (string, string, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err == nil {
		return stdout.String(), stderr.String(), nil
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, fs.ErrNotExist) {
		return stdout.String(), stderr.String(), fmt.Errorf("%w: %q", ErrNotInstalled, path)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return stdout.String(), stderr.String(), fmt.Errorf("fbs: %w", ctxErr)
	}

	exitCode := -1
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		exitCode = exitErr.ExitCode()
	}
	return stdout.String(), stderr.String(), &CommandError{
		Command:  path,
		Args:     args,
		Stderr:   strings.TrimSpace(stderr.String()),
		ExitCode: exitCode,
		Err:      err,
	}
}

func appendIncludes(args []string, dirs []string) []string {
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		args = append(args, "-I", dir)
	}
	return args
}
