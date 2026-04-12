package pluginsdk

import "os"

// Identity holds the agent principal identity injected by the kernel at container start.
type Identity struct {
	Principal  string
	ProjectID  string
	InstanceID string
	AgentType  string
	SessionID  string
	PluginID   string
}

// GetIdentity reads the agent identity from environment variables.
func GetIdentity() Identity {
	return Identity{
		Principal:  os.Getenv("AGENT_PRINCIPAL"),
		ProjectID:  os.Getenv("AGENT_PROJECT_ID"),
		InstanceID: os.Getenv("AGENT_INSTANCE_ID"),
		AgentType:  os.Getenv("AGENT_TYPE"),
		SessionID:  os.Getenv("AGENT_SESSION_ID"),
		PluginID:   os.Getenv("AGENT_PLUGIN_ID"),
	}
}
