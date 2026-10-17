package audio

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/darui3018823/dgo"
	"github.com/jonas747/ogg"
)

// LoopMode controls queue repetition behaviour.
type LoopMode string

const (
	LoopOff   LoopMode = "off"
	LoopOne   LoopMode = "one"
	LoopQueue LoopMode = "queue"
)

// Player manages the voice connection, queue, and playback for one guild.
type Player struct {
	mu      sync.Mutex
	sess    *dgo.Session
	guildID string

	queue   []*Song
	current *Song

	vc  *dgo.VoiceConnection
	cmd *exec.Cmd

	skipCh chan struct{}

	playing  bool
	paused   bool
	loopMode LoopMode
	volume   int // 0–100

	idleSince time.Time // when playback last stopped (or the bot joined)
}

func NewPlayer(sess *dgo.Session, guildID string) *Player {
	return &Player{
		sess:     sess,
		guildID:  guildID,
		skipCh:   make(chan struct{}, 1),
		loopMode: LoopOff,
		volume:   100,
	}
}

// JoinChannel connects (or moves) the bot to a voice channel.
func (p *Player) JoinChannel(channelID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.vc != nil && p.vc.ChannelID == channelID && voiceReady(p.vc) {
		return nil
	}

	// mute=false, deaf=true (self-deafen)
	vc, err := p.sess.ChannelVoiceJoin(p.guildID, channelID, false, true)
	if err != nil {
		return err
	}
	p.vc = vc
	p.idleSince = time.Now()
	return nil
}

// Enqueue adds a song and starts playback if nothing is running.
// Returns the 1-based queue position, or 0 if it will play immediately.
func (p *Player) Enqueue(song *Song) int {
	p.mu.Lock()
	p.queue = append(p.queue, song)
	pos := len(p.queue)
	wasPlaying := p.playing
	p.mu.Unlock()

	if !wasPlaying {
		go p.playbackLoop()
		return 0
	}
	return pos
}

func (p *Player) Skip() {
	p.mu.Lock()
	cmd := p.cmd
	p.mu.Unlock()
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}

	select {
	case p.skipCh <- struct{}{}:
	default:
	}
}

func (p *Player) Pause() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = true
}

func (p *Player) Resume() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.paused = false
}

func (p *Player) Stop() {
	p.mu.Lock()
	p.queue = nil
	p.paused = false
	cmd := p.cmd
	p.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	p.Skip()
}

func (p *Player) Disconnect() {
	p.Stop()
	p.mu.Lock()
	vc := p.vc
	p.vc = nil
	p.mu.Unlock()
	if vc != nil {
		_ = vc.Disconnect()
	}
}

// IdleFor reports how long the bot has sat in a voice channel without playing
// anything. It returns 0 while playing or when not connected.
func (p *Player) IdleFor() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.vc == nil || p.playing {
		return 0
	}
	return time.Since(p.idleSince)
}

// ResetIdle restarts the idle timer.
func (p *Player) ResetIdle() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.idleSince = time.Now()
}

// ChannelID returns the voice channel the bot is in, or "" if not connected.
func (p *Player) ChannelID() string {
	p.mu.Lock()
	vc := p.vc
	p.mu.Unlock()
	if vc == nil {
		return ""
	}
	vc.RLock()
	defer vc.RUnlock()
	return vc.ChannelID
}

func (p *Player) GetQueue() []*Song {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*Song, len(p.queue))
	copy(out, p.queue)
	return out
}

func (p *Player) Current() *Song {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.current
}

func (p *Player) IsPlaying() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.playing
}

func (p *Player) IsPaused() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.paused
}

func (p *Player) SetLoop(mode LoopMode) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.loopMode = mode
}

func (p *Player) GetLoop() LoopMode {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.loopMode
}

func (p *Player) SetVolume(vol int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if vol < 0 {
		vol = 0
	}
	if vol > 100 {
		vol = 100
	}
	p.volume = vol
}

func (p *Player) GetVolume() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.volume
}

// ── Internal playback ─────────────────────────────────────────────────────────

func (p *Player) playbackLoop() {
	p.mu.Lock()
	p.playing = true
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.playing = false
		p.current = nil
		p.paused = false
		p.idleSince = time.Now()
		p.mu.Unlock()
	}()

	for {
		p.mu.Lock()

		// Pick the next song according to loop mode
		var next *Song
		switch p.loopMode {
		case LoopOne:
			if p.current != nil {
				next = p.current
			} else if len(p.queue) > 0 {
				next = p.queue[0]
				p.queue = p.queue[1:]
			}
		case LoopQueue:
			if len(p.queue) > 0 {
				next = p.queue[0]
				p.queue = p.queue[1:]
				if p.current != nil {
					p.queue = append(p.queue, p.current)
				}
			}
		default:
			if len(p.queue) > 0 {
				next = p.queue[0]
				p.queue = p.queue[1:]
			}
		}

		if next == nil {
			p.mu.Unlock()
			return
		}

		p.current = next
		vc := p.vc
		vol := p.volume
		p.mu.Unlock()

		if vc == nil {
			return
		}

		if err := p.playTrack(vc, next, vol); err != nil {
			log.Printf("[Player] Playback finished for %q (%v)", next.Title, err)
		}

		// Drain any leftover skip signal from a completed track
		select {
		case <-p.skipCh:
		default:
		}
	}
}

func (p *Player) playTrack(vc *dgo.VoiceConnection, song *Song, vol int) error {
	if !waitVoiceReady(vc, 10*time.Second) {
		return errors.New("voice connection never became ready")
	}

	args := []string{"-hide_banner", "-loglevel", "error"}
	// Use the same headers yt-dlp used to resolve the URL, otherwise YouTube
	// may reject the request.
	if ua := song.Headers["User-Agent"]; ua != "" {
		args = append(args, "-user_agent", ua)
	}
	var hdrs strings.Builder
	for k, v := range song.Headers {
		if k != "User-Agent" {
			fmt.Fprintf(&hdrs, "%s: %s\r\n", k, v)
		}
	}
	if hdrs.Len() > 0 {
		args = append(args, "-headers", hdrs.String())
	}
	args = append(args,
		"-reconnect", "1",
		"-reconnect_streamed", "1",
		"-reconnect_delay_max", "5",
		"-i", song.StreamURL,
		"-map", "0:a",
	)
	// Only filter when needed; at 100% the audio is passed to the encoder untouched.
	if vol != 100 {
		args = append(args, "-af", fmt.Sprintf("volume=%.2f", float64(vol)/100.0))
	}
	args = append(args,
		"-acodec", "libopus",
		"-f", "ogg",
		"-ar", "48000",
		"-ac", "2",
		"-b:a", "160000", // YouTube's best Opus stream is ~130–160 kbps; more gains nothing
		"-vbr", "on",
		"-compression_level", "10",
		"-application", "audio",
		"-frame_duration", "20",
		"pipe:1",
	)
	cmd := exec.Command("ffmpeg", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout pipe error: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start error: %w", err)
	}

	p.mu.Lock()
	p.cmd = cmd
	p.mu.Unlock()

	defer func() {
		p.mu.Lock()
		p.cmd = nil
		p.mu.Unlock()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			log.Printf("[Player] ffmpeg: %s", msg)
		}
	}()

	_ = vc.Speaking(true)
	defer func() {
		_ = vc.Speaking(false)
	}()

	decoder := ogg.NewPacketDecoder(ogg.NewDecoder(stdout))

	// The first 2 packets in Ogg Opus are stream metadata (OpusHead, OpusTags).
	// We discard them and stream all subsequent packets (raw Opus audio frames).
	skipPackets := 2
	sent := 0

	for {
		select {
		case <-p.skipCh:
			return nil
		default:
		}

		p.mu.Lock()
		paused := p.paused
		p.mu.Unlock()

		if paused {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		packet, _, err := decoder.Decode()
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				if sent == 0 {
					return errors.New("ffmpeg produced no audio")
				}
				log.Printf("[Player] Sent %d packets for %q", sent, song.Title)
				return nil
			}
			return fmt.Errorf("ogg decode: %w", err)
		}

		if skipPackets > 0 {
			skipPackets--
			continue
		}

		vc.RLock()
		opusSend := vc.OpusSend
		vc.RUnlock()

		// If the voice connection's sender has died, OpusSend stops draining.
		// Time out instead of blocking forever while claiming to be playing.
		select {
		case opusSend <- packet:
			sent++
		case <-p.skipCh:
			return nil
		case <-time.After(5 * time.Second):
			p.mu.Lock()
			if p.vc == vc {
				p.vc = nil // force a rejoin on the next !play
			}
			p.mu.Unlock()
			return errors.New("voice connection stopped accepting audio")
		}
	}
}

func voiceReady(vc *dgo.VoiceConnection) bool {
	vc.RLock()
	defer vc.RUnlock()
	return vc.Ready && vc.OpusSend != nil
}

func waitVoiceReady(vc *dgo.VoiceConnection, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for !voiceReady(vc) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(100 * time.Millisecond)
	}
	return true
}
