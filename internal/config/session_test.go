package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConsentMigration(t *testing.T) {
	for _, tc := range []struct {
		name, json      string
		consent, resume bool
	}{
		{"empty legacy", `{}`, false, false},
		{"partial legacy", `{"consentAccepted":true}`, false, false},
		{"storage only", `{"persistCredentials":true}`, false, false},
		{"approved legacy", `{"consentAccepted":true,"persistCredentials":true}`, true, false},
		{"legacy cannot invent intent", `{"consentAccepted":true,"persistCredentials":true,"connectionWanted":true}`, true, false},
		{"current resume", `{"schemaVersion":1,"consentVersion":1,"consentAccepted":true,"persistCredentials":true,"connectionWanted":true}`, true, true},
		{"explicit disconnect", `{"schemaVersion":1,"consentVersion":1,"consentAccepted":true,"persistCredentials":true,"connectionWanted":false}`, true, false},
		{"unversioned current approval", `{"schemaVersion":1,"consentAccepted":true,"persistCredentials":true,"connectionWanted":true}`, false, false},
		{"unknown scope", `{"schemaVersion":1,"consentVersion":2,"consentAccepted":true,"persistCredentials":true,"connectionWanted":true}`, false, false},
		{"subnet unapproved", `{"schemaVersion":1,"consentVersion":1,"consentAccepted":true,"persistCredentials":true,"connectionWanted":true,"advertiseRoutes":["10.1.0.0/16"]}`, true, false},
		{"subnet approved", `{"schemaVersion":1,"consentVersion":1,"consentAccepted":true,"persistCredentials":true,"connectionWanted":true,"advertiseRoutes":["10.1.0.0/16"],"subnetConsentAccepted":true}`, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "private")
			if err := os.Mkdir(dir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(tc.json), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.HasConsent() != tc.consent || cfg.CanResume() != tc.resume {
				t.Fatalf("unexpected migration: %+v", cfg)
			}
			if cfg.SchemaVersion != CurrentSchemaVersion {
				t.Fatal("schema not migrated")
			}
			if err := Save(dir, cfg); err != nil {
				t.Fatal(err)
			}
			again, err := Load(dir)
			if err != nil || again.HasConsent() != tc.consent || again.CanResume() != tc.resume {
				t.Fatalf("migration was not durable: %+v %v", again, err)
			}
		})
	}
}

func TestFutureSchemaFailsClosed(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"schemaVersion":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("future schema accepted")
	}
}
