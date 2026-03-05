module github.com/antimatter-studios/teamagentica/plugins/telegram

go 1.25.0

require (
	github.com/antimatter-studios/teamagentica/pkg/pluginsdk v0.0.0
	github.com/go-telegram-bot-api/telegram-bot-api/v5 v5.5.1
)

replace github.com/antimatter-studios/teamagentica/pkg/pluginsdk => ../../pkg/pluginsdk
