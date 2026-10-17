package main

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/darui3018823/dgo"
	"whitmanBot/audio"
)

var players sync.Map // map[guildID string]*audio.Player

func getOrCreatePlayer(s *dgo.Session, guildID string) *audio.Player {
	v, _ := players.LoadOrStore(guildID, audio.NewPlayer(s, guildID))
	return v.(*audio.Player)
}

// userVoiceChannel returns the voice channel ID the given user is currently in.
func userVoiceChannel(s *dgo.Session, guildID, userID string) string {
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return ""
	}
	for _, vs := range guild.VoiceStates {
		if vs.UserID == userID {
			return vs.ChannelID
		}
	}
	return ""
}

func RegisterMusicHandlers(s *dgo.Session) {
	s.AddHandler(func(s *dgo.Session, m *dgo.MessageCreate) {
		if m.Author.Bot || m.GuildID == "" {
			return
		}

		parts := strings.Fields(m.Content)
		if len(parts) == 0 {
			return
		}

		cmd := strings.ToLower(parts[0])
		args := parts[1:]

		switch cmd {
		case "!play", "!p":
			musicPlay(s, m, args)
		case "!skip", "!s":
			musicSkip(s, m)
		case "!pause":
			musicPause(s, m)
		case "!resume":
			musicResume(s, m)
		case "!stop":
			musicStop(s, m)
		case "!queue", "!q":
			musicQueue(s, m)
		case "!nowplaying", "!np":
			musicNowPlaying(s, m)
		case "!volume", "!vol":
			musicVolume(s, m, args)
		case "!loop":
			musicLoop(s, m, args)
		case "!disconnect", "!dc":
			musicDisconnect(s, m)
		case "!meme", "!memes":
			musicMeme(s, m, args)
		case "!help", "!h":
			musicHelp(s, m)
		}
	})
}

// ── Commands ──────────────────────────────────────────────────────────────────

func musicPlay(s *dgo.Session, m *dgo.MessageCreate, args []string) {
	if len(args) == 0 {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Usage: `!play <song name or URL>`")
		return
	}

	vcID := userVoiceChannel(s, m.GuildID, m.Author.ID)
	if vcID == "" {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Join a voice channel first.")
		return
	}

	textChannels.Store(m.GuildID, m.ChannelID)

	query := strings.Join(args, " ")
	msg, _ := s.ChannelMessageSend(m.ChannelID, "🔍 Searching...")

	go func() {
		song, err := audio.Resolve(query, m.Author.Username)
		if err != nil {
			_, _ = s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("❌ Nothing found for **%s**", query))
			return
		}

		p := getOrCreatePlayer(s, m.GuildID)

		if err := p.JoinChannel(vcID); err != nil {
			_, _ = s.ChannelMessageEdit(m.ChannelID, msg.ID, fmt.Sprintf("❌ Can't join voice: %v", err))
			return
		}

		pos := p.Enqueue(song)

		var embed *dgo.MessageEmbed
		if pos == 0 {
			embed = nowPlayingEmbed(song)
		} else {
			embed = &dgo.MessageEmbed{
				Author:      &dgo.MessageEmbedAuthor{Name: "➕ Added to Queue"},
				Title:       song.Title,
				URL:         song.YoutubeURL,
				Color:       0x8B3FFF,
				Description: fmt.Sprintf("Position **#%d** in queue", pos),
				Thumbnail:   &dgo.MessageEmbedThumbnail{URL: song.Thumbnail},
				Fields: []*dgo.MessageEmbedField{
					{Name: "Artist", Value: song.Artist, Inline: true},
					{Name: "Duration", Value: song.DurationStr(), Inline: true},
				},
			}
		}

		_, _ = s.ChannelMessageEditEmbed(m.ChannelID, msg.ID, embed)
	}()
}

func musicSkip(s *dgo.Session, m *dgo.MessageCreate) {
	p := getOrCreatePlayer(s, m.GuildID)
	cur := p.Current()
	if cur == nil {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Nothing is playing.")
		return
	}
	p.Skip()
	_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("⏭ Skipped **%s**", cur.Title))
}

func musicPause(s *dgo.Session, m *dgo.MessageCreate) {
	p := getOrCreatePlayer(s, m.GuildID)
	if p.Current() == nil {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Nothing is playing.")
		return
	}
	p.Pause()
	_, _ = s.ChannelMessageSend(m.ChannelID, "⏸ Paused.")
}

func musicResume(s *dgo.Session, m *dgo.MessageCreate) {
	p := getOrCreatePlayer(s, m.GuildID)
	if !p.IsPaused() {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Not paused.")
		return
	}
	p.Resume()
	_, _ = s.ChannelMessageSend(m.ChannelID, "▶ Resumed.")
}

func musicStop(s *dgo.Session, m *dgo.MessageCreate) {
	p := getOrCreatePlayer(s, m.GuildID)
	p.Stop()
	_, _ = s.ChannelMessageSend(m.ChannelID, "⏹ Stopped and queue cleared.")
}

func musicDisconnect(s *dgo.Session, m *dgo.MessageCreate) {
	p := getOrCreatePlayer(s, m.GuildID)
	p.Disconnect()
	_, _ = s.ChannelMessageSend(m.ChannelID, "👋 Disconnected.")
}

func musicNowPlaying(s *dgo.Session, m *dgo.MessageCreate) {
	p := getOrCreatePlayer(s, m.GuildID)
	cur := p.Current()
	if cur == nil {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Nothing is playing right now.")
		return
	}
	_, _ = s.ChannelMessageSendEmbed(m.ChannelID, nowPlayingEmbed(cur))
}

func musicQueue(s *dgo.Session, m *dgo.MessageCreate) {
	p := getOrCreatePlayer(s, m.GuildID)
	cur := p.Current()
	queue := p.GetQueue()

	if cur == nil && len(queue) == 0 {
		_, _ = s.ChannelMessageSend(m.ChannelID, "📭 Queue is empty.")
		return
	}

	embed := &dgo.MessageEmbed{
		Title: "📋 Queue",
		Color: 0xBF00FF,
	}

	if cur != nil {
		label := "▶ Now Playing"
		if p.IsPaused() {
			label = "⏸ Paused"
		}
		embed.Fields = append(embed.Fields, &dgo.MessageEmbedField{
			Name:  label,
			Value: fmt.Sprintf("`%s` — %s", cur.Title, cur.DurationStr()),
		})
	}

	if len(queue) > 0 {
		limit := len(queue)
		if limit > 10 {
			limit = 10
		}
		lines := make([]string, 0, limit)
		for i, t := range queue[:limit] {
			lines = append(lines, fmt.Sprintf("`%d.` %s — %s", i+1, t.Title, t.DurationStr()))
		}
		embed.Fields = append(embed.Fields, &dgo.MessageEmbedField{
			Name:  "Up Next",
			Value: strings.Join(lines, "\n"),
		})
		if len(queue) > 10 {
			embed.Footer = &dgo.MessageEmbedFooter{
				Text: fmt.Sprintf("...and %d more", len(queue)-10),
			}
		}
	}

	// Show loop mode in footer if active
	loop := p.GetLoop()
	if loop != audio.LoopOff {
		loopStr := map[audio.LoopMode]string{
			audio.LoopOne:   "🔂 Looping track",
			audio.LoopQueue: "🔁 Looping queue",
		}[loop]
		if embed.Footer == nil {
			embed.Footer = &dgo.MessageEmbedFooter{Text: loopStr}
		} else {
			embed.Footer.Text = loopStr + " · " + embed.Footer.Text
		}
	}

	_, _ = s.ChannelMessageSendEmbed(m.ChannelID, embed)
}

func musicVolume(s *dgo.Session, m *dgo.MessageCreate, args []string) {
	p := getOrCreatePlayer(s, m.GuildID)
	if len(args) == 0 {
		_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("🔊 Volume is **%d%%**. Use `!volume <0–100>` to change.", p.GetVolume()))
		return
	}
	var vol int
	fmt.Sscanf(args[0], "%d", &vol)
	p.SetVolume(vol)
	_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("🔊 Volume set to **%d%%** (kicks in on next track).", vol))
}

func musicLoop(s *dgo.Session, m *dgo.MessageCreate, args []string) {
	p := getOrCreatePlayer(s, m.GuildID)
	mode := "off"
	if len(args) > 0 {
		mode = strings.ToLower(args[0])
	}
	switch mode {
	case "off":
		p.SetLoop(audio.LoopOff)
		_, _ = s.ChannelMessageSend(m.ChannelID, "➡️ Loop off.")
	case "one":
		p.SetLoop(audio.LoopOne)
		_, _ = s.ChannelMessageSend(m.ChannelID, "🔂 Looping current track.")
	case "queue", "q":
		p.SetLoop(audio.LoopQueue)
		_, _ = s.ChannelMessageSend(m.ChannelID, "🔁 Looping queue.")
	default:
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Usage: `!loop [off | one | queue]`")
	}
}

func musicHelp(s *dgo.Session, m *dgo.MessageCreate) {
	commands := []struct{ usage, desc string }{
		{"!play <song or URL>", "Play a song or add it to the queue · `!p`"},
		{"!skip", "Skip the current song · `!s`"},
		{"!pause", "Pause playback"},
		{"!resume", "Resume playback"},
		{"!stop", "Stop and clear the queue"},
		{"!queue", "Show the queue · `!q`"},
		{"!nowplaying", "Show the current song · `!np`"},
		{"!volume [0–100]", "Show or set the volume · `!vol`"},
		{"!loop [off | one | queue]", "Set loop mode"},
		{"!disconnect", "Leave the voice channel · `!dc`"},
		{"!meme add <song or URL>", "Add a track to the AFK meme list"},
		{"!meme list", "Show the AFK meme list"},
		{"!meme remove <number>", "Remove a track from the AFK meme list"},
		{"!help", "Show this message · `!h`"},
	}

	lines := make([]string, 0, len(commands))
	for _, c := range commands {
		lines = append(lines, fmt.Sprintf("`%s` — %s", c.usage, c.desc))
	}

	embed := &dgo.MessageEmbed{
		Title:       "🎵 Music Commands",
		Color:       0xBF00FF,
		Description: strings.Join(lines, "\n"),
	}
	if idle := memeIdleTime(); idle > 0 {
		embed.Footer = &dgo.MessageEmbedFooter{
			Text: fmt.Sprintf("🎲 Leave me idle in voice for %s and I'll play a meme.", friendlyDuration(idle)),
		}
	}
	_, _ = s.ChannelMessageSendEmbed(m.ChannelID, embed)
}

// friendlyDuration formats durations like "1 minute" or "90 seconds".
func friendlyDuration(d time.Duration) string {
	if d%time.Minute == 0 {
		if n := int(d / time.Minute); n != 1 {
			return fmt.Sprintf("%d minutes", n)
		}
		return "1 minute"
	}
	return fmt.Sprintf("%d seconds", int(d/time.Second))
}

// ── Embed helper ──────────────────────────────────────────────────────────────

func nowPlayingEmbed(s *audio.Song) *dgo.MessageEmbed {
	return &dgo.MessageEmbed{
		Author:    &dgo.MessageEmbedAuthor{Name: "▶ Now Playing"},
		Title:     s.Title,
		URL:       s.YoutubeURL,
		Color:     0xBF00FF,
		Thumbnail: &dgo.MessageEmbedThumbnail{URL: s.Thumbnail},
		Fields: []*dgo.MessageEmbedField{
			{Name: "Artist", Value: s.Artist, Inline: true},
			{Name: "Duration", Value: s.DurationStr(), Inline: true},
			{Name: "Requested by", Value: s.RequestedBy, Inline: true},
		},
	}
}
