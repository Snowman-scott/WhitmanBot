package main

import (
	"fmt"
	"log"
	"net/url"
	"path"
	"strings"

	"github.com/darui3018823/dgo"
	"resty.dev/v3"
)

type Track struct {
	ID          int    `json:"id"`
	TrackName   string `json:"trackName"`
	ArtistName  string `json:"artistName"`
	AlbumName   string `json:"albumName"`
	PlainLyrics string `json:"plainLyrics"`
}

func RegisterLyricsHandlers(sess *dgo.Session) {
	client := resty.New().
		SetBaseURL("https://lrclib.net").
		SetHeader("User-Agent", "Mozilla/5.0 (X11; Linux x86_64; rv:132.0) Gecko/20100101 Firefox/132.0")

	// Register the slash command
	sess.AddHandler(func(s *dgo.Session, r *dgo.Ready) {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, "", &dgo.ApplicationCommand{
			Name:        "lyrics",
			Description: "Search lyrics by name or paste an LRCLIB URL",
			Options: []*dgo.ApplicationCommandOption{
				{
					Type:        dgo.ApplicationCommandOptionString,
					Name:        "track",
					Description: "Track name, search query, or full lrclib.net track URL",
					Required:    true,
				},
				{
					Type:        dgo.ApplicationCommandOptionString,
					Name:        "artist",
					Description: "Artist name (optional if searching by title or URL)",
					Required:    false,
				},
			},
			IntegrationTypes: &[]dgo.ApplicationIntegrationType{
				dgo.ApplicationIntegrationGuildInstall,
				dgo.ApplicationIntegrationUserInstall,
			},
			Contexts: &[]dgo.InteractionContextType{
				dgo.InteractionContextGuild,
				dgo.InteractionContextBotDM,
				dgo.InteractionContextPrivateChannel,
			},
		})
		if err != nil {
			log.Printf("Cannot create lyrics slash command: %v", err)
		}
	})

	// Handle interactions
	sess.AddHandler(func(s *dgo.Session, i *dgo.InteractionCreate) {
		if i.Type != dgo.InteractionApplicationCommand {
			return
		}
		if i.ApplicationCommandData().Name == "lyrics" {
			err := s.InteractionRespond(i.Interaction, &dgo.InteractionResponse{
				Type: dgo.InteractionResponseDeferredChannelMessageWithSource,
			})
			if err != nil {
				log.Printf("Failed to defer response: %v", err)
				return
			}

			var trackInput, artistName string
			for _, opt := range i.ApplicationCommandData().Options {
				if opt.Name == "track" {
					trackInput = opt.StringValue()
				}
				if opt.Name == "artist" {
					artistName = opt.StringValue()
				}
			}

			var track Track

			// Check if the user pasted an LRCLIB track URL
			if strings.Contains(trackInput, "lrclib.net/tracks/") {
				u, parseErr := url.Parse(trackInput)
				if parseErr == nil {
					trackID := path.Base(u.Path)
					_, _ = client.R().
						SetResult(&track).
						Get(fmt.Sprintf("/api/get/%s", trackID))
				}
			}

			// If it wasn't a URL or direct fetch didn't return a track, fall back to keyword search
			if track.TrackName == "" {
				var tracks []Track
				_, err = client.R().
					SetQueryParams(map[string]string{
						"track_name":  trackInput,
						"artist_name": artistName,
					}).
					SetResult(&tracks).
					Get("/api/search")

				if err != nil || len(tracks) == 0 {
					_, _ = s.FollowupMessageCreate(i.Interaction, true, &dgo.WebhookParams{
						Content: "❌ No lyrics found for that query or URL.",
					})
					return
				}
				track = tracks[0]
			}

			lyrics := track.PlainLyrics
			if lyrics == "" {
				lyrics = "No plain lyrics available for this track."
			}

			responseContent := fmt.Sprintf("**%s** by %s\n*Album: %s*\n\n```\n%s\n```", track.TrackName, track.ArtistName, track.AlbumName, lyrics)

			// Safely check and truncate based on characters (runes), not bytes
			runes := []rune(responseContent)
			if len(runes) > 2000 {
				suffix := "\n```\n*(lyrics truncated)*"
				suffixRunes := []rune(suffix)
				allowedLength := 2000 - len(suffixRunes)
				if allowedLength < 0 {
					allowedLength = 0
				}
				responseContent = string(runes[:allowedLength]) + suffix
			}

			_, err = s.FollowupMessageCreate(i.Interaction, true, &dgo.WebhookParams{
				Content: responseContent,
			})
			if err != nil {
				log.Printf("Failed to send followup message: %v", err)
			}
		}
	})
}
