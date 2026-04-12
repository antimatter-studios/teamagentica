package models

import (
	"encoding/json"
	"testing"
)

func TestManagedContainer_DiskMounts_RoundTrip(t *testing.T) {
	mc := &ManagedContainer{}
	mounts := []DiskMount{
		{SourcePath: "/data/storage-root/workspace/ws-abc", Target: "/workspace"},
		{SourcePath: "/data/storage-root/shared/config", Target: "/config", ReadOnly: true},
	}

	mc.SetDiskMounts(mounts)
	got := mc.GetDiskMounts()

	if len(got) != 2 {
		t.Fatalf("expected 2 mounts, got %d", len(got))
	}
	if got[0].SourcePath != "/data/storage-root/workspace/ws-abc" || got[0].Target != "/workspace" {
		t.Errorf("mount 0 = %+v", got[0])
	}
	if got[1].SourcePath != "/data/storage-root/shared/config" || got[1].ReadOnly != true {
		t.Errorf("mount 1 = %+v", got[1])
	}
}

func TestManagedContainer_DiskMounts_Empty(t *testing.T) {
	mc := &ManagedContainer{}
	mc.SetDiskMounts(nil)
	if got := mc.GetDiskMounts(); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	mc.SetDiskMounts([]DiskMount{})
	if got := mc.GetDiskMounts(); got != nil {
		t.Errorf("expected nil for empty slice, got %v", got)
	}
}

func TestManagedContainer_DiskMounts_InvalidJSON(t *testing.T) {
	mc := &ManagedContainer{DiskMounts: "not-json"}
	if got := mc.GetDiskMounts(); got != nil {
		t.Errorf("expected nil for invalid JSON, got %v", got)
	}
}

func TestManagedContainer_Env_RoundTrip(t *testing.T) {
	mc := &ManagedContainer{}
	env := map[string]string{"FOO": "bar", "BAZ": "qux"}
	mc.SetEnv(env)
	got := mc.GetEnv()
	if got["FOO"] != "bar" || got["BAZ"] != "qux" {
		t.Errorf("env = %v", got)
	}
}

func TestManagedContainer_Env_Nil(t *testing.T) {
	mc := &ManagedContainer{}
	mc.SetEnv(nil)
	// SetEnv(nil) stores "{}" which GetEnv parses as empty map, not nil.
	got := mc.GetEnv()
	if got == nil {
		// Acceptable — json.Unmarshal of "{}" returns empty map.
	} else if len(got) != 0 {
		t.Errorf("expected empty map, got %v", got)
	}
}

func TestManagedContainer_Env_Empty(t *testing.T) {
	mc := &ManagedContainer{}
	if got := mc.GetEnv(); got != nil {
		t.Errorf("expected nil for unset env, got %v", got)
	}
}

func TestManagedContainer_Cmd_RoundTrip(t *testing.T) {
	mc := &ManagedContainer{}
	cmd := []string{"bash", "-c", "echo hello"}
	mc.SetCmd(cmd)
	got := mc.GetCmd()
	if len(got) != 3 || got[0] != "bash" || got[2] != "echo hello" {
		t.Errorf("cmd = %v", got)
	}
}

func TestManagedContainer_Cmd_Empty(t *testing.T) {
	mc := &ManagedContainer{}
	mc.SetCmd(nil)
	if got := mc.GetCmd(); got != nil {
		t.Errorf("expected nil, got %v", got)
	}
	mc.SetCmd([]string{})
	if got := mc.GetCmd(); got != nil {
		t.Errorf("expected nil for empty slice, got %v", got)
	}
}

func TestDiskMount_JSON(t *testing.T) {
	dm := DiskMount{SourcePath: "/data/storage-root/shared/test", Target: "/mnt", ReadOnly: true}
	data, err := json.Marshal(dm)
	if err != nil {
		t.Fatal(err)
	}
	var got DiskMount
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got != dm {
		t.Errorf("roundtrip = %+v, want %+v", got, dm)
	}
}

func TestDiskMount_JSON_OmitReadOnly(t *testing.T) {
	dm := DiskMount{SourcePath: "/data/storage-root/workspace/ws-abc", Target: "/ws"}
	data, err := json.Marshal(dm)
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	if contains(s, "read_only") {
		t.Errorf("read_only should be omitted when false, got %s", s)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
