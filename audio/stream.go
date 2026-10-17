package audio

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// stream is a running `yt-dlp | ffmpeg` pipeline producing Ogg Opus on out.
//
// yt-dlp does the downloading because it fetches YouTube in small chunks with
// retries and gets a fresh stream URL when the track starts. Handing ffmpeg
// the raw URL instead holds one HTTP connection open for the whole track,
// which YouTube often drops partway through long videos.
//
// Audio flows through OS pipes, so memory use stays flat however long the
// track is: when playback falls behind, the pipes fill and both processes
// simply wait.
type stream struct {
	ytdlp, ffmpeg *exec.Cmd
	out           io.ReadCloser

	ytdlpErr, ffmpegErr *tailBuffer
}

func startStream(song *Song, vol int) (*stream, error) {
	if song.YoutubeURL == "" {
		return nil, errors.New("song has no URL to stream")
	}

	st := &stream{ytdlpErr: &tailBuffer{}, ffmpegErr: &tailBuffer{}}

	st.ytdlp = exec.Command(
		"yt-dlp",
		"--format", audioFormat,
		"--no-playlist",
		"--quiet",
		"--no-warnings",
		"--no-progress",
		// ~2 minutes of audio per request, so no connection sits idle for long
		"--http-chunk-size", "2M",
		"--retries", "10",
		"--fragment-retries", "10",
		"--output", "-",
		"--", song.YoutubeURL,
	)
	st.ytdlp.Stderr = st.ytdlpErr

	// YouTube's audio often peaks several dB above full scale, which Discord
	// clips into a crushed sound. Like YouTube's own player, turn it down
	// rather than squashing it: a fixed -6 dB keeps the dynamics intact, and
	// the limiter is only a safety net for the rare peaks still over -1 dBFS.
	filter := fmt.Sprintf("volume=%.3f,alimiter=limit=0.891:attack=5:release=50:level=false",
		0.5*float64(vol)/100.0)
	args := []string{"-hide_banner", "-loglevel", "error", "-i", "pipe:0", "-map", "0:a", "-af", filter}
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
	st.ffmpeg = exec.Command("ffmpeg", args...)
	st.ffmpeg.Stderr = st.ffmpegErr

	r, w, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	st.ytdlp.Stdout = w
	st.ffmpeg.Stdin = r

	if st.out, err = st.ffmpeg.StdoutPipe(); err != nil {
		r.Close()
		w.Close()
		return nil, err
	}

	if err := st.ytdlp.Start(); err != nil {
		r.Close()
		w.Close()
		return nil, fmt.Errorf("yt-dlp start: %w", err)
	}
	if err := st.ffmpeg.Start(); err != nil {
		r.Close()
		w.Close()
		_ = st.ytdlp.Process.Kill()
		_ = st.ytdlp.Wait()
		return nil, fmt.Errorf("ffmpeg start: %w", err)
	}
	// The children have their own copies now. Closing ours lets ffmpeg see
	// EOF when yt-dlp finishes, and lets yt-dlp stop if ffmpeg dies.
	r.Close()
	w.Close()

	return st, nil
}

// kill stops both processes immediately. Safe to call on nil or more than once.
func (st *stream) kill() {
	if st == nil {
		return
	}
	for _, c := range []*exec.Cmd{st.ytdlp, st.ffmpeg} {
		if c.Process != nil {
			_ = c.Process.Kill()
		}
	}
}

// close stops the pipeline, reaps both processes and logs any errors they printed.
func (st *stream) close() {
	st.kill()
	_ = st.ytdlp.Wait()
	_ = st.ffmpeg.Wait()
	if msg := st.ytdlpErr.String(); msg != "" {
		log.Printf("[Player] yt-dlp: %s", msg)
	}
	if msg := st.ffmpegErr.String(); msg != "" {
		log.Printf("[Player] ffmpeg: %s", msg)
	}
}

// tailBuffer keeps only the last few KB written to it, so a chatty process
// can't grow memory during a long track.
type tailBuffer struct {
	mu  sync.Mutex
	buf []byte
}

const tailBufferMax = 4096

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.buf = append(t.buf, p...)
	if len(t.buf) > tailBufferMax {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-tailBufferMax:]...)
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.buf))
}
