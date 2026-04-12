package authz

// ScopeDefinition describes a single authorization scope.
type ScopeDefinition struct {
	Description string `json:"description"`
	JITRequired bool   `json:"jit_required"`
}

// ScopeCatalog is the static set of known scopes.
var ScopeCatalog = map[string]ScopeDefinition{
	"memory.read":      {Description: "Read agent memory", JITRequired: false},
	"memory.write":     {Description: "Write agent memory", JITRequired: false},
	"storage.read":     {Description: "Read from storage disks", JITRequired: false},
	"storage.write":    {Description: "Write to storage disks", JITRequired: false},
	"container.build":  {Description: "Build container images", JITRequired: false},
	"deploy.candidate": {Description: "Deploy candidate versions", JITRequired: false},
	"deploy.promote":   {Description: "Promote to production", JITRequired: true},
	"workspace.create": {Description: "Create workspaces", JITRequired: false},
	"workspace.delete": {Description: "Delete workspaces", JITRequired: true},
	"persona.read":     {Description: "Read persona definitions", JITRequired: false},
	"persona.write":    {Description: "Modify persona definitions", JITRequired: false},
	"relay.send":       {Description: "Send messages via relay", JITRequired: false},
	"cost.read":        {Description: "Read cost/usage data", JITRequired: false},
	"plugin.register":  {Description: "Register new plugins", JITRequired: true},
	"plugin.stop":      {Description: "Stop running plugins", JITRequired: true},
}
