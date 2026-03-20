module github.com/antimatter-studios/teamagentica/plugins/network-webhook-ingress

go 1.25.0

require github.com/antimatter-studios/teamagentica/pkg/pluginsdk v1.1.0

require (
	github.com/kr/text v0.2.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/antimatter-studios/teamagentica/pkg/pluginsdk => ../../pkg/pluginsdk
