package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// mongoStore implémente Store pour MongoDB. Pas de schéma : la collection
// canary_hits naît à la première insertion.
type mongoStore struct {
	target DBTarget
	client *mongo.Client
	hits   *mongo.Collection
}

// openMongo prépare le client. mongo.Connect ne contacte pas le serveur : la
// connexion se fait au premier appel, comme pour les moteurs SQL.
func openMongo(t DBTarget) (*mongoStore, error) {
	client, err := mongo.Connect(options.Client().
		ApplyURI(t.URL()).
		SetServerSelectionTimeout(StepTimeout).
		SetConnectTimeout(StepTimeout))
	if err != nil {
		return nil, err
	}
	return &mongoStore{target: t, client: client, hits: client.Database(t.Database).Collection("canary_hits")}, nil
}

func (s *mongoStore) Engine() string { return EngineMongo }

func (s *mongoStore) Ping(ctx context.Context) (string, error) {
	if err := s.client.Ping(ctx, nil); err != nil {
		return "", err
	}
	var info struct {
		Version string `bson:"version"`
	}
	err := s.client.Database(s.target.Database).RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&info)
	return "MongoDB " + info.Version, err
}

func (s *mongoStore) EnsureSchema(context.Context) error { return nil }

func (s *mongoStore) Write(ctx context.Context, pod string) error {
	_, err := s.hits.InsertOne(ctx, bson.D{{Key: "pod", Value: pod}, {Key: "createdAt", Value: time.Now()}})
	return err
}

func (s *mongoStore) Count(ctx context.Context) (int64, time.Time, error) {
	n, err := s.hits.CountDocuments(ctx, bson.D{})
	if err != nil {
		return 0, time.Time{}, err
	}
	var last struct {
		CreatedAt time.Time `bson:"createdAt"`
	}
	err = s.hits.FindOne(ctx, bson.D{}, options.FindOne().SetSort(bson.D{{Key: "createdAt", Value: -1}})).Decode(&last)
	if errors.Is(err, mongo.ErrNoDocuments) {
		err = nil
	}
	return n, last.CreatedAt, err
}

// TryCredentials ouvre un client à part avec ces identifiants et tente quatre
// opérations de droits croissants. La DDL de MongoDB, c'est créer et
// supprimer une collection.
func (s *mongoStore) TryCredentials(ctx context.Context, user, password string) CredentialReport {
	other, err := openMongo(s.target.WithCredentials(user, password))
	if err != nil {
		return CredentialReport{Connect: stepOf(err), Read: notTried, Write: notTried, DDL: notTried}
	}
	defer other.Close()

	step := func(f func(context.Context) error) StepResult {
		c, cancel := context.WithTimeout(ctx, StepTimeout)
		defer cancel()
		return stepOf(f(c))
	}

	r := CredentialReport{Connect: step(func(c context.Context) error { return other.client.Ping(c, nil) })}
	if !r.Connect.OK {
		r.Read, r.Write, r.DDL = notTried, notTried, notTried
		return r
	}
	r.Read = step(func(c context.Context) error {
		_, err := other.hits.CountDocuments(c, bson.D{})
		return err
	})
	r.Write = step(func(c context.Context) error { return other.Write(c, "credentials-test") })
	r.DDL = step(func(c context.Context) error {
		name := fmt.Sprintf("canary_ddl_probe_%d", time.Now().UnixNano()%1_000_000_000)
		db := other.client.Database(s.target.Database)
		if err := db.CreateCollection(c, name); err != nil {
			return err
		}
		return db.Collection(name).Drop(c)
	})
	return r
}

// IsAuthError reconnaît un refus d'authentification : code serveur 18
// (AuthenticationFailed), ou erreur de poignée de main qui le mentionne.
func (s *mongoStore) IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	var cmdErr mongo.CommandError
	if errors.As(err, &cmdErr) && cmdErr.Code == 18 {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "AuthenticationFailed") || strings.Contains(msg, "auth error")
}

func (s *mongoStore) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), StepTimeout)
	defer cancel()
	return s.client.Disconnect(ctx)
}
