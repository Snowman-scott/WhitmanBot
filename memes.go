package main

import (
	_ "embed"
	"log"
	"math/rand/v2"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darui3018823/dgo"
	"whitmanBot/audio"
)

// Built-in meme list. Set MEME_FILE to use a different file without rebuilding.
//
//go:embed memes.txt
var defaultMemes string

var textChannels sync.Map // map[guildID string]string — where to announce meme tracks

// memeTracks loads the list from MEME_FILE if set, otherwise the embedded one.
func memeTracks() []string {
	src := defaultMemes
	if path := os.Getenv("MEME_FILE"); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			log.Printf("[Memes] Can't read MEME_FILE, using built-in list: %v", err)
		} else {
			src = string(b)
		}
	}

	var tracks []string
	for _, line := range strings.Split(src, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			tracks = append(tracks, line)
		}
	}
	return tracks
}

// memeIdleTime is how long the bot must sit idle in voice before playing a
// meme. Override with MEME_IDLE_SECONDS (0 disables memes).
func memeIdleTime() time.Duration {
	if v := os.Getenv("MEME_IDLE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return time.Duration(n) * time.Second
		}
	}
	return time.Minute
}

// humansInChannel counts non-bot users in a voice channel.
func humansInChannel(s *dgo.Session, guildID, channelID string) int {
	guild, err := s.State.Guild(guildID)
	if err != nil {
		return 0
	}
	n := 0
	for _, vs := range guild.VoiceStates {
		if vs.ChannelID != channelID || vs.UserID == s.State.User.ID {
			continue
		}
		if vs.Member != nil && vs.Member.User != nil && vs.Member.User.Bot {
			continue
		}
		n++
	}
	return n
}

// StartMemeWatcher plays a random meme track whenever the bot has been idle
// in a voice channel (with people in it) for memeIdleTime.
func StartMemeWatcher(s *dgo.Session) {
	idle := memeIdleTime()
	if idle <= 0 {
		log.Println("[Memes] Disabled")
		return
	}

	go func() {
		for range time.Tick(5 * time.Second) {
			players.Range(func(key, value any) bool {
				guildID := key.(string)
				p := value.(*audio.Player)

				if p.IdleFor() < idle {
					return true
				}
				vcID := p.ChannelID()
				if vcID == "" || humansInChannel(s, guildID, vcID) == 0 {
					return true
				}

				// Restart the timer now so we don't fire again while resolving.
				p.ResetIdle()
				go playMeme(s, guildID, p)
				return true
			})
		}
	}()
}

func playMeme(s *dgo.Session, guildID string, p *audio.Player) {
	tracks := memeTracks()
	if len(tracks) == 0 {
		return
	}

	query := tracks[rand.IntN(len(tracks))]
	song, err := audio.Resolve(query, "🎲 AFK memes")
	if err != nil {
		log.Printf("[Memes] Couldn't resolve %q: %v", query, err)
		return
	}

	// Someone may have queued a song while we were resolving.
	if p.IsPlaying() || p.ChannelID() == "" {
		return
	}
	p.Enqueue(song)

	if v, ok := textChannels.Load(guildID); ok {
		embed := nowPlayingEmbed(song)
		embed.Author.Name = "🎲 Too quiet in here..."
		embed.Description = "Nobody's played anything for a while, so here's a meme."
		_, _ = s.ChannelMessageSendEmbed(v.(string), embed)
	}
}
