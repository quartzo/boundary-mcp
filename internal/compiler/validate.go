package compiler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"boundary-mcp/internal/fbs"
)

func validateStateMachine(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("is empty")
	}
	var value any
	if err := json.Unmarshal([]byte(raw), &value); err != nil {
		return err
	}
	if _, ok := value.(map[string]any); !ok {
		return errors.New("must be a JSON object")
	}
	return nil
}

func (c *Compiler) validateProtocol(ctx context.Context, base, candidate string) error {
	tmp, err := os.MkdirTemp("", "boundary-protocol-*")
	if err != nil {
		return fmt.Errorf("compiler: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	basePath := filepath.Join(tmp, "base.fbs")
	nextPath := filepath.Join(tmp, "next.fbs")
	if err := os.WriteFile(basePath, []byte(base), 0o644); err != nil {
		return fmt.Errorf("compiler: write base schema: %w", err)
	}
	if err := os.WriteFile(nextPath, []byte(candidate), 0o644); err != nil {
		return fmt.Errorf("compiler: write candidate schema: %w", err)
	}

	if _, err := c.opts.Flatc.CompileBinarySchema(ctx, nextPath, tmp); err != nil {
		return fmt.Errorf("syntax: %w", err)
	}
	if err := c.opts.Flatc.Conform(ctx, basePath, nextPath); err != nil {
		return fmt.Errorf("compatibility: %w", err)
	}
	return nil
}

func (c *Compiler) dryRunSDKs(ctx context.Context, protocol string) error {
	if len(c.opts.SDKTargets) == 0 {
		return nil
	}
	tmp, err := os.MkdirTemp("", "boundary-sdk-*")
	if err != nil {
		return fmt.Errorf("compiler: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(tmp) }()

	protocolPath := filepath.Join(tmp, "protocol.fbs")
	if err := os.WriteFile(protocolPath, []byte(protocol), 0o644); err != nil {
		return fmt.Errorf("compiler: write candidate schema: %w", err)
	}

	for _, target := range c.opts.SDKTargets {
		outDir := filepath.Join(tmp, string(target.Language))
		if err := c.opts.Flatc.Generate(ctx, fbs.GenerateOptions{
			Schema:   protocolPath,
			Language: target.Language,
			OutDir:   outDir,
		}); err != nil {
			return fmt.Errorf("generate %s: %w", target.Language, err)
		}
	}
	return nil
}

func (c *Compiler) generateSDKs(ctx context.Context) error {
	for _, target := range c.opts.SDKTargets {
		if err := os.MkdirAll(target.Dir, 0o755); err != nil {
			return fmt.Errorf("create %s: %w", target.Dir, err)
		}
		if err := c.opts.Flatc.Generate(ctx, fbs.GenerateOptions{
			Schema:   c.path("protocol.fbs"),
			Language: target.Language,
			OutDir:   target.Dir,
		}); err != nil {
			return fmt.Errorf("generate %s: %w", target.Language, err)
		}
	}
	return nil
}
