package main

import (
	_ "embed"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darui3018823/dgo"
	"whitmanBot/audio"
)

// Starting meme list, used until the first !meme add/remove saves a copy to
// MEME_FILE.
//
//go:embed memes.txt
var defaultMemes string

var textChannels sync.Map // map[guildID string]string — where to announce meme tracks

var memeFileMu sync.Mutex

// memeFile is where the editable meme list is saved (MEME_FILE, default memes.txt).
func memeFile() string {
	if path := os.Getenv("MEME_FILE"); path != "" {
		return path
	}
	return "memes.txt"
}

// memeTracks returns the saved meme list, or the built-in one if none is saved yet.
func memeTracks() []string {
	memeFileMu.Lock()
	defer memeFileMu.Unlock()
	return loadMemesLocked()
}

func loadMemesLocked() []string {
	src := defaultMemes
	b, err := os.ReadFile(memeFile())
	if err == nil {
		src = string(b)
	} else if !os.IsNotExist(err) {
		log.Printf("[Memes] Can't read %s, using built-in list: %v", memeFile(), err)
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

func saveMemesLocked(tracks []string) error {
	path := memeFile()
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	content := "# Tracks played when the bot sits idle in voice. Managed with !meme add/remove.\n" +
		strings.Join(tracks, "\n") + "\n"
	// Write to a temp file and rename so a crash can't leave a half-written list.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// addMeme appends a track. It reports false if the track is already listed.
func addMeme(track string) (bool, error) {
	memeFileMu.Lock()
	defer memeFileMu.Unlock()
	tracks := loadMemesLocked()
	for _, t := range tracks {
		if strings.EqualFold(t, track) {
			return false, nil
		}
	}
	return true, saveMemesLocked(append(tracks, track))
}

// removeMeme deletes the track at the 1-based position n and returns it.
func removeMeme(n int) (string, error) {
	memeFileMu.Lock()
	defer memeFileMu.Unlock()
	tracks := loadMemesLocked()
	if n < 1 || n > len(tracks) {
		return "", fmt.Errorf("there's no track #%d (the list has %d)", n, len(tracks))
	}
	removed := tracks[n-1]
	tracks = append(tracks[:n-1], tracks[n:]...)
	return removed, saveMemesLocked(tracks)
}

// memeAutoplayEnabled reads MEME_AUTOPLAY (true/false, default true).
func memeAutoplayEnabled() bool {
	v := os.Getenv("MEME_AUTOPLAY")
	if v == "" {
		return true
	}
	enabled, err := strconv.ParseBool(v)
	if err != nil {
		log.Printf("[Memes] MEME_AUTOPLAY=%q isn't true/false, leaving it on", v)
		return true
	}
	return enabled
}

// memeIdleTime is how long the bot must sit idle in voice before playing a
// meme. Override with MEME_IDLE_SECONDS. Returns 0 when autoplay is off.
func memeIdleTime() time.Duration {
	if !memeAutoplayEnabled() {
		return 0
	}
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

// StartMemeWatcher starts a meme run whenever the bot has been idle in a
// voice channel (with people in it) for memeIdleTime.
func StartMemeWatcher(s *dgo.Session) {
	idle := memeIdleTime()
	if idle <= 0 {
		log.Println("[Memes] Autoplay off")
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
				go playNextMeme(s, guildID, p, true)
				return true
			})
		}
	}()
}

// A meme run starts once the bot has sat idle for memeIdleTime. It plays the
// whole meme list in a shuffled order, back to back while the queue stays
// empty, then moves on to !random picks once every meme has played. Queueing
// a song by hand (or !stop) ends the run, so the next one gets the full list.
type memeRun struct {
	left   []string // memes not played yet this run
	played int
}

var (
	memeRunsMu sync.Mutex
	memeRuns   = map[string]*memeRun{} // guildID → current run
)

func endMemeRun(guildID string) {
	memeRunsMu.Lock()
	defer memeRunsMu.Unlock()
	delete(memeRuns, guildID)
}

// next returns the run's next search query, and whether it came from the
// meme list (as opposed to a random pick once the list is used up).
func (r *memeRun) next() (query string, fromList bool, n int) {
	memeRunsMu.Lock()
	defer memeRunsMu.Unlock()
	r.played++
	if len(r.left) > 0 {
		query, r.left = r.left[0], r.left[1:]
		return query, true, r.played
	}
	return randomPick(""), false, r.played
}

// continueMemeRun plays the next meme when the queue empties mid-run.
func continueMemeRun(s *dgo.Session, guildID string, p *audio.Player) {
	if !memeAutoplayEnabled() {
		return
	}
	vcID := p.ChannelID()
	if vcID == "" || humansInChannel(s, guildID, vcID) == 0 {
		return
	}
	p.ResetIdle()
	playNextMeme(s, guildID, p, false)
}

// playNextMeme queues the next track of the guild's meme run. With start set
// it begins a new run if none is going; otherwise it only continues one.
func playNextMeme(s *dgo.Session, guildID string, p *audio.Player, start bool) {
	memeRunsMu.Lock()
	run := memeRuns[guildID]
	if run == nil && start {
		tracks := memeTracks()
		rand.Shuffle(len(tracks), func(i, j int) { tracks[i], tracks[j] = tracks[j], tracks[i] })
		run = &memeRun{left: tracks}
		memeRuns[guildID] = run
	}
	memeRunsMu.Unlock()
	if run == nil {
		return
	}

	// Skip past a few tracks yt-dlp can't find rather than going quiet.
	var song *audio.Song
	var fromList bool
	var n int
	for range 3 {
		var query string
		query, fromList, n = run.next()
		var err error
		if song, err = audio.Resolve(query, "🎲 AFK memes"); err == nil {
			break
		}
		log.Printf("[Memes] Couldn't resolve %q: %v", query, err)
	}
	if song == nil {
		return
	}

	// Someone may have queued a song or hit !stop while we were resolving.
	memeRunsMu.Lock()
	current := memeRuns[guildID] == run
	memeRunsMu.Unlock()
	if !current || p.IsPlaying() || p.ChannelID() == "" {
		return
	}
	p.Enqueue(song)

	if v, ok := textChannels.Load(guildID); ok {
		embed := nowPlayingEmbed(song)
		switch {
		case n == 1:
			embed.Author.Name = "🎲 Too quiet in here..."
			embed.Description = "Nobody's played anything for a while, so here's a meme."
		case fromList:
			embed.Author.Name = fmt.Sprintf("🎲 Meme #%d", n)
			embed.Description = "Queue's still empty, so here's another. `!play` something to take over."
		default:
			embed.Author.Name = "🎲 Out of memes..."
			embed.Description = "That's every meme on the list, so here's a random song instead."
		}
		_, _ = s.ChannelMessageSendEmbed(v.(string), embed)
	}
}

// ── !meme command ─────────────────────────────────────────────────────────────

const memeUsage = "❌ Usage: `!meme add <song or URL>` · `!meme list` · `!meme remove <number>`"

func musicMeme(s *dgo.Session, m *dgo.MessageCreate, args []string) {
	if len(args) == 0 {
		_, _ = s.ChannelMessageSend(m.ChannelID, memeUsage)
		return
	}

	switch strings.ToLower(args[0]) {
	case "add":
		memeAdd(s, m, strings.TrimSpace(strings.Join(args[1:], " ")))
	case "list", "ls":
		memeList(s, m)
	case "remove", "rm", "delete":
		if len(args) < 2 {
			_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Usage: `!meme remove <number>` (see `!meme list`)")
			return
		}
		n, err := strconv.Atoi(args[1])
		if err != nil {
			_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Give the track's number from `!meme list`.")
			return
		}
		removed, err := removeMeme(n)
		if err != nil {
			_, _ = s.ChannelMessageSend(m.ChannelID, "❌ "+capitalize(err.Error())+".")
			return
		}
		_, _ = s.ChannelMessageSend(m.ChannelID, fmt.Sprintf("🗑 Removed **%s** from the meme list.", removed))
	default:
		_, _ = s.ChannelMessageSend(m.ChannelID, memeUsage)
	}
}

func memeAdd(s *dgo.Session, m *dgo.MessageCreate, query string) {
	if query == "" {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ Usage: `!meme add <song or URL>`")
		return
	}
	if strings.Contains(query, "\n") || strings.HasPrefix(query, "#") {
		_, _ = s.ChannelMessageSend(m.ChannelID, "❌ That doesn't look like a song.")
		return
	}

	status := sendStatus(s, m.ChannelID, "🔍 Checking that track exists...")
	go func() {
		// Make sure yt-dlp can actually find it before saving.
		song, err := audio.Resolve(query, m.Author.Username)
		if err != nil {
			status.text(fmt.Sprintf("❌ Nothing found for **%s**", query))
			return
		}
		added, err := addMeme(query)
		switch {
		case err != nil:
			log.Printf("[Memes] Saving list: %v", err)
			status.text("❌ Couldn't save the meme list.")
		case !added:
			status.text(fmt.Sprintf("ℹ️ **%s** is already on the meme list.", query))
		default:
			status.text(fmt.Sprintf("➕ Added **%s** to the meme list (found: %s).", query, song.Title))
		}
	}()
}

func memeList(s *dgo.Session, m *dgo.MessageCreate) {
	tracks := memeTracks()
	if len(tracks) == 0 {
		_, _ = s.ChannelMessageSend(m.ChannelID, "📭 The meme list is empty. Add one with `!meme add <song or URL>`.")
		return
	}

	// Stay well under Discord's 4096-character embed description limit.
	var b strings.Builder
	shown := 0
	for i, t := range tracks {
		line := fmt.Sprintf("`%d.` %s\n", i+1, t)
		if b.Len()+len(line) > 3800 {
			break
		}
		b.WriteString(line)
		shown++
	}

	status := "🎲 Autoplay is off"
	if idle := memeIdleTime(); idle > 0 {
		status = fmt.Sprintf("🎲 Plays after %s idle in voice", friendlyDuration(idle))
	}
	footer := fmt.Sprintf("%d tracks · %s", len(tracks), status)
	if shown < len(tracks) {
		footer = fmt.Sprintf("Showing %d of %d tracks · %s", shown, len(tracks), status)
	}

	_, _ = s.ChannelMessageSendEmbed(m.ChannelID, &dgo.MessageEmbed{
		Title:       "🎲 Meme List",
		Color:       0xBF00FF,
		Description: b.String(),
		Footer:      &dgo.MessageEmbedFooter{Text: footer},
	})
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
