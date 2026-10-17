package main

import (
	"flag"
	"log"
	"os"
	"os/signal"

	"github.com/darui3018823/dgo"
	"github.com/joho/godotenv"
)

var Token = flag.String("token", "", "Discord Bot Token")

func main() {
	_ = godotenv.Load()
	flag.Parse()

	tokenVal := *Token
	if tokenVal == "" {
		tokenVal = os.Getenv("BOT_TOKEN")
	}
	if tokenVal == "" {
		log.Fatal("Provide a token via -token flag or BOT_TOKEN in .env")
	}

	sess, err := dgo.NewBot(tokenVal)
	if err != nil {
		log.Fatalf("Error creating Discord session: %v", err)
	}

	sess.AddHandler(func(s *dgo.Session, r *dgo.Ready) {
		log.Printf("✅ Logged in as: %s#%s", s.State.User.Username, s.State.User.Discriminator)
	})

	// Register all command groups
	RegisterWhitmanHandlers(sess)
	RegisterLyricsHandlers(sess)
	RegisterMusicHandlers(sess)
	StartMemeWatcher(sess)

	// Gateway Intents
	sess.Identify.Intents = dgo.IntentsGuilds |
		dgo.IntentsGuildMessages |
		dgo.IntentsMessageContent |
		dgo.IntentsGuildVoiceStates

	if err = sess.Open(); err != nil {
		log.Fatalf("Error opening connection: %v", err)
	}
	defer sess.Close()

	log.Println("Bot is running. Press CTRL-C to exit.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	log.Println("Shutting down.")
}
