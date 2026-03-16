package client

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, New(srv.URL, "test-token")
}

func TestHealth_OK(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok", "version": "1.0.0", "app": "teamagentica",
		})
	})

	h, err := c.Health()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if h.Status != "ok" {
		t.Errorf("status = %q, want %q", h.Status, "ok")
	}
	if h.Version != "1.0.0" {
		t.Errorf("version = %q, want %q", h.Version, "1.0.0")
	}
}

func TestHealth_502(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := c.Health()
	if err == nil {
		t.Fatal("expected error for 502")
	}
}

func TestHealth_EmptyFields(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"status": "", "version": ""})
	})
	_, err := c.Health()
	if err == nil {
		t.Fatal("expected error for empty fields")
	}
}

func TestListPlugins(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing auth header")
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"plugins": []map[string]interface{}{
				{"id": "p1", "name": "Plugin One", "version": "1.0", "status": "running", "enabled": true},
				{"id": "p2", "name": "Plugin Two", "version": "2.0", "status": "stopped", "enabled": false},
			},
		})
	})

	plugins, err := c.ListPlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(plugins))
	}
	if plugins[0].ID != "p1" || plugins[0].Name != "Plugin One" {
		t.Errorf("plugin 0 = %+v", plugins[0])
	}
	if plugins[1].Enabled != false {
		t.Errorf("plugin 1 enabled = %v, want false", plugins[1].Enabled)
	}
}

func TestEnablePlugin(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/plugins/p1/enable" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if _, err := c.EnablePlugin("p1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDisablePlugin(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != "/api/plugins/p1/disable" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := c.DisablePlugin("p1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDisablePlugin_Forbidden(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "system plugins cannot be disabled"})
	})
	err := c.DisablePlugin("builtin-provider")
	if err == nil {
		t.Fatal("expected error for 403")
	}
}

func TestRestartPlugin(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins/p1/restart" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := c.RestartPlugin("p1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestUninstallPlugin(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" {
			t.Errorf("method = %s, want DELETE", r.Method)
		}
		if r.URL.Path != "/api/plugins/p1" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := c.UninstallPlugin("p1"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPluginConfig(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins/p1/config" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"key":"value"}`))
	})
	raw, err := c.PluginConfig("p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(raw) != `{"key":"value"}` {
		t.Errorf("config = %s", string(raw))
	}
}

func TestPluginSchema(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/plugins/p1/schema" {
			t.Errorf("path = %s", r.URL.Path)
		}
		w.Write([]byte(`{"type":"object"}`))
	})
	raw, err := c.PluginSchema("p1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(raw) != `{"type":"object"}` {
		t.Errorf("schema = %s", string(raw))
	}
}

func TestListProviders(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/marketplace/providers" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"providers": []map[string]interface{}{
				{"id": 1, "name": "builtin-provider", "url": "http://bp:8083", "enabled": true, "system": true},
				{"id": 2, "name": "custom", "url": "http://custom:9000", "enabled": true, "system": false},
			},
		})
	})

	provs, err := c.ListProviders()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(provs) != 2 {
		t.Fatalf("got %d providers, want 2", len(provs))
	}
	if !provs[0].System {
		t.Error("provider 0 should be system")
	}
	if provs[1].System {
		t.Error("provider 1 should not be system")
	}
}

func TestAddProvider(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/marketplace/providers" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["name"] != "test" || body["url"] != "http://test:9000" {
			t.Errorf("body = %v", body)
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"provider": map[string]interface{}{
				"id": 3, "name": "test", "url": "http://test:9000", "enabled": true, "system": false,
			},
		})
	})

	prov, err := c.AddProvider("test", "http://test:9000")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prov.Name != "test" || prov.ID != 3 {
		t.Errorf("provider = %+v", prov)
	}
}

func TestDeleteProvider(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "DELETE" || r.URL.Path != "/api/marketplace/providers/2" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	})
	if err := c.DeleteProvider("2"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteProvider_SystemForbidden(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"error": "system providers cannot be deleted"})
	})
	err := c.DeleteProvider("1")
	if err == nil {
		t.Fatal("expected error for system provider deletion")
	}
}

func TestProviderPlugins(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/marketplace/providers/builtin-provider/plugins" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"plugins": []map[string]interface{}{
				{"plugin_id": "p1", "name": "Plugin 1", "version": "1.0"},
			},
		})
	})

	plugins, err := c.ProviderPlugins("builtin-provider")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plugins) != 1 || plugins[0].PluginID != "p1" {
		t.Errorf("plugins = %+v", plugins)
	}
}

func TestProviderPlugins_NotFound(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"error": "provider not found"})
	})
	_, err := c.ProviderPlugins("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing provider")
	}
}

func TestBrowsePlugins_WithPlugins(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/marketplace/plugins" {
			t.Errorf("path = %s", r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"plugins": []map[string]interface{}{
				{"plugin_id": "p1", "name": "Plugin 1", "version": "1.0", "provider": "builtin"},
				{"plugin_id": "p2", "name": "Plugin 2", "version": "2.0", "provider": "builtin"},
			},
		})
	})

	result, err := c.BrowsePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Plugins) != 2 {
		t.Fatalf("got %d plugins, want 2", len(result.Plugins))
	}
	if len(result.Errors) != 0 {
		t.Errorf("got %d errors, want 0", len(result.Errors))
	}
}

func TestBrowsePlugins_WithErrors(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"plugins": []map[string]interface{}{},
			"errors":  []string{"builtin-provider: connection refused"},
		})
	})

	result, err := c.BrowsePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Plugins) != 0 {
		t.Errorf("got %d plugins, want 0", len(result.Plugins))
	}
	if len(result.Errors) != 1 {
		t.Fatalf("got %d errors, want 1", len(result.Errors))
	}
	if result.Errors[0] != "builtin-provider: connection refused" {
		t.Errorf("error = %q", result.Errors[0])
	}
}

func TestBrowsePlugins_NoErrors(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"plugins": []map[string]interface{}{},
		})
	})

	result, err := c.BrowsePlugins()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Errors) != 0 {
		t.Errorf("got %d errors, want 0", len(result.Errors))
	}
}

func TestInstallPlugin(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/marketplace/install" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["plugin_id"] != "my-plugin" {
			t.Errorf("plugin_id = %q", body["plugin_id"])
		}
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
			"message": "plugin installed",
			"plugin": map[string]interface{}{
				"id": "my-plugin", "name": "My Plugin", "version": "1.0",
			},
		})
	})

	result, err := c.InstallPlugin("my-plugin")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Plugin.Name != "My Plugin" {
		t.Errorf("plugin name = %q", result.Plugin.Name)
	}
}

func TestInstallPlugin_AlreadyExists(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		json.NewEncoder(w).Encode(map[string]string{"error": "plugin already installed"})
	})
	_, err := c.InstallPlugin("existing")
	if err == nil {
		t.Fatal("expected error for conflict")
	}
}

func TestNew_TrimsTrailingSlash(t *testing.T) {
	c := New("http://localhost:8080/", "tok")
	if c.BaseURL != "http://localhost:8080" {
		t.Errorf("BaseURL = %q", c.BaseURL)
	}
}

func TestDo_ErrorResponse(t *testing.T) {
	_, c := testServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	})
	_, err := c.ListPlugins()
	if err == nil {
		t.Fatal("expected error for 500")
	}
}

func TestDo_NoToken(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		json.NewEncoder(w).Encode(map[string]interface{}{"plugins": []interface{}{}})
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL, "")
	c.ListPlugins()
	if gotAuth != "" {
		t.Errorf("expected no auth header, got %q", gotAuth)
	}
}
