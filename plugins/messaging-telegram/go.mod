module github.com/antimatter-studios/teamagentica/plugins/messaging-telegram

go 1.25.0

require (
	github.com/antimatter-studios/teamagentica/pkg/pluginsdk v1.1.0
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
)

require gopkg.in/yaml.v3 v3.0.1 // indirect

replace github.com/antimatter-studios/teamagentica/pkg/pluginsdk => ../../pkg/pluginsdk
