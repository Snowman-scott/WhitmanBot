package audio

import (
	"errors"
	"fmt"
	"io"
	"log"
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

	vc     *dgo.VoiceConnection
	stream *stream // yt-dlp | ffmpeg for the current track

	skipCh chan struct{}

	playing  bool
	paused   bool
	loopMode LoopMode
	volume   int // 0–100

	idleSince time.Time // when playback last stopped (or the bot joined)
	stopped   bool      // Stop was called and nothing has been queued since

	// OnQueueEnd, if set, runs in its own goroutine when the queue runs out
	// on its own: not after Stop, and not when the voice connection failed.
	OnQueueEnd func()
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

	var err error
	for attempt := 1; attempt <= 2; attempt++ {
		// mute=false, deaf=true (self-deafen)
		var vc *dgo.VoiceConnection
		vc, err = p.sess.ChannelVoiceJoin(p.guildID, channelID, false, true)
		if err == nil {
			p.vc = vc
			p.idleSince = time.Now()
			return nil
		}
		log.Printf("[Player] Voice join attempt %d in guild %s failed: %v", attempt, p.guildID, err)
		// A failed join can leave a half-open connection behind that makes
		// every later join fail too. Tear it down so the retry starts clean.
		p.dropVoiceLocked()
	}
	return err
}

// dropVoiceLocked disconnects whatever voice connection the session holds
// for this guild. p.mu must be held.
func (p *Player) dropVoiceLocked() {
	p.sess.RLock()
	vc := p.sess.VoiceConnections[p.guildID]
	p.sess.RUnlock()
	if vc != nil {
		_ = vc.Disconnect()
	}
	p.vc = nil
}

// Enqueue adds a song and starts playback if nothing is running.
// Returns the 1-based queue position, or 0 if it will play immediately.
func (p *Player) Enqueue(song *Song) int {
	p.mu.Lock()
	p.stopped = false
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
	st := p.stream
	p.mu.Unlock()
	st.kill()

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
	p.stopped = true
	p.mu.Unlock()

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

// WasStopped reports whether Stop was called with nothing queued since.
func (p *Player) WasStopped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stopped
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

	ranOut := false // the queue emptied after a track played without error
	defer func() {
		p.mu.Lock()
		p.playing = false
		p.current = nil
		p.paused = false
		p.idleSince = time.Now()
		autoplay := ranOut && !p.stopped && p.OnQueueEnd != nil
		p.mu.Unlock()
		if autoplay {
			go p.OnQueueEnd()
		}
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
		ranOut = false

		p.current = next
		vc := p.vc
		vol := p.volume
		p.mu.Unlock()

		if vc == nil {
			return
		}

		if err := p.playTrack(vc, next, vol); err != nil {
			log.Printf("[Player] Playback finished for %q (%v)", next.Title, err)
		} else {
			ranOut = true
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
		p.mu.Lock()
		if p.vc == vc {
			p.vc = nil // force a rejoin on the next !play
		}
		p.mu.Unlock()
		return errors.New("voice connection never became ready")
	}

	st, err := startStream(song, vol)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.stream = st
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.stream = nil
		p.mu.Unlock()
		st.close()
	}()

	_ = vc.Speaking(true)
	defer func() {
		sendSilence(vc)
		_ = vc.Speaking(false)
	}()

	buf, readErr, stopReader := bufferPackets(st.out)
	defer stopReader()

	// Start in "buffering" mode: send silence until a cushion of audio is
	// ready. This keeps Discord's playback clock steady instead of trickling
	// packets out unevenly, which clients hear as speeding up and slowing down.
	buffering := true
	bufferingSince := time.Now()
	wasPaused := false
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
			// Packets keep queuing in buf (up to its cap), so resuming is instant.
			if !wasPaused {
				sendSilence(vc)
				_ = vc.Speaking(false)
				wasPaused = true
			}
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if wasPaused {
			_ = vc.Speaking(true)
			wasPaused = false
		}

		var packet []byte
		if buffering {
			if len(buf) >= prebufferPackets || readerDone(readErr) {
				buffering = false
				continue
			}
			if time.Since(bufferingSince) > 60*time.Second {
				return errors.New("timed out waiting for audio")
			}
			packet = silenceFrame
		} else {
			select {
			case pkt, ok := <-buf:
				if !ok {
					if err := <-readErr; err != nil {
						return err
					}
					if sent == 0 {
						return errors.New("ffmpeg produced no audio")
					}
					log.Printf("[Player] Sent %d packets for %q", sent, song.Title)
					return nil
				}
				packet = pkt
			default:
				// Ran dry (download hiccup): rebuild the cushion rather than
				// sending audio in uneven bursts.
				buffering = true
				bufferingSince = time.Now()
				continue
			}
		}

		vc.RLock()
		opusSend := vc.OpusSend
		vc.RUnlock()

		// If the voice connection's sender has died, OpusSend stops draining.
		// Time out instead of blocking forever while claiming to be playing.
		select {
		case opusSend <- packet:
			if !buffering {
				sent++
			}
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

const (
	frameDuration    = 20 * time.Millisecond
	prebufferPackets = int(time.Second / frameDuration)     // audio held before playing
	maxBufferPackets = int(5 * time.Second / frameDuration) // ~300 KB worst case
)

// silenceFrame is an Opus frame of silence.
var silenceFrame = []byte{0xF8, 0xFF, 0xFE}

// sendSilence sends the 5 silent frames Discord expects when audio stops, so
// clients don't interpolate the last real frame into a glitch.
func sendSilence(vc *dgo.VoiceConnection) {
	vc.RLock()
	opusSend := vc.OpusSend
	vc.RUnlock()
	for range 5 {
		select {
		case opusSend <- silenceFrame:
		case <-time.After(time.Second):
			return
		}
	}
}

// bufferPackets reads Opus packets from an Ogg stream into a bounded channel
// in the background. The channel is closed at end of stream, after which
// readErr yields nil (clean end) or the read error. stop ends the reader early.
func bufferPackets(r io.Reader) (buf chan []byte, readErr chan error, stop func()) {
	buf = make(chan []byte, maxBufferPackets)
	readErr = make(chan error, 1)
	done := make(chan struct{})

	go func() {
		defer close(buf)
		decoder := ogg.NewPacketDecoder(ogg.NewDecoder(r))
		// The first 2 packets in Ogg Opus are stream metadata (OpusHead, OpusTags).
		for skip := 2; ; skip-- {
			packet, _, err := decoder.Decode()
			if err != nil {
				if err == io.EOF || err == io.ErrUnexpectedEOF {
					err = nil
				} else {
					err = fmt.Errorf("ogg decode: %w", err)
				}
				readErr <- err
				return
			}
			if skip > 0 {
				continue
			}
			select {
			case buf <- packet:
			case <-done:
				readErr <- nil
				return
			}
		}
	}()

	var once sync.Once
	return buf, readErr, func() { once.Do(func() { close(done) }) }
}

// readerDone reports whether the reader has finished, without consuming its
// result if it hasn't.
func readerDone(readErr chan error) bool {
	select {
	case err := <-readErr:
		readErr <- err // put it back for whoever handles the end of stream
		return true
	default:
		return false
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
