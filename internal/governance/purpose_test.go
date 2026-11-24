package governance

import (
	"errors"
	"strings"
	"testing"
)

const samplePurpose = `# System: Entity Enrichment Pipeline

Intro paragraph.

## Actors
1. **actor_ingest** (Emitter / Client): Ingests raw batches and dispatches requests.
2. **actor_enrich** (Receiver / Server): Queries storage and returns metadata.
3. **actor_external_crm** (External Adapter): Wraps a legacy HTTP API.

## Protocol & State Flow
- **Initial State:** ` + "`IDLE`" + `
- **Transition 1:** ` + "`actor_ingest`" + ` sends ` + "`EnrichRequest`" + `.
- **Transition 2:** ` + "`actor_enrich`" + ` replies with ` + "`EnrichResponse`" + `.

## Boundary Invariants (Hard Contracts)
- **INV-01:** ` + "`correlation_id`" + ` must be a UUIDv7.
- **INV-02:** ` + "`actor_enrich`" + ` must reject invalid documents.
`

func TestViewForFiltersActorsSection(t *testing.T) {
	t.Parallel()

	view, err := ParsePurpose([]byte(samplePurpose)).ViewFor("actor_ingest")
	if err != nil {
		t.Fatalf("ViewFor: %v", err)
	}

	for _, want := range []string{
		"# System: Entity Enrichment Pipeline",
		"Intro paragraph.",
		"**actor_ingest**",
		"## Protocol & State Flow",
		"## Boundary Invariants",
		"INV-01",
		"INV-02",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view does not contain %q:\n%s", want, view)
		}
	}

	for _, unwanted := range []string{
		"**actor_enrich** (Receiver / Server)",
		"**actor_external_crm**",
	} {
		if strings.Contains(view, unwanted) {
			t.Errorf("view contains %q:\n%s", unwanted, view)
		}
	}
}

func TestViewForSecondActor(t *testing.T) {
	t.Parallel()

	view, err := ParsePurpose([]byte(samplePurpose)).ViewFor("actor_external_crm")
	if err != nil {
		t.Fatalf("ViewFor: %v", err)
	}
	if !strings.Contains(view, "**actor_external_crm**") {
		t.Fatalf("view is missing the requested actor:\n%s", view)
	}
	if strings.Contains(view, "**actor_ingest**") {
		t.Fatalf("view leaked another actor:\n%s", view)
	}
}

func TestViewForUnknownActor(t *testing.T) {
	t.Parallel()

	_, err := ParsePurpose([]byte(samplePurpose)).ViewFor("actor_ghost")
	if !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("err = %v, want ErrActorNotFound", err)
	}
}

func TestViewForRequiresActor(t *testing.T) {
	t.Parallel()

	if _, err := ParsePurpose([]byte(samplePurpose)).ViewFor("  "); err == nil {
		t.Fatal("ViewFor with empty actor returned nil error")
	}
}

func TestActorNamesRespectWordBoundaries(t *testing.T) {
	t.Parallel()

	doc := "# System\n\n## Actors\n1. **actor_ingest** does work.\n"
	p := ParsePurpose([]byte(doc))

	if _, err := p.ViewFor("actor"); !errors.Is(err, ErrActorNotFound) {
		t.Fatalf("err = %v, want ErrActorNotFound for partial name", err)
	}
	if _, err := p.ViewFor("actor_ingest"); err != nil {
		t.Fatalf("ViewFor(actor_ingest): %v", err)
	}
}

func TestParsePurposeHandlesCRLF(t *testing.T) {
	t.Parallel()

	doc := "# System\r\n\r\n## Actors\r\n1. **actor_a** does work.\r\n"
	p := ParsePurpose([]byte(doc))

	view, err := p.ViewFor("actor_a")
	if err != nil {
		t.Fatalf("ViewFor: %v", err)
	}
	if strings.Contains(view, "\r") {
		t.Fatalf("view still contains carriage returns: %q", view)
	}
	if !strings.Contains(view, "## Actors") {
		t.Fatalf("view is missing the Actors section: %q", view)
	}
}

func TestViewForNoSections(t *testing.T) {
	t.Parallel()

	p := ParsePurpose([]byte("Just actor_a mentioned in prose."))
	view, err := p.ViewFor("actor_a")
	if err != nil {
		t.Fatalf("ViewFor: %v", err)
	}
	if !strings.Contains(view, "actor_a") {
		t.Fatalf("view = %q", view)
	}
}
