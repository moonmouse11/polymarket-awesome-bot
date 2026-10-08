package polymarket

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// Market is a Polymarket market as stored in MongoDB.
// To store another API field: add it to apiMarket (JSON name from the API),
// add it here (bson name in the DB) and copy it in toMarket.
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

// apiMarket mirrors the Gamma API JSON. Only the fields we store are listed.
type apiMarket struct {
	ID              string  `json:"id"`
	Slug            string  `json:"slug"`
	ConditionID     string  `json:"conditionId"`
	Question        string  `json:"question"`
	Description     string  `json:"description"`
	Active          bool    `json:"active"`
	Closed          bool    `json:"closed"`
	Archived        bool    `json:"archived"`
	AcceptingOrders bool    `json:"acceptingOrders"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
	StartDate       string  `json:"startDate"`
	EndDate         string  `json:"endDate"`
	Outcomes        string  `json:"outcomes"`      // JSON array encoded as a string
	OutcomePrices   string  `json:"outcomePrices"` // JSON array of numeric strings encoded as a string
	VolumeNum       float64 `json:"volumeNum"`
	Volume24hr      float64 `json:"volume24hr"`
	LiquidityNum    float64 `json:"liquidityNum"`
	LastTradePrice  float64 `json:"lastTradePrice"`
	Tags            []struct {
		Label string `json:"label"`
	} `json:"tags"`
	Events []struct {
		Slug  string `json:"slug"`
		Title string `json:"title"`
	} `json:"events"`
}

func (a apiMarket) toMarket() (Market, error) {
	m := Market{
		ID:              a.ID,
		Slug:            a.Slug,
		ConditionID:     a.ConditionID,
		Question:        a.Question,
		Description:     a.Description,
		Active:          a.Active,
		Closed:          a.Closed,
		Archived:        a.Archived,
		AcceptingOrders: a.AcceptingOrders,
		CreatedAt:       parseTime(a.CreatedAt),
		UpdatedAt:       parseTime(a.UpdatedAt),
		StartDate:       parseTime(a.StartDate),
		EndDate:         parseTime(a.EndDate),
		Volume:          a.VolumeNum,
		Volume24h:       a.Volume24hr,
		Liquidity:       a.LiquidityNum,
		LastTradePrice:  a.LastTradePrice,
		Tags:            make([]string, 0, len(a.Tags)),
	}
	for _, t := range a.Tags {
		m.Tags = append(m.Tags, t.Label)
	}
	if len(a.Events) > 0 {
		m.EventSlug = a.Events[0].Slug
		m.EventTitle = a.Events[0].Title
	}

	if a.Outcomes != "" {
		if err := json.Unmarshal([]byte(a.Outcomes), &m.Outcomes); err != nil {
			return Market{}, fmt.Errorf("market %s: parse outcomes: %w", a.ID, err)
		}
	}
	if a.OutcomePrices != "" {
		var prices []string
		if err := json.Unmarshal([]byte(a.OutcomePrices), &prices); err != nil {
			return Market{}, fmt.Errorf("market %s: parse outcomePrices: %w", a.ID, err)
		}
		m.OutcomePrices = make([]float64, 0, len(prices))
		for _, p := range prices {
			f, err := strconv.ParseFloat(p, 64)
			if err != nil {
				return Market{}, fmt.Errorf("market %s: parse price %q: %w", a.ID, p, err)
			}
			m.OutcomePrices = append(m.OutcomePrices, f)
		}
	}
	return m, nil
}

// parseTime returns nil for empty or unparseable timestamps: dates are
// informational and a bad one should not drop the whole market.
func parseTime(s string) *time.Time {
	if s == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &t
}
