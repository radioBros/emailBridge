package crypto

import (
	"context"
	"errors"
	"fmt"

	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"

	"github.com/radiobros/emailbridge/internal/app"
	"github.com/radiobros/emailbridge/internal/store"
)

// ProviderOptions select and configure the KEK provider (EB_KEYS_*, S9 §2.4).
type ProviderOptions struct {
	Provider        string // local | vault | awskms
	Local           []LocalKey
	Active          string
	VaultAddr       string
	VaultToken      string
	VaultTransitKey string
	VaultNamespace  string
	AWSKMSKeyID     string
	AWSRegion       string
}

// NewProvider builds the configured KEK provider.
func NewProvider(ctx context.Context, o ProviderOptions) (KEKProvider, error) {
	switch o.Provider {
	case "local", "":
		return NewLocal(o.Local, o.Active)
	case "vault":
		if o.VaultAddr == "" || o.VaultToken == "" || o.VaultTransitKey == "" {
			return nil, errors.New("crypto: vault provider needs EB_VAULT_ADDR, EB_VAULT_TOKEN and EB_VAULT_TRANSIT_KEY")
		}
		return &Vault{Addr: o.VaultAddr, Token: o.VaultToken, Key: o.VaultTransitKey, Namespace: o.VaultNamespace}, nil
	case "awskms":
		if o.AWSKMSKeyID == "" {
			return nil, errors.New("crypto: awskms provider needs EB_AWS_KMS_KEY_ID")
		}
		var opts []func(*awsconfig.LoadOptions) error
		if o.AWSRegion != "" {
			opts = append(opts, awsconfig.WithRegion(o.AWSRegion))
		}
		cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("crypto: aws config: %w", err)
		}
		return &KMS{Client: kms.NewFromConfig(cfg), KeyID: o.AWSKMSKeyID}, nil
	}
	return nil, fmt.Errorf("crypto: unknown key provider %q", o.Provider)
}

// Migration creates the dataKeys indexes (registered by the binary's migration list).
func Migration(number int) store.Migration {
	return store.Migration{Number: number, Name: "dataKeys indexes", Up: func(ctx context.Context, db *store.DB) error {
		return db.EnsureIndexes(ctx, DataKeysCollection, (&MongoKeyStore{DB: db}).Indexes()...)
	}}
}

// Module builds the Cipher (needs *store.DB) and provides *Cipher.
type Module struct {
	Provider ProviderOptions
	cipher   *Cipher
}

// Name implements app.Module.
func (m *Module) Name() string { return "crypto" }

// Roles implements app.Module (every node may decrypt credentials).
func (m *Module) Roles() []app.Role { return nil }

// Start implements app.Module.
func (m *Module) Start(ctx context.Context, a *app.App) error {
	db, err := app.MustGet[*store.DB](a)
	if err != nil {
		return err
	}
	kek, err := NewProvider(ctx, m.Provider)
	if err != nil {
		return err
	}
	m.cipher = &Cipher{KEK: kek, Store: &MongoKeyStore{DB: db}, NewID: func() string { return store.NewID(store.PrefixDataKey) }}
	app.Provide(a, m.cipher)
	return nil
}

// Stop implements app.Module.
func (m *Module) Stop(context.Context) error { return nil }

// Ready checks that the KEK provider can unwrap an existing key (when there is one).
func (m *Module) Ready(ctx context.Context) error {
	if m.cipher == nil {
		return errors.New("crypto: not started")
	}
	ks, ok := m.cipher.Store.(*MongoKeyStore)
	if !ok {
		return nil
	}
	var dk DataKey
	err := ks.DB.Collection(DataKeysCollection).FindOne(ctx, bson.M{"status": DEKActive}, options.FindOne()).Decode(&dk)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = m.cipher.key(ctx, dk.ID)
	return err
}
