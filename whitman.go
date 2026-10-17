package main

import (
	"log"
	"os"

	"github.com/bwmarrin/discordgo"
)

func RegisterWhitmanHandlers(sess *discordgo.Session) {
	// Register the command when the bot is ready
	sess.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		_, err := s.ApplicationCommandCreate(s.State.User.ID, "", &discordgo.ApplicationCommand{
			Name:        "whitman",
			Description: "Displays the Whitman image",

			IntegrationTypes: &[]discordgo.ApplicationIntegrationType{
				discordgo.ApplicationIntegrationGuildInstall,
				discordgo.ApplicationIntegrationUserInstall,
			},

			Contexts: &[]discordgo.InteractionContextType{
				discordgo.InteractionContextGuild,
				discordgo.InteractionContextBotDM,
				discordgo.InteractionContextPrivateChannel,
			},
		})
		if err != nil {
			log.Fatalf("Cannot create slash command: %v", err)
		}
	})

	// Handle the interaction response
	sess.AddHandler(func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		if i.ApplicationCommandData().Name == "whitman" {
			file, err := os.Open("assets/james.png")
			if err != nil {
				log.Printf("Failed to open image: %v", err)
				return
			}
			defer file.Close()

			err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseChannelMessageWithSource,
				Data: &discordgo.InteractionResponseData{
					Files: []*discordgo.File{
						{
							Name:   "whitman.png",
							Reader: file,
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
