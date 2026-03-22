module github.com/antimatter-studios/teamagentica/plugins/messaging-discord

go 1.25.0

require (
	github.com/antimatter-studios/teamagentica/pkg/pluginsdk v1.1.0
	github.com/bwmarrin/discordgo v0.29.0
	github.com/redis/go-redis/v9 v9.18.0
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/gorilla/websocket v1.4.2 // indirect
	github.com/jinzhu/inflection v1.0.0 // indirect
	github.com/jinzhu/now v1.1.5 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/mattn/go-sqlite3 v1.14.22 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.34.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	gorm.io/driver/sqlite v1.6.0 // indirect
	gorm.io/gorm v1.31.1 // indirect
)

replace github.com/antimatter-studios/teamagentica/pkg/pluginsdk => ../../pkg/pluginsdk
