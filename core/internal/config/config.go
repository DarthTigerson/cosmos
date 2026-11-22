// Package config holds the settings the user can change from the app.
package config

import (
	"cosmos/core/internal/mcp"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Launch describes a llama-server the core starts itself when the endpoint
// is not already answering.
type Launch struct {
	Binary      string `json:"binary"`
	ModelPath   string `json:"model_path"`
	MMProj      string `json:"mmproj,omitempty"`
	ContextSize int    `json:"context_size"`
	GPULayers   int    `json:"gpu_layers"`
}

// Endpoint is one OpenAI-compatible model server.
type Endpoint struct {
	BaseURL string  `json:"base_url"`
	Model   string  `json:"model"`
	APIKey  string  `json:"api_key,omitempty"`
	Launch  *Launch `json:"launch,omitempty"`
}

type Config struct {
	// Names are settings, never written into code or prompts: who the
	// user is, what the companion is called, and what the platform she
	// runs on is called.
	UserName      string `json:"user_name"`
	CompanionName string `json:"companion_name"`
	PlatformName  string `json:"platform_name"`
	Location      string `json:"location"`

	// Main is her voice: it writes replies and checks them against tool
	// results. Worker is the small fast model: it plans, feels, checks
	// for invented facts, saves memories and summarises. Sleeper reflects
	// at night. An empty Worker or Sleeper falls back to Main.
	Main    Endpoint `json:"main"`
	Worker  Endpoint `json:"worker"`
	Sleeper Endpoint `json:"sleeper"`
	// Embed is a small model that turns text into numbers, so memories
	// can be found by what they mean. Empty switches that off.
	Embed Endpoint `json:"embed"`

	// Planning: "auto" lets the main model pick per request, or force
	// "once" (whole plan up front) or "steps" (decide after each result).
	Planning        string `json:"planning"`
	VerifyWithModel bool   `json:"verify_with_model"`
	// VerifyClaims has the main model check each reply for things stated
	// about the user that they never said.
	VerifyClaims bool `json:"verify_claims"`

	QuietFrom        int  `json:"quiet_from"` // local hour, initiative stops
	QuietTo          int  `json:"quiet_to"`   // local hour, initiative resumes
	InitiativeBudget int  `json:"initiative_budget"`
	CooldownMinutes  int  `json:"cooldown_minutes"`
	BriefHour        int  `json:"brief_hour"`
	SleepHour        int  `json:"sleep_hour"`
	WeatherWatch     bool `json:"weather_watch"`
	// WebSearch lets her look things up online. It is the one thing that
	// sends words from a conversation to another computer.
	WebSearch bool `json:"web_search"`
	// Podcasts lets her check the shows she follows and listen to new
	// episodes on her own.
	Podcasts bool `json:"podcasts"`
	// Voice is the name of the system voice she speaks with. Empty
	// means the computer's own default.
	Voice string `json:"voice"`
	// HearModel is the Whisper model for what the user says aloud; empty
	// means the first in ~/models/whisper. HearLanguage is the language
	// they speak, which is quicker and surer than guessing it each time.
	HearModel    string `json:"hear_model"`
	HearLanguage string `json:"hear_language"`
	// When always listening she answers only to her name, or to
	// anything that sounds like it. NameSoundsLike adds spellings the
	// recogniser makes of it that are not caught on their own.
	NameSoundsLike []string `json:"name_sounds_like"`
	// ListenWindow is how many seconds after her reply she still takes
	// what is said as meant for her, without her name.
	ListenWindow int `json:"listen_window"`
	// HearPause is how many seconds of quiet end what the user is saying.
	// Shorter pauses, for a breath or a thought, are kept in it.
	HearPause float64 `json:"hear_pause"`
	// RoomMinutes is how long what she overhears is kept, in memory
	// only, for the reply she is next asked for. 0 keeps nothing.
	RoomMinutes int `json:"room_minutes"`
	// MainSees marks a main model server started elsewhere as able to
	// look at pictures. One the core starts sees when it has a projector.
	MainSees bool `json:"main_sees"`
	// How alike two prints must be. A face at FaceSure or more is that
	// person; at FaceMaybe she thinks so and asks; strangers at FaceSame
	// are taken to be one person. A voice at VoiceSure is that person's.
	FaceSure  float64 `json:"face_sure"`
	FaceMaybe float64 `json:"face_maybe"`
	FaceSame  float64 `json:"face_same"`
	VoiceSure float64 `json:"voice_sure"`
	// UnknownFaceDays is how long pictures of strangers are kept unasked
	// or unanswered.
	UnknownFaceDays int `json:"unknown_face_days"`
	// RememberFaces: faces the camera follows are kept and matched to
	// people. Off, she still looks but keeps nothing.
	RememberFaces bool `json:"remember_faces"`
	// AnswerOthers: when someone other than the user says her name, she
	// answers. Off, she listens and keeps it as what was said nearby.
	AnswerOthers bool `json:"answer_others"`
	// MCPServers are the user's own tool servers she may use.
	MCPServers []mcp.Server `json:"mcp_servers"`
}

func Default() Config {
	return Config{
		UserName:      "Friend",
		CompanionName: "Companion",
		PlatformName:  "Cosmos",
		Main:          Endpoint{BaseURL: "http://127.0.0.1:8080/v1", Model: "main"},
		Planning:      "auto",
		// Off by default: in practice the model rejected correct replies
		// far more often than it caught wrong ones. The code checks stay.
		VerifyWithModel:  false,
		VerifyClaims:     true,
		QuietFrom:        23,
		QuietTo:          7,
		InitiativeBudget: 6,
		CooldownMinutes:  45,
		BriefHour:        8,
		SleepHour:        2,
		WeatherWatch:     true,
		WebSearch:        true,
		Podcasts:         true,
		HearLanguage:     "en",
		ListenWindow:     25,
		HearPause:        0.9,
		RoomMinutes:      3,
		FaceSure:         0.45,
		FaceMaybe:        0.32,
		FaceSame:         0.40,
		VoiceSure:        0.62,
		UnknownFaceDays:  14,
		RememberFaces:    true,
	}
}

// Tier returns the endpoint for "main", "worker" or "sleeper", applying the
// fallback to Main.
func (c Config) Tier(name string) Endpoint {
	switch name {
	case "worker":
		if c.Worker.BaseURL != "" {
			return c.Worker
		}
	case "sleeper":
		if c.Sleeper.BaseURL != "" {
			return c.Sleeper
		}
	case "embed":
		// No fallback: a chat model cannot stand in for this one.
		return c.Embed
	}
	return c.Main
}

// Sees says whether the main model can look at pictures: it is started
// with a vision projector, or the user says the server they run has one.
func (c Config) Sees() bool {
	return c.MainSees || (c.Main.Launch != nil && c.Main.Launch.MMProj != "")
}

// Holder keeps the live config and saves changes to disk.
type Holder struct {
	mu   sync.RWMutex
	path string
	cfg  Config
}

// Load reads the config at path, creating it with defaults when missing.
// An empty path keeps the config in memory only.
func Load(path string) (*Holder, error) {
	h := &Holder{path: path, cfg: Default()}
	if path == "" {
		return h, nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return h, h.save()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &h.cfg); err != nil {
		return nil, err
	}
	return h, nil
}

func (h *Holder) Get() Config {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.cfg
}

func (h *Holder) Set(c Config) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cfg = c
	return h.save()
}

func (h *Holder) save() error {
	if h.path == "" {
		return nil
	}
	raw, err := json.MarshalIndent(h.cfg, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(h.path), 0o700); err != nil {
		return err
	}
	tmp := h.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, h.path)
}
