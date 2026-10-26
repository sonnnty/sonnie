package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"

	"github.com/Sonty15/SONNIE/internal/bot"
	"github.com/Sonty15/SONNIE/internal/config"
)

func main() {
	_ = godotenv.Load()

	cfg, err := config.Load(os.Getenv)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	b, err := bot.New(cfg)
	if err != nil {
		log.Fatalf("bot: %v", err)
	}

	if err := b.Open(); err != nil {
		log.Fatalf("open: %v", err)
	}
	defer b.Close()

	log.Println("bot is running, press Ctrl+C to exit")

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	log.Println("shutting down")
}
