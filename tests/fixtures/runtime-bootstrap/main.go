package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	runtime "github.com/Liapoldus/core/internal/runtime"
)

func main() {
	words, err := config.LoadRuntime()
	if err != nil {
		fail(err)
	}
	path, err := runtime.StatePath()
	if err != nil {
		fail(err)
	}
	if err := runtime.EnsureInitialized(path); err != nil {
		fail(err)
	}
	database, unlock, err := runtime.OpenExclusiveDatabase(context.Background(), path)
	if err != nil {
		fail(err)
	}
	defer unlock()
	defer database.Close()
	keys, err := storage.NewSQLiteServiceKeyStore(database)
	if err != nil {
		fail(err)
	}
	secret := make([]byte, words.ServiceKey.KeyBytes)
	if _, err := rand.Read(secret); err != nil {
		fail(err)
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	clear(secret)
	verifier, err := security.HashServiceKey(token, words.ServiceKey.HashCost)
	if err != nil {
		fail(err)
	}
	identifier := make([]byte, words.ServiceKey.KeyBytes)
	if _, err := rand.Read(identifier); err != nil {
		fail(err)
	}
	keyID := hex.EncodeToString(identifier)
	clear(identifier)
	if err := (application.AccessService{Store: keys}).Bootstrap(context.Background(), keyID, verifier, words.ServiceKey.RolePlatformAdmin); err != nil {
		fail(err)
	}
	fmt.Println(token)
}

func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
