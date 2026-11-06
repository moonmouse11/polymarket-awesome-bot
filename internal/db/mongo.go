package db

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type MongoDB struct {
	client *mongo.Client
	db     *mongo.Database
}

func NewMongoDB(ctx context.Context, uri, dbName string) (*MongoDB, error) {
	clientOptions := options.Client().ApplyURI(uri)

	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to mongo: %w", err)
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to ping mongo: %w", err)
	}

	return &MongoDB{
		client: client,
		db:     client.Database(dbName),
	}, nil
}

// Connect opens MongoDB with a 10-second setup timeout. The timeout covers
// only connect+ping; the returned handle lives until Close.
func Connect(ctx context.Context, uri, dbName string) (*MongoDB, error) {
	dbCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return NewMongoDB(dbCtx, uri, dbName)
}

// CloseWithTimeout closes with a fresh 5-second context: at shutdown the
// caller's ctx is usually already canceled.
func (m *MongoDB) CloseWithTimeout() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = m.Close(ctx)
}

func (m *MongoDB) SaveMarketEvent(ctx context.Context, event interface{}) error {
	collection := m.db.Collection("awesome_markets")
	_, err := collection.InsertOne(ctx, event)
	return err
}

func (m *MongoDB) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}
