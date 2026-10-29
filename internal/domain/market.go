// Package domain defines the market model shared by the application.
package domain

import "time"

// Market is the normalized market shared by application rules and adapters.
type Market struct {
	ID          string `bson:"_id"`
	Slug        string `bson:"slug"`
	ConditionID string `bson:"condition_id"`
	EventSlug   string `bson:"event_slug,omitempty"`
	EventTitle  string `bson:"event_title,omitempty"`

	Question    string `bson:"question"`
	Description string `bson:"description"`

	Active          bool `bson:"active"`
	Closed          bool `bson:"closed"`
	Archived        bool `bson:"archived"`
	AcceptingOrders bool `bson:"accepting_orders"`

	// Closing and resolution: closed is the authoritative flag; resolution
	// (who won) comes later via the UMA oracle or automatically.
	ClosedTime            *time.Time `bson:"closed_time,omitempty"`
	UMAResolutionStatus   string     `bson:"uma_resolution_status,omitempty"` // "", "proposed", "resolved", ...
	AutomaticallyResolved bool       `bson:"automatically_resolved"`

	CreatedAt *time.Time `bson:"created_at,omitempty"`
	UpdatedAt *time.Time `bson:"updated_at,omitempty"`
	StartDate *time.Time `bson:"start_date,omitempty"`
	EndDate   *time.Time `bson:"end_date,omitempty"`

	Tags []string `bson:"tags"`

	Outcomes       []string  `bson:"outcomes"`
	OutcomePrices  []float64 `bson:"outcome_prices"`
	Volume         float64   `bson:"volume"`
	Volume24h      float64   `bson:"volume_24h"`
	Liquidity      float64   `bson:"liquidity"`
	LastTradePrice float64   `bson:"last_trade_price"`

	// SyncedAt is when the bot last wrote this document (set by the DB layer).
	SyncedAt time.Time `bson:"synced_at"`
}
