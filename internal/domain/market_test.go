package domain

import (
	"reflect"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/bson"
)

func TestMarketBSONRoundTrip(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	want := Market{ID: "m", ConditionID: "c", EventSlug: "event", Question: "Question?", CreatedAt: &now, Tags: []string{"Science"}, OutcomePrices: []float64{0.25, 0.75}, SyncedAt: now}
	data, err := bson.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var fields bson.M
	if err := bson.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"_id", "condition_id", "event_slug", "outcome_prices", "synced_at"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("missing BSON field %q", key)
		}
	}
	for _, key := range []string{"closed_time", "updated_at", "start_date", "end_date"} {
		if _, ok := fields[key]; ok {
			t.Errorf("nil optional field %q must be omitted", key)
		}
	}
	var got Market
	if err := bson.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip: got %+v, want %+v", got, want)
	}
}

func TestMarketBSONMinimalDocument(t *testing.T) {
	data, err := bson.Marshal(bson.M{"_id": "old", "question": "Legacy?"})
	if err != nil {
		t.Fatal(err)
	}
	var got Market
	if err := bson.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	want := Market{ID: "old", Question: "Legacy?"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("minimal document: got %+v, want %+v", got, want)
	}
}
