package manager

import (
	"context"
	"reflect"
	"testing"
)

func TestParseSpecs_Empty(t *testing.T) {
	specs, err := ParseSpecs("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if specs != nil {
		t.Errorf("expected nil specs, got %+v", specs)
	}
}

func TestParseSpecs_Valid(t *testing.T) {
	raw := `[{"name":"ingress","driver":"ngrok","auto_start":true,"role":"ingress","target":"webhook","config":{"authtoken":"x"}}]`
	specs, err := ParseSpecs(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(specs))
	}
	want := Spec{
		Name: "ingress", Driver: "ngrok", AutoStart: true, Role: RoleIngress, Target: TargetWebhook,
		Config: map[string]string{"authtoken": "x"},
	}
	if !reflect.DeepEqual(specs[0], want) {
		t.Errorf("got %+v want %+v", specs[0], want)
	}
}

func TestParseSpecs_Invalid(t *testing.T) {
	if _, err := ParseSpecs("not json"); err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestApplySpecs_SkipsUnnamed(t *testing.T) {
	m := New()
	m.ApplySpecs(context.Background(), []Spec{{Driver: "ngrok"}})
	if got := m.List(); len(got) != 0 {
		t.Errorf("expected 0 tunnels (unnamed skipped), got %d", len(got))
	}
}

func TestStart_UnknownDriver(t *testing.T) {
	m := New()
	m.ApplySpecs(nil, []Spec{{Name: "a", Driver: "nope", Target: "host:1"}})
	_, err := m.Start(context.Background(), "a")
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
}

func TestStart_WebhookTargetNotResolved(t *testing.T) {
	m := New()
	m.ApplySpecs(nil, []Spec{{Name: "a", Driver: "ngrok", Target: TargetWebhook}})
	_, err := m.Start(context.Background(), "a")
	if err == nil {
		t.Fatal("expected error when webhook target unknown")
	}
}

func TestSetWebhookTarget_Idempotent(t *testing.T) {
	m := New()
	if !m.SetWebhookTarget("host:1") {
		t.Error("first set should return true")
	}
	if m.SetWebhookTarget("host:1") {
		t.Error("identical set should return false")
	}
	if !m.SetWebhookTarget("host:2") {
		t.Error("changed value should return true")
	}
}

func TestStop_UnknownTunnel(t *testing.T) {
	m := New()
	if err := m.Stop(context.Background(), "nope"); err == nil {
		t.Error("expected error for unknown tunnel")
	}
}

func TestAddOrReplace_RequiresNameAndDriver(t *testing.T) {
	m := New()
	if _, err := m.AddOrReplace(context.Background(), Spec{Driver: "ngrok"}); err == nil {
		t.Error("expected error with empty name")
	}
	if _, err := m.AddOrReplace(context.Background(), Spec{Name: "a"}); err == nil {
		t.Error("expected error with empty driver")
	}
}

func TestAddOrReplace_InsertsWithoutAutoStart(t *testing.T) {
	m := New()
	view, err := m.AddOrReplace(context.Background(), Spec{Name: "t1", Driver: "ngrok", Target: "host:1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if view.Spec.Name != "t1" {
		t.Errorf("view.Spec.Name=%q", view.Spec.Name)
	}
	if list := m.List(); len(list) != 1 {
		t.Errorf("expected 1 tunnel, got %d", len(list))
	}
}

func TestAddOrReplace_ReplacesSameName(t *testing.T) {
	m := New()
	if _, err := m.AddOrReplace(context.Background(), Spec{Name: "t1", Driver: "ngrok", Target: "old:1"}); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if _, err := m.AddOrReplace(context.Background(), Spec{Name: "t1", Driver: "ngrok", Target: "new:2"}); err != nil {
		t.Fatalf("replace: %v", err)
	}
	v, ok := m.Get("t1")
	if !ok || v.Spec.Target != "new:2" {
		t.Errorf("expected replaced target, got %+v ok=%v", v, ok)
	}
	if list := m.List(); len(list) != 1 {
		t.Errorf("expected still 1 tunnel after replace, got %d", len(list))
	}
}

func TestRemove_Idempotent(t *testing.T) {
	m := New()
	if err := m.Remove(context.Background(), "nope"); err != nil {
		t.Errorf("Remove unknown should be nil, got %v", err)
	}
	if _, err := m.AddOrReplace(context.Background(), Spec{Name: "t1", Driver: "ngrok", Target: "h:1"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Remove(context.Background(), "t1"); err != nil {
		t.Errorf("Remove: %v", err)
	}
	if list := m.List(); len(list) != 0 {
		t.Errorf("expected 0 tunnels after remove, got %d", len(list))
	}
}

func TestSpecsEqual(t *testing.T) {
	a := Spec{Name: "x", Driver: "ngrok", Config: map[string]string{"k": "v"}}
	b := Spec{Name: "x", Driver: "ngrok", Config: map[string]string{"k": "v"}}
	if !specsEqual(a, b) {
		t.Error("expected equal")
	}
	b.Config["k"] = "w"
	if specsEqual(a, b) {
		t.Error("expected unequal after config change")
	}
}
