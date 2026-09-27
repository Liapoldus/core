package config

import (
	"encoding/json"
	"errors"

	assets "github.com/Liapoldus/core"
)

type AdminMutationWords struct {
	Paths struct {
		ManagementPrefix        string   `json:"managementPrefix"`
		Snapshot                string   `json:"snapshot"`
		PathSeparator           string   `json:"pathSeparator"`
		LeadingSlash            string   `json:"leadingSlash"`
		QuerySeparator          string   `json:"querySeparator"`
		ForbiddenPathCharacters []string `json:"forbiddenPathCharacters"`
		CheckpointDirectory     string   `json:"checkpointDirectory"`
		CheckpointSuffix        string   `json:"checkpointSuffix"`
		TemporarySuffix         string   `json:"temporarySuffix"`
		UnixPrefix              string   `json:"unixPrefix"`
		UnixNetwork             string   `json:"unixNetwork"`
		URLScheme               string   `json:"urlScheme"`
		URLHost                 string   `json:"urlHost"`
	} `json:"paths"`
	Methods struct {
		ReadOnly []string `json:"readOnly"`
		Mutating []string `json:"mutating"`
	} `json:"methods"`
	Headers struct {
		ForwardRequest  []string `json:"forwardRequest"`
		ForwardResponse []string `json:"forwardResponse"`
	} `json:"headers"`
	Limits struct {
		RequestBodyBytes  int64 `json:"requestBodyBytes"`
		SnapshotBytes     int64 `json:"snapshotBytes"`
		ResponseBodyBytes int64 `json:"responseBodyBytes"`
	} `json:"limits"`
	Timeouts struct {
		Request string `json:"request"`
	} `json:"timeouts"`
	Modes struct {
		Directory uint32 `json:"directory"`
		File      uint32 `json:"file"`
		Socket    uint32 `json:"socket"`
	} `json:"modes"`
	Socket struct {
		DirectoryPrefix string `json:"directoryPrefix"`
		Name            string `json:"name"`
	} `json:"socket"`
	Operation struct {
		Kind      string `json:"kind"`
		Running   string `json:"running"`
		Succeeded string `json:"succeeded"`
		Failed    string `json:"failed"`
	} `json:"operation"`
	Audit struct {
		Action    string `json:"action"`
		Resource  string `json:"resource"`
		Started   string `json:"started"`
		Succeeded string `json:"succeeded"`
		Failed    string `json:"failed"`
	} `json:"audit"`
	Statuses struct {
		SuccessMinimum int `json:"successMinimum"`
		SuccessMaximum int `json:"successMaximum"`
	} `json:"statuses"`
	Diagnostics struct {
		InvalidConfiguration  string `json:"invalidConfiguration"`
		SnapshotUnavailable   string `json:"snapshotUnavailable"`
		CheckpointUnavailable string `json:"checkpointUnavailable"`
		AdminUnavailable      string `json:"adminUnavailable"`
	} `json:"diagnostics"`
}

func LoadAdminMutation() (AdminMutationWords, error) {
	contents, err := assets.Contract(assets.CaddyAdminMutation)
	if err != nil {
		return AdminMutationWords{}, err
	}
	var words AdminMutationWords
	if err := json.Unmarshal(contents, &words); err != nil {
		return AdminMutationWords{}, err
	}
	if words.Paths.ManagementPrefix == "" || words.Paths.Snapshot == "" || words.Paths.PathSeparator == "" || words.Paths.LeadingSlash == "" || words.Paths.QuerySeparator == "" || len(words.Paths.ForbiddenPathCharacters) == 0 || words.Paths.CheckpointDirectory == "" || words.Paths.CheckpointSuffix == "" || words.Paths.TemporarySuffix == "" || words.Paths.UnixPrefix == "" || words.Paths.UnixNetwork == "" || words.Paths.URLScheme == "" || words.Paths.URLHost == "" || len(words.Methods.ReadOnly) == 0 || len(words.Methods.Mutating) == 0 || len(words.Headers.ForwardRequest) == 0 || len(words.Headers.ForwardResponse) == 0 || words.Limits.RequestBodyBytes < 1 || words.Limits.SnapshotBytes < 1 || words.Limits.ResponseBodyBytes < 1 || words.Timeouts.Request == "" || words.Modes.Directory == 0 || words.Modes.File == 0 || words.Modes.Socket == 0 || words.Socket.DirectoryPrefix == "" || words.Socket.Name == "" || words.Operation.Kind == "" || words.Operation.Running == "" || words.Operation.Succeeded == "" || words.Operation.Failed == "" || words.Audit.Action == "" || words.Audit.Resource == "" || words.Audit.Started == "" || words.Audit.Succeeded == "" || words.Audit.Failed == "" || words.Statuses.SuccessMinimum < 100 || words.Statuses.SuccessMaximum <= words.Statuses.SuccessMinimum || words.Diagnostics.InvalidConfiguration == "" || words.Diagnostics.SnapshotUnavailable == "" || words.Diagnostics.CheckpointUnavailable == "" || words.Diagnostics.AdminUnavailable == "" {
		return AdminMutationWords{}, errors.New(words.Diagnostics.InvalidConfiguration)
	}
	return words, nil
}
