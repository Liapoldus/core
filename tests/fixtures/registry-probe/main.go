package main

import (
  "encoding/json"
  "os"

  "github.com/Liapoldus/core/internal/application"
  "github.com/Liapoldus/core/internal/infrastructure/config"
  "github.com/Liapoldus/core/internal/infrastructure/storage"
)

func main() {
  values := os.Args[1:]
  layout, err := config.LoadRegistryLayout()
  if err != nil { os.Exit(1) }
  service := application.RegistryService{Store: storage.NewFilesystemStore(values[0], layout)}
  var result any
  if values[2] == "publish" {
    result, err = service.Publish(values[1], values[3])
  } else {
    result, err = service.Rollback(values[1])
  }
  if err != nil { os.Exit(1) }
  _ = json.NewEncoder(os.Stdout).Encode(result)
}