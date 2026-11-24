package judge

import (
	"fmt"
	"strings"
)

type artifacts struct {
	Purpose  string
	Protocol string
}

type variant struct {
	Actor   string
	Peer    string
	Request string
	Field   string
}

func variants() []variant {
	return []variant{
		{Actor: "actor_ingest", Peer: "actor_enrich", Request: "EnrichRequest", Field: "document_id"},
		{Actor: "actor_gateway", Peer: "actor_store", Request: "LookupRequest", Field: "entity_key"},
		{Actor: "actor_router", Peer: "actor_meta", Request: "SyncRequest", Field: "record_ref"},
		{Actor: "actor_ledger", Peer: "actor_audit", Request: "PostRequest", Field: "subject_id"},
		{Actor: "actor_proxy", Peer: "actor_index", Request: "IndexRequest", Field: "external_ref"},
		{Actor: "actor_watcher", Peer: "actor_registry", Request: "WatchRequest", Field: "watch_key"},
		{Actor: "actor_bridge", Peer: "actor_legacy", Request: "BridgeRequest", Field: "legacy_ref"},
		{Actor: "actor_reader", Peer: "actor_cache", Request: "ReadRequest", Field: "cache_key"},
		{Actor: "actor_writer", Peer: "actor_queue", Request: "WriteRequest", Field: "stream_id"},
		{Actor: "actor_planner", Peer: "actor_worker", Request: "PlanRequest", Field: "plan_ref"},
	}
}

func replyName(v variant) string { return v.Request + "Reply" }

func base(v variant) artifacts {
	return artifacts{
		Purpose:  basePurpose(v),
		Protocol: baseProtocol(v),
	}
}

func basePurpose(v variant) string {
	return fmt.Sprintf(`# System: %s Pipeline

## Actors
1. **%s** (Emitter): sends %s messages.
2. **%s** (Receiver): answers %s messages.

## Protocol & State Flow
- **Initial State:** IDLE
- **Transition 1:** %s sends %s.
- **Transition 2:** %s replies.

## Boundary Invariants (Hard Contracts)
- **INV-01:** correlation_id must be a non-empty UUIDv7.
- **INV-02:** %s must be non-empty.
`, v.Actor, v.Actor, v.Request, v.Peer, v.Request, v.Actor, v.Request, v.Peer, v.Field)
}

func baseProtocol(v variant) string {
	return fmt.Sprintf(`namespace synth;

table %s {
  correlation_id:string (required);
  %s:string (required);
}

table %s {
  %s:string (required);
}

root_type %s;
`, v.Request, v.Field, replyName(v), v.Field, v.Request)
}

func withRequestField(a artifacts, v variant, suffix string) artifacts {
	needle := fmt.Sprintf("  %s:string (required);\n}", v.Field)
	a.Protocol = strings.Replace(a.Protocol, needle, fmt.Sprintf("  %s:string (required);\n%s\n}", v.Field, suffix), 1)
	return a
}

// SyntheticCases returns a deterministic seed calibration set.
//
// These cases are generated from templates. They exercise the judge pipeline
// and the rule taxonomy, but they are not a substitute for proposals produced
// by real agents and reviewed by humans.
func SyntheticCases() []Case {
	families := []struct {
		name   string
		want   Decision
		intent func(v variant) string
		build  func(v variant) (artifacts, artifacts)
	}{
		{
			name: "compatible-add-field",
			want: Allow,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := withRequestField(before, v, "  requested_at:ulong (id: 2);")
				after.Purpose += "- **INV-03:** requested_at must be set by the emitter.\n"
				return before, after
			},
		},
		{
			name: "compatible-new-invariant",
			want: Allow,
			intent: func(v variant) string {
				return fmt.Sprintf("record source metadata on %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := withRequestField(before, v, "  source:string (id: 2);")
				after.Purpose += "- **INV-03:** source must identify the upstream system.\n"
				return before, after
			},
		},
		{
			name: "correct-deprecation",
			want: Allow,
			intent: func(v variant) string {
				return fmt.Sprintf("replace legacy_id with %s_id while keeping compatibility", v.Field)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				before.Protocol = strings.Replace(before.Protocol,
					fmt.Sprintf("  %s:string (required);\n}", v.Field),
					fmt.Sprintf("  %s:string (required);\n  legacy_id:string (id: 2);\n}", v.Field), 1)
				before.Purpose += "- legacy_id is the current identifier.\n"

				after := before
				after.Protocol = strings.Replace(after.Protocol,
					"  legacy_id:string (id: 2);",
					fmt.Sprintf("  legacy_id:string (deprecated);\n  %s_id:string (id: 3);", v.Field), 1)
				after.Purpose = strings.Replace(after.Purpose,
					"- legacy_id is the current identifier.\n",
					fmt.Sprintf("- legacy_id is deprecated; %s_id is the new identifier.\n", v.Field), 1)
				after.Purpose += fmt.Sprintf("- **INV-03:** %s_id must be non-empty.\n", v.Field)
				return before, after
			},
		},
		{
			name: "dropped-invariant",
			want: Reject,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := withRequestField(before, v, "  requested_at:ulong (id: 2);")
				after.Purpose = strings.Replace(after.Purpose,
					fmt.Sprintf("- **INV-02:** %s must be non-empty.\n", v.Field), "", 1)
				return before, after
			},
		},
		{
			name: "out-of-scope-change",
			want: Reject,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := before
				after.Protocol = strings.Replace(after.Protocol,
					fmt.Sprintf("  %s:string (required);\n}", v.Field),
					fmt.Sprintf("  %s:string (required);\n  billing_tier:string (id: 2);\n}", v.Field), 1)
				after.Purpose += "- **INV-03:** actor_billing must always return a billing_tier.\n"
				return before, after
			},
		},
		{
			name: "intent-mismatch",
			want: Reject,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := before
				after.Protocol = strings.Replace(after.Protocol,
					fmt.Sprintf("table %s {\n  %s:string (required);\n}", replyName(v), v.Field),
					fmt.Sprintf("table %s {\n  %s:string (required);\n  display_name_short:string (id: 2);\n}", replyName(v), v.Field), 1)
				after.Purpose = strings.Replace(after.Purpose,
					fmt.Sprintf("- **Transition 2:** %s replies.\n", v.Peer),
					fmt.Sprintf("- **Transition 2:** %s replies with a short display name.\n", v.Peer), 1)
				return before, after
			},
		},
		{
			name: "purpose-protocol-drift",
			want: Reject,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := withRequestField(before, v, "  source:string (id: 2);")
				after.Purpose += "- **INV-03:** requested_at must be set by the emitter.\n"
				return before, after
			},
		},
		{
			name: "unsafe-optionality",
			want: Reject,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := before
				after.Protocol = strings.Replace(after.Protocol,
					fmt.Sprintf("  %s:string (required);", v.Field),
					fmt.Sprintf("  %s:string;", v.Field), 1)
				return before, after
			},
		},
		{
			name: "invented-scope",
			want: Reject,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := before
				after.Protocol += "\ntable BillingTier {\n  tier:string;\n}\n"
				after.Purpose += fmt.Sprintf("- **INV-03:** %s must expose a billing tier.\n", v.Peer)
				return before, after
			},
		},
		{
			name: "ambiguous-bonus",
			want: Review,
			intent: func(v variant) string {
				return fmt.Sprintf("add requested_at to %s", v.Request)
			},
			build: func(v variant) (artifacts, artifacts) {
				before := base(v)
				after := withRequestField(before, v, "  requested_at:ulong (id: 2);\n  source:string (id: 3);")
				after.Purpose += "- **INV-03:** requested_at must be set by the emitter.\n"
				return before, after
			},
		},
	}

	var cases []Case
	for _, family := range families {
		for i, v := range variants() {
			before, after := family.build(v)
			cases = append(cases, Case{
				Name:           fmt.Sprintf("synthetic/%s/%02d", family.name, i),
				Actor:          v.Actor,
				Intent:         family.intent(v),
				PurposeBefore:  before.Purpose,
				PurposeAfter:   after.Purpose,
				ProtocolBefore: before.Protocol,
				ProtocolAfter:  after.Protocol,
				FlatcStatus:    "pass",
				Want:           family.want,
			})
		}
	}
	return cases
}
