package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/darui3018823/dgo"
)

// voiceAccessProblem explains why the bot can't join or be heard in a voice
// channel, so locked channels get a clear message instead of a join that
// silently times out. It returns "" when everything looks fine (or when the
// cache doesn't know enough to tell).
func voiceAccessProblem(s *dgo.Session, guildID, vcID string) string {
	ch, err := s.State.Channel(vcID)
	if err != nil {
		return ""
	}
	perms, err := s.State.UserChannelPermissions(s.State.User.ID, vcID)
	if err != nil {
		return ""
	}

	needed := []struct {
		perm int64
		name string
	}{
		{dgo.PermissionViewChannel, "View Channel"},
		{dgo.PermissionVoiceConnect, "Connect"},
	}
	// Stage speakers are invited up instead of needing Speak.
	if ch.Type != dgo.ChannelTypeGuildStageVoice {
		needed = append(needed, struct {
			perm int64
			name string
		}{dgo.PermissionVoiceSpeak, "Speak"})
	}
	var missing []string
	for _, n := range needed {
		if perms&n.perm == 0 {
			missing = append(missing, n.name)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("I'm missing **%s** in <#%s>. Allow it for my role in that channel's permission settings (channel overrides beat server roles).",
			strings.Join(missing, "**, **"), vcID)
	}

	// Move Members lets the bot squeeze into a full channel.
	if ch.UserLimit > 0 && perms&dgo.PermissionVoiceMoveMembers == 0 {
		if n, inside := usersInChannel(s, guildID, vcID); !inside && n >= ch.UserLimit {
			return fmt.Sprintf("<#%s> is full (%d/%d). Give me **Move Members** so I can join anyway, or raise the user limit.",
				vcID, n, ch.UserLimit)
		}
	}
	return ""
}

// usersInChannel counts everyone (bots included) in a voice channel and
// reports whether this bot is one of them.
func usersInChannel(s *dgo.Session, guildID, vcID string) (n int, botInside bool) {
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return 0, false
	}
	for _, vs := range guild.VoiceStates {
		if vs.ChannelID != vcID {
			continue
		}
		n++
		if vs.UserID == s.State.User.ID {
			botInside = true
		}
	}
	return n, botInside
}

// speakOnStage makes sure the bot is a speaker when it joins a Stage channel,
// where everyone starts in the audience and can't be heard. It returns a note
// for the user if it could only raise its hand.
func speakOnStage(s *dgo.Session, guildID, vcID string) string {
	ch, err := s.State.Channel(vcID)
	if err != nil || ch.Type != dgo.ChannelTypeGuildStageVoice {
		return ""
	}

	unsuppress := false
	err = s.CurrentUserVoiceStateEdit(guildID, &dgo.CurrentUserVoiceStateEditParams{
		UserVoiceStateEditParams: dgo.UserVoiceStateEditParams{ChannelID: &vcID, Suppress: &unsuppress},
	})
	if err == nil {
		return ""
	}
	log.Printf("[Music] Can't become a stage speaker in %s: %v", vcID, err)

	// No Mute Members: ask to speak instead.
	now := time.Now()
	nowPtr := &now
	err = s.CurrentUserVoiceStateEdit(guildID, &dgo.CurrentUserVoiceStateEditParams{
		UserVoiceStateEditParams: dgo.UserVoiceStateEditParams{ChannelID: &vcID},
		RequestToSpeakTimestamp:  &nowPtr,
	})
	if err != nil {
		log.Printf("[Music] Can't request to speak in %s: %v", vcID, err)
		return "⚠️ I'm stuck in the stage audience. Invite me to speak, or give me **Mute Members** so I can do it myself."
	}
	return "✋ I've raised my hand on stage. Invite me to speak so you can hear me."
}

// statusMsg is a "working on it..." message that's later replaced with the
// result. If the first send failed (no access to the channel, rate limit...)
// the result is sent as a new message instead, rather than editing nothing.
type statusMsg struct {
	s         *dgo.Session
	channelID string
	msg       *dgo.Message
}

func sendStatus(s *dgo.Session, channelID, content string) *statusMsg {
	msg, err := s.ChannelMessageSend(channelID, content)
	if err != nil {
		log.Printf("[Music] Can't post in %s: %v", channelID, err)
	}
	return &statusMsg{s: s, channelID: channelID, msg: msg}
}

func (sm *statusMsg) text(content string) {
	if sm.msg != nil {
		if _, err := sm.s.ChannelMessageEdit(sm.channelID, sm.msg.ID, content); err == nil {
			return
		}
	}
	_, _ = sm.s.ChannelMessageSend(sm.channelID, content)
}

func (sm *statusMsg) embed(e *dgo.MessageEmbed) {
	if sm.msg != nil {
		if _, err := sm.s.ChannelMessageEditEmbed(sm.channelID, sm.msg.ID, e); err == nil {
			return
		}
	}
	_, _ = sm.s.ChannelMessageSendEmbed(sm.channelID, e)
}
