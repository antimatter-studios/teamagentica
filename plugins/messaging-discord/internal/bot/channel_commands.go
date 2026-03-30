package bot

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/antimatter-studios/teamagentica/plugins/messaging-discord/internal/channels"
)

// nativeCommands are slash commands owned by the Discord plugin itself.
var nativeCommands = []*discordgo.ApplicationCommand{
	{
		Name:        "newchannel",
		Description: "Link this channel to an alias — messages auto-route without @mention",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "target",
				Description: "Alias to route to (e.g. codearchitect). Future: workspace/name",
				Required:    true,
			},
		},
	},
	{
		Name:        "linkchannel",
		Description: "Link this existing channel to an alias — messages auto-route without @mention",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "target",
				Description: "Alias to route to (e.g. codearchitect)",
				Required:    true,
			},
		},
	},
	{
		Name:        "unlinkchannel",
		Description: "Remove the alias mapping from this channel",
	},
	{
		Name:        "channels",
		Description: "List all channel-to-alias mappings",
	},
}

// registerNativeCommands registers the plugin's own slash commands with Discord.
func (b *Bot) registerNativeCommands() {
	if b.channelStore == nil || b.guildID == "" {
		return
	}
	for _, cmd := range nativeCommands {
		if _, err := b.session.ApplicationCommandCreate(b.botUserID, b.guildID, cmd); err != nil {
			log.Printf("registerNativeCommands: /%s: %v", cmd.Name, err)
		} else {
			log.Printf("Registered native command: /%s", cmd.Name)
		}
	}
}

// handleNativeCommand dispatches native slash commands. Returns true if handled.
func (b *Bot) handleNativeCommand(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if b.channelStore == nil {
		return false
	}
	data := i.ApplicationCommandData()
	switch data.Name {
	case "newchannel":
		b.handleNewChannel(s, i)
		return true
	case "linkchannel":
		b.handleLinkChannel(s, i)
		return true
	case "unlinkchannel":
		b.handleUnlinkChannel(s, i)
		return true
	case "channels":
		b.handleListChannels(s, i)
		return true
	}
	return false
}

// handleLinkChannel maps the current channel to a target alias.
func (b *Bot) handleLinkChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	target := ""
	for _, opt := range data.Options {
		if opt.Name == "target" {
			target = opt.StringValue()
		}
	}
	target = strings.TrimPrefix(strings.TrimSpace(target), "@")
	if target == "" {
		respondEphemeral(s, i, "Target is required.")
		return
	}

	channelID := i.ChannelID

	if err := b.channelStore.Set(channelID, target); err != nil {
		log.Printf("handleLinkChannel: store error: %v", err)
		respondEphemeral(s, i, "Failed to save mapping: "+err.Error())
		return
	}

	log.Printf("Channel %s linked to @%s", channelID, target)
	respondEphemeral(s, i, fmt.Sprintf("Linked <#%s> → **@%s**\nMessages here auto-route — no @mention needed.", channelID, target))
}

// handleNewChannel creates a new Discord channel named after the target and maps it.
func (b *Bot) handleNewChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	target := ""
	for _, opt := range data.Options {
		if opt.Name == "target" {
			target = opt.StringValue()
		}
	}
	target = strings.TrimPrefix(strings.TrimSpace(target), "@")
	if target == "" {
		respondEphemeral(s, i, "Target is required.")
		return
	}

	if b.guildID == "" {
		respondEphemeral(s, i, "Guild ID not configured.")
		return
	}

	// If this target is already mapped, remove the old mapping (but not the old channel).
	b.channelStore.DeleteByTarget(target)

	// Create a new text channel named after the target.
	ch, err := s.GuildChannelCreateComplex(b.guildID, discordgo.GuildChannelCreateData{
		Name:  channels.SanitizeChannelName(target),
		Type:  discordgo.ChannelTypeGuildText,
		Topic: fmt.Sprintf("Chat with @%s", target),
	})
	if err != nil {
		log.Printf("handleNewChannel: create channel error: %v", err)
		respondEphemeral(s, i, "Failed to create channel: "+err.Error())
		return
	}

	if err := b.channelStore.Set(ch.ID, target); err != nil {
		log.Printf("handleNewChannel: store error: %v", err)
		respondEphemeral(s, i, "Channel created but failed to save mapping: "+err.Error())
		return
	}

	log.Printf("Created channel #%s (%s) linked to @%s", ch.Name, ch.ID, target)
	respondEphemeral(s, i, fmt.Sprintf("Created <#%s> → **@%s**\nMessages there auto-route — no @mention needed.", ch.ID, target))
}

// handleUnlinkChannel removes the alias mapping from the current channel.
func (b *Bot) handleUnlinkChannel(s *discordgo.Session, i *discordgo.InteractionCreate) {
	old, existed := b.channelStore.Delete(i.ChannelID)
	if !existed {
		respondEphemeral(s, i, "This channel is not linked to any target.")
		return
	}
	respondEphemeral(s, i, fmt.Sprintf("Unlinked this channel from @%s.", old))
}

// handleListChannels lists all channel-target mappings.
func (b *Bot) handleListChannels(s *discordgo.Session, i *discordgo.InteractionCreate) {
	mappings := b.channelStore.ListAll()
	if len(mappings) == 0 {
		respondEphemeral(s, i, "No channel mappings configured. Use `/newchannel` to create one.")
		return
	}

	var lines []string
	for _, m := range mappings {
		lines = append(lines, fmt.Sprintf("<#%s> → **@%s**", m.ChannelID, m.Target))
	}

	respondEphemeral(s, i, fmt.Sprintf("**Channel Mappings** (%d)\n%s", len(mappings), strings.Join(lines, "\n")))
}

// respondEphemeral sends an ephemeral response to an interaction.
func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// SetChannelStore attaches the channel mapping store.
func (b *Bot) SetChannelStore(cs *channels.Store) {
	b.channelStore = cs
}
