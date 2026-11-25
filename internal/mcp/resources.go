package mcp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"

	"boundary-mcp/internal/protocoltest"
	"boundary-mcp/internal/versioning"
)

// Resource URI templates. Protocol resources are addressed by the bundle
// content hash, so the same URI always names the same contract.
const (
	protocolFBSURITemplate     = "boundary://protocol/{hash}/fbs"
	protocolPurposeURITemplate = "boundary://protocol/{hash}/purpose"
	protocolTestsURITemplate   = "boundary://protocol/{hash}/tests"
	actorContextURITemplate    = "boundary://actor/{actor}/context"
	protocolURIPrefix          = "boundary://protocol/"
	actorURIPrefix             = "boundary://actor/"
	protocolFBSMIME            = "text/plain"
	protocolPurposeMIME        = "text/markdown"
	protocolTestsMIME          = "application/json"
	actorContextMIME           = "text/markdown"
	bfbsFragment               = "#bfbs"
	bfbsMIME                   = "application/octet-stream"
)

func (s *Server) registerResources() {
	s.mcp.AddResourceTemplate(
		mcpsdk.NewResourceTemplate(protocolFBSURITemplate, "protocol schema",
			mcpsdk.WithTemplateDescription("The active protocol.fbs and, when present, its compiled .bfbs for the addressed content hash."),
			mcpsdk.WithTemplateMIMEType(protocolFBSMIME),
		),
		s.handleProtocolFBS,
	)
	s.mcp.AddResourceTemplate(
		mcpsdk.NewResourceTemplate(protocolPurposeURITemplate, "protocol purpose",
			mcpsdk.WithTemplateDescription("The full purpose.md for the addressed content hash."),
			mcpsdk.WithTemplateMIMEType(protocolPurposeMIME),
		),
		s.handleProtocolPurpose,
	)
	s.mcp.AddResourceTemplate(
		mcpsdk.NewResourceTemplate(protocolTestsURITemplate, "protocol tests",
			mcpsdk.WithTemplateDescription("The normative protocol test specs and the content hash they belong to."),
			mcpsdk.WithTemplateMIMEType(protocolTestsMIME),
		),
		s.handleProtocolTests,
	)
	s.mcp.AddResourceTemplate(
		mcpsdk.NewResourceTemplate(actorContextURITemplate, "actor boundary context",
			mcpsdk.WithTemplateDescription("The filtered boundary view for one actor: purpose, protocol and SDK path."),
			mcpsdk.WithTemplateMIMEType(actorContextMIME),
		),
		s.handleActorContext,
	)
}

func (s *Server) handleProtocolFBS(_ context.Context, req mcpsdk.ReadResourceRequest) ([]mcpsdk.ResourceContents, error) {
	if _, err := s.requireCurrentHash(req.Params.URI, protocolURIPrefix); err != nil {
		return nil, err
	}

	protocolPath := filepath.Join(s.opts.GovernanceDir, "protocol.fbs")
	protocol, err := os.ReadFile(protocolPath)
	if err != nil {
		return nil, fmt.Errorf("mcp: read protocol: %w", err)
	}

	contents := []mcpsdk.ResourceContents{
		mcpsdk.TextResourceContents{URI: req.Params.URI, MIMEType: protocolFBSMIME, Text: string(protocol)},
	}

	if bfbs, err := os.ReadFile(filepath.Join(s.opts.GovernanceDir, "protocol.bfbs")); err == nil {
		contents = append(contents, mcpsdk.BlobResourceContents{
			URI:      req.Params.URI + bfbsFragment,
			MIMEType: bfbsMIME,
			Blob:     base64.StdEncoding.EncodeToString(bfbs),
		})
	}
	return contents, nil
}

func (s *Server) handleProtocolPurpose(_ context.Context, req mcpsdk.ReadResourceRequest) ([]mcpsdk.ResourceContents, error) {
	if _, err := s.requireCurrentHash(req.Params.URI, protocolURIPrefix); err != nil {
		return nil, err
	}

	purpose, err := os.ReadFile(filepath.Join(s.opts.GovernanceDir, "purpose.md"))
	if err != nil {
		return nil, fmt.Errorf("mcp: read purpose: %w", err)
	}
	return []mcpsdk.ResourceContents{
		mcpsdk.TextResourceContents{URI: req.Params.URI, MIMEType: protocolPurposeMIME, Text: string(purpose)},
	}, nil
}

func (s *Server) handleProtocolTests(_ context.Context, req mcpsdk.ReadResourceRequest) ([]mcpsdk.ResourceContents, error) {
	hash, err := s.requireCurrentHash(req.Params.URI, protocolURIPrefix)
	if err != nil {
		return nil, err
	}

	payload, err := s.protocolTestsPayload(hash)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("mcp: encode protocol tests: %w", err)
	}
	return []mcpsdk.ResourceContents{
		mcpsdk.TextResourceContents{URI: req.Params.URI, MIMEType: protocolTestsMIME, Text: string(data)},
	}, nil
}

func (s *Server) handleActorContext(_ context.Context, req mcpsdk.ReadResourceRequest) ([]mcpsdk.ResourceContents, error) {
	actor, ok := strings.CutPrefix(req.Params.URI, actorURIPrefix)
	if !ok {
		return nil, fmt.Errorf("mcp: unsupported resource %s", req.Params.URI)
	}
	actor, _, _ = strings.Cut(actor, "/")
	if strings.TrimSpace(actor) == "" {
		return nil, errors.New("mcp: actor resource requires an actor name")
	}

	out, err := s.boundaryContext(actor)
	if err != nil {
		return nil, err
	}
	return []mcpsdk.ResourceContents{
		mcpsdk.TextResourceContents{URI: req.Params.URI, MIMEType: actorContextMIME, Text: formatBoundaryContext(out)},
	}, nil
}

// protocolTestsPayload is shared by the tests resource and the
// get_protocol_tests tool.
type protocolTestsPayload struct {
	Protocol string                     `json:"protocol"`
	Specs    map[string]json.RawMessage `json:"specs"`
}

func (s *Server) protocolTestsPayload(hash string) (protocolTestsPayload, error) {
	specs, err := s.loadProtocolSpecs()
	if err != nil {
		return protocolTestsPayload{}, err
	}
	return protocolTestsPayload{Protocol: hash, Specs: specs}, nil
}

func (s *Server) loadProtocolSpecs() (map[string]json.RawMessage, error) {
	dir := s.opts.ProtocolTestsDir
	out := make(map[string]json.RawMessage)

	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, fmt.Errorf("mcp: read protocol tests: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("mcp: read protocol test %s: %w", path, err)
		}
		if _, err := protocoltest.ParseSpec(data); err != nil {
			return nil, fmt.Errorf("mcp: protocol test %s: %w", path, err)
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		out[name] = json.RawMessage(data)
	}
	return out, nil
}

// currentProtocolHash returns the content hash of the active bundle.
func (s *Server) currentProtocolHash() (string, error) {
	bundle, err := versioning.NewStore(s.opts.GovernanceDir).LoadBundle(s.opts.GovernanceDir)
	if err != nil {
		return "", fmt.Errorf("mcp: protocol hash: %w", err)
	}
	return bundle.VersionHash(), nil
}

// requireCurrentHash validates that the resource URI addresses the active
// content hash. Historical versions are not served yet.
func (s *Server) requireCurrentHash(uri, prefix string) (string, error) {
	hash, err := s.currentProtocolHash()
	if err != nil {
		return "", err
	}
	rest, ok := strings.CutPrefix(uri, prefix)
	if !ok {
		return "", fmt.Errorf("mcp: unsupported resource %s", uri)
	}
	requested, _, ok := strings.Cut(rest, "/")
	if !ok || requested == "" {
		return "", fmt.Errorf("mcp: resource %s has no content hash", uri)
	}
	if requested != strings.TrimPrefix(hash, "sha256:") && requested != hash {
		return "", fmt.Errorf("mcp: protocol version %s is not active", requested)
	}
	return hash, nil
}
