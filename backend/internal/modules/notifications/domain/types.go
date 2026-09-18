package domain

import (
	"errors"
	"time"
)

var ErrInvalidInput = errors.New("invalid notification input")
var ErrNotFound = errors.New("notification not found")
var ErrConflict = errors.New("notification operation conflicts with delivery state")

type Credentials struct {
	BotToken      string `json:"botToken,omitempty"`
	ChatID        string `json:"chatId,omitempty"`
	WebhookURL    string `json:"webhookUrl,omitempty"`
	SigningSecret string `json:"signingSecret,omitempty"`
}

type Channel struct {
	ID             string    `json:"id"`
	ProjectID      string    `json:"-"`
	Name           string    `json:"name"`
	Platform       string    `json:"platform"`
	Enabled        bool      `json:"enabled"`
	HasCredentials bool      `json:"hasCredentials"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
	Ciphertext     []byte    `json:"-"`
	Nonce          []byte    `json:"-"`
}

type ChannelInput struct {
	Name        string       `json:"name"`
	Platform    string       `json:"platform"`
	Enabled     bool         `json:"enabled"`
	Credentials *Credentials `json:"credentials,omitempty"`
}

type Event struct {
	IncidentID string
	Generation int64
	Kind       string
	RunID      string
	State      string
}

const (
	DeliveryPending   = "pending"
	DeliverySending   = "sending"
	DeliveryDelivered = "delivered"
	DeliveryFailed    = "failed"
	DeliveryCancelled = "cancelled"
)

type Delivery struct {
	ID             string     `json:"id"`
	ChannelID      string     `json:"channelId"`
	ChannelName    string     `json:"channelName"`
	Platform       string     `json:"platform"`
	Kind           string     `json:"kind"`
	IncidentNumber int64      `json:"incidentNumber"`
	Generation     int64      `json:"generation"`
	State          string     `json:"state"`
	Attempts       int        `json:"attempts"`
	LastError      string     `json:"lastError"`
	CreatedAt      time.Time  `json:"createdAt"`
	NextAttemptAt  time.Time  `json:"nextAttemptAt"`
	DeliveredAt    *time.Time `json:"deliveredAt"`
	ProjectID      string     `json:"-"`
	Ciphertext     []byte     `json:"-"`
	Nonce          []byte     `json:"-"`
	Message        string     `json:"-"`
	LeaseToken     string     `json:"-"`
}

func IsStoppingResult(state, mode string, analysisOnly bool) bool {
	switch state {
	case "diagnosis_ready_for_review":
		return mode != "auto_hotfix" || analysisOnly
	case "completed_non_code", "blocked_manual_review", "failed", "budget_exhausted", "awaiting_human_review":
		return true
	default:
		return false
	}
}
