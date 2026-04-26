package ngrok

import (
	"context"
	"testing"

	"github.com/antimatter-studios/teamagentica/plugins/network-traffic-manager/internal/drivers"
)

func TestNew_RequiresAuthToken(t *testing.T) {
	if _, err := New("test", "host:1", map[string]string{}); err == nil {
		t.Fatal("expected error when authtoken missing")
	}
}

func TestNew_RequiresTarget(t *testing.T) {
	if _, err := New("test", "", map[string]string{"authtoken": "x"}); err == nil {
		t.Fatal("expected error when target missing")
	}
}

func TestStatus_InitiallyStopped(t *testing.T) {
	d, err := New("test", "host:1", map[string]string{"authtoken": "x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := d.Status().State; got != drivers.StateStopped {
		t.Errorf("initial state = %q, want %q", got, drivers.StateStopped)
	}
}

func TestStop_Idempotent(t *testing.T) {
	d, err := New("test", "host:1", map[string]string{"authtoken": "x"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Stop(context.Background()); err != nil {
		t.Errorf("Stop on never-started: %v", err)
	}
}

func TestRegistered(t *testing.T) {
	ids := drivers.Available()
	found := false
	for _, id := range ids {
		if id == ID {
			found = true
		}
	}
	if !found {
		t.Errorf("ngrok not registered; available: %v", ids)
	}
}
