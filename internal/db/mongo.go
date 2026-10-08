package db

import (
	"context"
	"fmt"

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

func (m *MongoDB) SaveMarketEvent(ctx context.Context, event interface{}) error {
	collection := m.db.Collection("awesome_markets")
	_, err := collection.InsertOne(ctx, event)
	return err
}

func (m *MongoDB) Close(ctx context.Context) error {
	return m.client.Disconnect(ctx)
}
