module github.com/antimatter-studios/teamagentica/plugins/messaging-discord

go 1.25.0

require (
	github.com/antimatter-studios/teamagentica/pkg/pluginsdk v1.1.0
	github.com/bwmarrin/discordgo v0.29.0
)

require (
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/antimatter-studios/teamagentica/pkg/pluginsdk => ../../pkg/pluginsdk
