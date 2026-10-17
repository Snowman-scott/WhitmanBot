package main

import (
	"bytes"
	_ "embed"
	"log"

	"github.com/darui3018823/dgo"
)

// Embedded so the command works no matter which directory the bot runs from.
//
//go:embed assets/james.png
var whitmanImage []byte

func RegisterWhitmanHandlers(sess *dgo.Session) {
	// Register the command when the bot is ready
	sess.AddHandler(func(s *dgo.Session, r *dgo.Ready) {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, "", &dgo.ApplicationCommand{
			Name:        "whitman",
			Description: "Displays the Whitman image",

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
			log.Fatalf("Cannot create slash command: %v", err)
		}
	})

	// Handle the interaction response
	sess.AddHandler(func(s *dgo.Session, i *dgo.InteractionCreate) {
		if i.Type != dgo.InteractionApplicationCommand {
			return
		}
		if i.ApplicationCommandData().Name == "whitman" {
			err := s.InteractionRespond(i.Interaction, &dgo.InteractionResponse{
				Type: dgo.InteractionResponseChannelMessageWithSource,
				Data: &dgo.InteractionResponseData{
					Files: []*dgo.File{
						{
							Name:        "whitman.png",
							ContentType: "image/png",
							Reader:      bytes.NewReader(whitmanImage),
						},
					},
				},
			})
			if err != nil {
				log.Printf("Failed to respond: %v", err)
			}
		}
	})
}
