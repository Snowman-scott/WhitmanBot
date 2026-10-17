# ── Build ─────────────────────────────────────────────────────────────────────
# Compiles the bot. Only the finished binary is copied into the final image;
# the source, assets/ and memes.txt are baked into that binary with go:embed.
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /whitmanBot .

# ── Runtime ───────────────────────────────────────────────────────────────────
FROM alpine:latest

# ffmpeg encodes the audio; yt-dlp (Python) finds and fetches it; deno is the
# JavaScript runtime yt-dlp needs to get around YouTube's player challenges.
RUN apk add --no-cache ffmpeg python3 deno ca-certificates tini

RUN adduser -D -u 1000 bot \
 && python3 -m venv /opt/yt-dlp \
 && /opt/yt-dlp/bin/pip install --no-cache-dir "yt-dlp[default]" \
 && chown -R bot:bot /opt/yt-dlp \
 && mkdir /data && chown bot:bot /data
ENV PATH="/opt/yt-dlp/bin:${PATH}"

# The meme list edited with !meme add/remove is saved here. Mount a volume on
# /data so it survives container rebuilds.
ENV MEME_FILE=/data/memes.txt
# Set to false to stop the bot playing memes after 1 minute idle in voice.
ENV MEME_AUTOPLAY=true
VOLUME /data

COPY --from=build /whitmanBot /usr/local/bin/whitmanBot
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh

USER bot
WORKDIR /home/bot
# BOT_TOKEN must be supplied at runtime (see compose.yaml).
ENTRYPOINT ["/sbin/tini", "--", "docker-entrypoint.sh"]
