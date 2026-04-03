package main

import "time"

// Config holds lightweight process configuration for the backend service.
type Config struct {
	GameID       string
	PollInterval time.Duration
	OutputPath   string
}
