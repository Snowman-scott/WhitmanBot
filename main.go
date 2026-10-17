package main

import (
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/bwmarrin/discordgo"
	"github.com/joho/godotenv"
)

var Token = flag.String("token", "", "Discord Bot Token")

func main() {
	// Load the .env file if it exists in the current directory
	_ = godotenv.Load()

	flag.Parse()

	// Use the flag value if provided, otherwise fall back to BOT_TOKEN from .env
	tokenVal := *Token
	if tokenVal == "" {
		tokenVal = os.Getenv("BOT_TOKEN")
	}

	if tokenVal == "" {
		log.Fatal("Please provide your bot token via the -token flag or set BOT_TOKEN in your .env file")
	}

	sess, err := discordgo.New("Bot " + tokenVal)
	if err != nil {
		log.Fatalf("Error creating Discord session: %v", err)
	}

	sess.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("Logged in as: %s", s.State.User.Username)
	})

	// Register your command handlers from modular files
	RegisterWhitmanHandlers(sess)
	RegisterLyricsHandlers(sess)

	err = sess.Open()
	if err != nil {
		log.Fatalf("Error opening connection: %v", err)
	}
	defer sess.Close()

	log.Println("App backend is running. Press CTRL-C to exit.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
}
