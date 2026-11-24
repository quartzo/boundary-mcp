package fbs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These tests exercise a real flatc binary. They are skipped when flatc is
// not available on PATH.

func TestIntegrationVersion(t *testing.T) {
	t.Parallel()

	c := realCompiler(t)
	version, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if !strings.HasPrefix(version, "flatc version") {
		t.Fatalf("Version = %q, want a flatc version string", version)
	}
}

func TestIntegrationGenerate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		lang   Language
		ext    string
		marker string
	}{
		{name: "go", lang: Go, ext: ".go", marker: "type EnrichRequest"},
		{name: "rust", lang: Rust, ext: ".rs", marker: "EnrichRequest"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			outDir := t.TempDir()
			err := realCompiler(t).Generate(context.Background(), GenerateOptions{
				Schema:   schemaPath("valid.fbs"),
				Language: tt.lang,
				OutDir:   outDir,
			})
			if err != nil {
				t.Fatalf("Generate(%s): %v", tt.lang, err)
			}

			files := generatedFiles(t, outDir, tt.ext)
			requireFileContaining(t, files, tt.marker)
		})
	}
}

func TestIntegrationCompileBinarySchema(t *testing.T) {
	t.Parallel()

	outDir := t.TempDir()
	got, err := realCompiler(t).CompileBinarySchema(context.Background(), schemaPath("valid.fbs"), outDir)
	if err != nil {
		t.Fatalf("CompileBinarySchema: %v", err)
	}

	info, err := os.Stat(got)
	if err != nil {
		t.Fatalf("compiled schema: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("compiled schema %s is empty", got)
	}
}

func TestIntegrationConform(t *testing.T) {
	t.Parallel()

	t.Run("compatible", func(t *testing.T) {
		t.Parallel()

		err := realCompiler(t).Conform(
			context.Background(),
			schemaPath("valid.fbs"),
			schemaPath("compatible.fbs"),
		)
		if err != nil {
			t.Fatalf("Conform(compatible): %v", err)
		}
	})

	t.Run("breaking", func(t *testing.T) {
		t.Parallel()

		err := realCompiler(t).Conform(
			context.Background(),
			schemaPath("valid.fbs"),
			schemaPath("breaking.fbs"),
		)
		if err == nil {
			t.Fatal("Conform(breaking) returned nil error")
		}

		var conformErr *ConformError
		if !errors.As(err, &conformErr) {
			t.Fatalf("err = %T, want *ConformError", err)
		}
		if !strings.Contains(conformErr.Diagnostics, "document_id") {
			t.Fatalf("diagnostics = %q, want the deleted field name", conformErr.Diagnostics)
		}
	})
}

func realCompiler(t *testing.T) *Compiler {
	t.Helper()

	path, err := exec.LookPath("flatc")
	if err != nil {
		t.Skip("flatc not installed; skipping integration test")
	}
	return NewCompiler(path)
}

func schemaPath(name string) string {
	return filepath.Join("testdata", name)
}

func generatedFiles(t *testing.T, dir, ext string) []string {
	t.Helper()

	var files []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(path, ext) {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return files
}

func requireFileContaining(t *testing.T, files []string, marker string) {
	t.Helper()

	if len(files) == 0 {
		t.Fatal("flatc generated no files")
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(data), marker) {
			return
		}
	}
	t.Fatalf("no generated file contains %q (files: %v)", marker, files)
}
