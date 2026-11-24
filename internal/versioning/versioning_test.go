package versioning

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBundleHashDeterministic(t *testing.T) {
	t.Parallel()

	bundle := Bundle{Purpose: "purpose", Protocol: "protocol", StateMachine: "{}"}
	version := bundle.VersionHash()
	if !strings.HasPrefix(version, "sha256:") || len(version) != len("sha256:")+64 {
		t.Fatalf("unexpected version hash %q", version)
	}
	if again := bundle.VersionHash(); again != version {
		t.Fatalf("hash is not deterministic: %q != %q", again, version)
	}
	if wire := bundle.WireHash(); wire != bundle.WireHash() {
		t.Fatal("wire hash is not deterministic")
	}

	changedPurpose := bundle
	changedPurpose.Purpose += "\n"
	if changedPurpose.VersionHash() == version {
		t.Fatal("purpose change did not change the version hash")
	}
	if changedPurpose.WireHash() != bundle.WireHash() {
		t.Fatal("purpose change changed the wire hash")
	}

	changedProtocol := bundle
	changedProtocol.Protocol += "\n"
	if changedProtocol.WireHash() == bundle.WireHash() {
		t.Fatal("protocol change did not change the wire hash")
	}
}

func TestSnapshotAndAliases(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGovernance(t, dir, "purpose", "protocol", "{}")
	store := NewStore(dir)
	bundle, err := store.LoadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}

	version, err := store.Snapshot(bundle, Meta{Actor: "actor_ingest", Intent: "initial"}, "v0")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := store.Resolve("v0"); err != nil || got != version.Version {
		t.Fatalf("Resolve(v0) = %q, %v; want %q", got, err, version.Version)
	}

	loaded, loadedBundle, err := store.Load("v0")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != version.Version || loadedBundle.Purpose != bundle.Purpose {
		t.Fatalf("Load returned %+v / %+v", loaded, loadedBundle)
	}
	if version.Actor != "actor_ingest" || version.Intent != "initial" {
		t.Fatalf("metadata not preserved: %+v", version)
	}

	versions, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 {
		t.Fatalf("versions = %d, want 1", len(versions))
	}

	// Identical content is the same version, even with a new alias.
	again, err := store.Snapshot(bundle, Meta{Actor: "actor_enrich", Intent: "same"}, "v1")
	if err != nil {
		t.Fatal(err)
	}
	if again.Version != version.Version {
		t.Fatalf("identical bundle produced a different version: %s != %s", again.Version, version.Version)
	}
	aliases, err := store.Aliases()
	if err != nil {
		t.Fatal(err)
	}
	if aliases["v0"] != version.Version || aliases["v1"] != version.Version {
		t.Fatalf("aliases = %+v", aliases)
	}

	// A different bundle is a different version.
	other := bundle
	other.Purpose = "purpose v2"
	second, err := store.Snapshot(other, Meta{Actor: "actor_ingest", Intent: "change"}, "v2")
	if err != nil {
		t.Fatal(err)
	}
	if second.Version == version.Version {
		t.Fatal("different bundle produced the same version")
	}

	if _, err := store.Snapshot(other, Meta{}, "v0"); err == nil {
		t.Fatal("snapshot with an already-used alias should fail")
	}
	if got, err := store.Resolve("v0"); err != nil || got != version.Version {
		t.Fatalf("v0 changed after rejected snapshot: %q, %v", got, err)
	}

	if err := store.SetAlias("v0", second.Version, false); err == nil {
		t.Fatal("moving v0 without force should fail")
	}
	if err := store.SetAlias("v0", second.Version, true); err != nil {
		t.Fatalf("moving v0 with force: %v", err)
	}
	if got, err := store.Resolve("v0"); err != nil || got != second.Version {
		t.Fatalf("Resolve(v0) after force = %q, %v; want %q", got, err, second.Version)
	}
}

func TestResolvePrefix(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGovernance(t, dir, "purpose", "protocol", "{}")
	store := NewStore(dir)
	bundle, err := store.LoadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.Snapshot(bundle, Meta{}, "v0")
	if err != nil {
		t.Fatal(err)
	}

	prefix := version.Version[:len("sha256:")+12]
	got, err := store.Resolve(prefix)
	if err != nil {
		t.Fatal(err)
	}
	if got != version.Version {
		t.Fatalf("Resolve(%q) = %q, want %q", prefix, got, version.Version)
	}
	if _, err := store.Resolve("deadbeefdeadbeef"); err == nil {
		t.Fatal("unknown prefix should fail")
	}
}

func TestLoadBundleDefaultsMissingStateMachine(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGovernance(t, dir, "purpose", "protocol", "")
	bundle, err := NewStore(dir).LoadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.StateMachine != "{}" {
		t.Fatalf("state machine = %q, want {}", bundle.StateMachine)
	}
}

func TestPinActor(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeGovernance(t, dir, "purpose", "protocol", "{}")
	store := NewStore(dir)
	bundle, err := store.LoadBundle(dir)
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.Snapshot(bundle, Meta{}, "v0")
	if err != nil {
		t.Fatal(err)
	}

	pin, err := store.PinActor("actor_ingest", "producer", "v0", "")
	if err != nil {
		t.Fatal(err)
	}
	if pin.Version != version.Version || pin.Alias != "v0" || pin.Status != "active" {
		t.Fatalf("pin = %+v", pin)
	}
	again, err := store.PinActor("actor_ingest", "producer", "v0", "")
	if err != nil {
		t.Fatal(err)
	}
	if !again.AdoptedAt.Equal(pin.AdoptedAt) {
		t.Fatalf("re-pinning changed AdoptedAt: %s != %s", again.AdoptedAt, pin.AdoptedAt)
	}
	if _, err := store.PinActor("actor_enrich", "consumer", "v0", "active"); err != nil {
		t.Fatal(err)
	}

	pins, err := store.Actors()
	if err != nil {
		t.Fatal(err)
	}
	if len(pins) != 2 {
		t.Fatalf("pins = %d, want 2", len(pins))
	}
	if pins[0].Actor != "actor_enrich" || pins[1].Actor != "actor_ingest" {
		t.Fatalf("pins not sorted: %+v", pins)
	}
}

func TestWriteCompat(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	store := NewStore(dir)
	base := "sha256:" + strings.Repeat("a", 64)
	candidate := "sha256:" + strings.Repeat("b", 64)
	compat := Compatibility{
		Base:      base,
		Candidate: candidate,
		Wire:      WireCompatible,
		Class:     ClassAdditive,
		Evidence:  Evidence{FlatcConform: "pass", StructuralDiff: "pass"},
		CreatedAt: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC),
	}
	path, err := store.WriteCompat(compat)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Compatibility
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Class != ClassAdditive || decoded.Base != base {
		t.Fatalf("decoded = %+v", decoded)
	}

	// Re-analyzing the same pair preserves the original timestamp.
	compat.CreatedAt = compat.CreatedAt.Add(time.Hour)
	if _, err := store.WriteCompat(compat); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.CreatedAt.Equal(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("CreatedAt = %s, want original", decoded.CreatedAt)
	}

	// A substantive change rewrites the manifest.
	compat.Class = ClassBreaking
	if _, err := store.WriteCompat(compat); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Class != ClassBreaking {
		t.Fatalf("class = %q, want breaking", decoded.Class)
	}

	if _, err := store.WriteCompat(Compatibility{}); err == nil {
		t.Fatal("WriteCompat with empty hashes should fail")
	}
}

func writeGovernance(t *testing.T, dir, purpose, protocol, state string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"purpose.md":         purpose,
		"protocol.fbs":       protocol,
		"state_machine.json": state,
	} {
		if content == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
