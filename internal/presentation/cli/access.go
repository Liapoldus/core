package cli

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/Liapoldus/core/internal/application"
	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/config"
	"github.com/Liapoldus/core/internal/infrastructure/security"
	"github.com/Liapoldus/core/internal/infrastructure/storage"
	bootstrapruntime "github.com/Liapoldus/core/internal/presentation/cli/bootstrap"
)

func access(options options) int {
	if len(options.command) != 2 || options.command[1] != words.Access.Bootstrap {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.CommandExpected)
		return words.Exits.Arguments
	}
	path, _, err := discoverConfig(options)
	if err != nil {
		writeFailure(options.output, words.Exits.Arguments, words.Codes.ConfigNotFound, words.Diagnostics.ConfigNotFound)
		return words.Exits.Arguments
	}
	bootstrap, err := config.LoadBootstrap(path)
	if err != nil {
		return configValidationFailure(options.output, err)
	}
	database, err := bootstrapruntime.OpenDatabase(context.Background(), bootstrap.StatePath)
	if err != nil {
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	defer database.Close()
	keyStore, err := storage.NewSQLiteServiceKeyStore(database)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	secret := make([]byte, words.ServiceKey.KeyBytes)
	if _, err := rand.Read(secret); err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	token := base64.RawURLEncoding.EncodeToString(secret)
	clear(secret)
	verifier, err := security.HashServiceKey(token, words.ServiceKey.HashCost)
	if err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	identifier := make([]byte, words.ServiceKey.KeyBytes)
	if _, err := rand.Read(identifier); err != nil {
		writeFailure(options.output, words.Exits.Internal, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Internal
	}
	keyID := hex.EncodeToString(identifier)
	clear(identifier)
	accessService := application.AccessService{Store: keyStore}
	if err := accessService.Bootstrap(context.Background(), keyID, verifier, words.ServiceKey.RolePlatformAdmin); err != nil {
		var exists models.ActiveServiceKeyExists
		if errors.As(err, &exists) {
			writeFailure(options.output, words.Exits.Conflict, words.Codes.AccessBootstrapConflict, words.Diagnostics.AccessBootstrapConflict)
			return words.Exits.Conflict
		}
		writeFailure(options.output, words.Exits.Unavailable, words.Codes.ConfigInvalid, words.Diagnostics.ConfigInvalid)
		return words.Exits.Unavailable
	}
	if options.output == words.Outputs.JSON {
		writeSuccess(options.output, map[string]any{words.JSON.Token: token})
	} else {
		fmt.Println(token)
	}
	return words.Exits.OK
}
