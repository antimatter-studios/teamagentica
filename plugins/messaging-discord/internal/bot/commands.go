package bot

import (
	"bytes"
	"context"
	"encoding/json"
	"log"

	"github.com/bwmarrin/discordgo"

	"github.com/antimatter-studios/teamagentica/pkg/pluginsdk"
)

// commandOwner maps a routed key to the plugin and endpoint that handles it.
// Key is "command" for leaf commands, "command/subcommand" for subcommands.
type commandOwner struct {
	pluginID string
	endpoint string
}

// discoverAndRegisterCommands queries all discord:command plugins, registers
// their slash commands with Discord, and returns the owner map.
func (b *Bot) discoverAndRegisterCommands(appID string) map[string]commandOwner {
	owners := make(map[string]commandOwner)

	if b.sdk == nil || b.guildID == "" {
		return owners
	}

	plugins, err := b.sdk.SearchPlugins("discord:command")
	if err != nil {
		log.Printf("discoverCommands: SearchPlugins error: %v", err)
		return owners
	}

	for _, p := range plugins {
		schema, err := b.sdk.GetPluginSchema(p.ID)
		if err != nil {
			log.Printf("discoverCommands: GetPluginSchema(%s) error: %v", p.ID, err)
			continue
		}

		raw, ok := schema["discord_commands"]
		if !ok {
			continue
		}
		data, err := json.Marshal(raw)
		if err != nil {
			continue
		}
		var cmds []pluginsdk.DiscordCommand
		if err := json.Unmarshal(data, &cmds); err != nil {
			continue
		}

		for _, cmd := range cmds {
			appCmd := buildDiscordAppCommand(cmd)
			if _, err := b.session.ApplicationCommandCreate(appID, b.guildID, appCmd); err != nil {
				log.Printf("discoverCommands: register /%s (from %s): %v", cmd.Name, p.ID, err)
				continue
			}

			if len(cmd.Subcommands) > 0 {
				for _, sub := range cmd.Subcommands {
					key := cmd.Name + "/" + sub.Name
					owners[key] = commandOwner{pluginID: p.ID, endpoint: sub.Endpoint}
				}
				log.Printf("Registered slash command: /%s (%d subcommands, from %s)", cmd.Name, len(cmd.Subcommands), p.ID)
			} else {
				owners[cmd.Name] = commandOwner{pluginID: p.ID, endpoint: cmd.Endpoint}
				log.Printf("Registered slash command: /%s (from %s)", cmd.Name, p.ID)
			}
		}
	}

	return owners
}

// buildDiscordAppCommand converts a DiscordCommand schema into a discordgo ApplicationCommand.
func buildDiscordAppCommand(cmd pluginsdk.DiscordCommand) *discordgo.ApplicationCommand {
	appCmd := &discordgo.ApplicationCommand{
		Name:        cmd.Name,
		Description: cmd.Description,
	}

	if len(cmd.Subcommands) > 0 {
		for _, sub := range cmd.Subcommands {
			opt := &discordgo.ApplicationCommandOption{
				Type:        discordgo.ApplicationCommandOptionSubCommand,
				Name:        sub.Name,
				Description: sub.Description,
			}
			for _, o := range sub.Options {
				opt.Options = append(opt.Options, &discordgo.ApplicationCommandOption{
					Type:        discordOptionType(o.Type),
					Name:        o.Name,
					Description: o.Description,
					Required:    o.Required,
				})
			}
			appCmd.Options = append(appCmd.Options, opt)
		}
	} else {
		for _, o := range cmd.Options {
			appCmd.Options = append(appCmd.Options, &discordgo.ApplicationCommandOption{
				Type:        discordOptionType(o.Type),
				Name:        o.Name,
				Description: o.Description,
				Required:    o.Required,
			})
		}
	}

	return appCmd
}

// handleSlashCommand routes an application command interaction to the owning plugin.
func (b *Bot) handleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate, owners map[string]commandOwner) {
	data := i.ApplicationCommandData()
	name := data.Name
	options := data.Options

	// Resolve subcommand if present.
	key := name
	if len(options) > 0 && options[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		key = name + "/" + options[0].Name
		options = options[0].Options
	}

	owner, ok := owners[key]
	if !ok {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Unknown command.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Acknowledge immediately.
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Build body from options.
	body := make(map[string]interface{})
	for _, opt := range options {
		body[opt.Name] = opt.Value
	}
	bodyData, _ := json.Marshal(body)

	raw, err := b.sdk.RouteToPlugin(context.Background(), owner.pluginID, "POST", owner.endpoint, bytes.NewReader(bodyData))
	if err != nil {
		log.Printf("handleSlashCommand /%s: RouteToPlugin error: %v", key, err)
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: "Command failed: " + err.Error(),
		})
		return
	}

	var resp pluginsdk.DiscordCommandResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: "Invalid response from plugin."})
		return
	}

	params := &discordgo.WebhookParams{}
	switch resp.Type {
	case "embed":
		for _, e := range resp.Embeds {
			embed := &discordgo.MessageEmbed{
				Title: e.Title,
				Color: e.Color,
			}
			for _, f := range e.Fields {
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
					Name:   f.Name,
					Value:  f.Value,
					Inline: f.Inline,
				})
			}
			params.Embeds = append(params.Embeds, embed)
		}
	default:
		params.Content = resp.Content
	}

	s.FollowupMessageCreate(i.Interaction, true, params)
}

// discordOptionType maps a string type name to a Discord option type.
func discordOptionType(t string) discordgo.ApplicationCommandOptionType {
	switch t {
	case "integer":
		return discordgo.ApplicationCommandOptionInteger
	case "boolean":
		return discordgo.ApplicationCommandOptionBoolean
	default:
		return discordgo.ApplicationCommandOptionString
	}
}
