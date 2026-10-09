package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
	sdkinfrastructure "github.com/Liapoldus/plugin-sdk/infrastructure"
)

// NewPluginPeerDirectoryHandler serves the Plugin SDK v2 peer-directory poll
// endpoint on Core's private mTLS listener. The directory is scoped to the
// authenticated replica named by the client certificate: a verified certificate
// that does not match a live registration is rejected, and the caller never
// supplies its own identity. An unchanged conditional poll blocks for at most
// waitMs and then answers 304 with the same ETag; a change wakes it immediately.
func NewPluginPeerDirectoryHandler(
	contract sdkinfrastructure.PeerDirectoryPollContract,
	directory *plugins.PluginReplicaDirectory,
	rules func(callerInstanceID string) []models.PeerLinkRule,
	subscribe func() (<-chan struct{}, func()),
	now func() time.Time,
	ttl time.Duration,
	next http.Handler,
) (http.Handler, error) {
	if contract.Validate() != nil || directory == nil || subscribe == nil || now == nil ||
		ttl <= 0 || ttl > sdkmodels.PeerDirectoryMaximumTTL || next == nil {
		return nil, sdkinfrastructure.ErrInvalidPeerDirectoryPollContract
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != contract.Endpoint.Path {
			next.ServeHTTP(response, request)
			return
		}
		servePeerDirectoryPoll(contract, directory, rules, subscribe, now, ttl, response, request)
	}), nil
}

func servePeerDirectoryPoll(
	contract sdkinfrastructure.PeerDirectoryPollContract,
	directory *plugins.PluginReplicaDirectory,
	rules func(callerInstanceID string) []models.PeerLinkRule,
	subscribe func() (<-chan struct{}, func()),
	now func() time.Time,
	ttl time.Duration,
	response http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != contract.Endpoint.Method {
		writePeerDirectoryProblem(response, contract, "invalidRequest")
		return
	}
	if request.TLS == nil || len(request.TLS.PeerCertificates) == 0 || len(request.TLS.VerifiedChains) == 0 {
		writePeerDirectoryProblem(response, contract, "unauthenticated")
		return
	}
	waitMs, ok := peerDirectoryWait(request, contract)
	if !ok {
		writePeerDirectoryProblem(response, contract, "invalidRequest")
		return
	}
	caller, authenticated := directory.ResolveIdentity(request.TLS.PeerCertificates[0])
	if !authenticated {
		writePeerDirectoryProblem(response, contract, "identityMismatch")
		return
	}
	ifNoneMatch := request.Header.Get(http.CanonicalHeaderKey("if-none-match"))
	if ifNoneMatch == "" {
		if waitMs != 0 {
			writePeerDirectoryProblem(response, contract, "invalidRequest")
			return
		}
		assembly, err := assemblePeerDirectory(directory, rules, caller, now, ttl)
		if err != nil {
			writePeerDirectoryProblem(response, contract, "unavailable")
			return
		}
		writePeerDirectory(response, contract, assembly)
		return
	}
	if !validPeerDirectoryETagHeader(ifNoneMatch) {
		writePeerDirectoryProblem(response, contract, "invalidRequest")
		return
	}
	etag := ifNoneMatch
	deadline := time.Now().Add(time.Duration(waitMs) * time.Millisecond)
	for {
		signal, cancel := subscribe()
		assembly, err := assemblePeerDirectory(directory, rules, caller, now, ttl)
		if err != nil {
			cancel()
			writePeerDirectoryProblem(response, contract, "unavailable")
			return
		}
		if peerDirectoryETag(assembly.Generation) != etag {
			cancel()
			writePeerDirectory(response, contract, assembly)
			return
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			cancel()
			writePeerDirectoryUnchanged(response, contract, etag)
			return
		}
		timer := peerDirectoryLeaseTimer(directory, now, remaining)
		select {
		case <-signal:
			cancel()
		case <-timer:
			cancel()
		case <-time.After(remaining):
			cancel()
			writePeerDirectoryUnchanged(response, contract, etag)
			return
		case <-request.Context().Done():
			cancel()
			return
		}
	}
}

func peerDirectoryWait(request *http.Request, contract sdkinfrastructure.PeerDirectoryPollContract) (int, bool) {
	query := request.URL.Query()
	for key := range query {
		if key != contract.Poll.WaitParameter {
			return 0, false
		}
	}
	values, present := query[contract.Poll.WaitParameter]
	if !present {
		return 0, true
	}
	if len(values) != 1 {
		return 0, false
	}
	parsed, err := strconv.Atoi(values[0])
	if err != nil || parsed < 0 || parsed > contract.Poll.MaximumWaitMs {
		return 0, false
	}
	return parsed, true
}

func assemblePeerDirectory(
	directory *plugins.PluginReplicaDirectory,
	rules func(callerInstanceID string) []models.PeerLinkRule,
	caller sdkmodels.PeerReplicaID,
	now func() time.Time,
	ttl time.Duration,
) (sdkmodels.PeerDirectory, error) {
	var callerRules []models.PeerLinkRule
	if rules != nil {
		callerRules = rules(caller.InstanceID)
	}
	return directory.PeerDirectory(caller, callerRules, now(), ttl)
}

// peerDirectoryLeaseTimer returns a channel that closes at the nearest live
// replica lease expiry, so a poller wakes when an eligible replica ages out
// even though no explicit change was broadcast. When no lease expires within
// the remaining wait budget the timer never fires.
func peerDirectoryLeaseTimer(directory *plugins.PluginReplicaDirectory, now func() time.Time, remaining time.Duration) <-chan time.Time {
	if remaining <= 0 {
		return nil
	}
	instant := now().UTC()
	var nearest time.Time
	for _, replica := range directory.Snapshot() {
		if !replica.LeaseExpires.After(instant) {
			continue
		}
		if nearest.IsZero() || replica.LeaseExpires.Before(nearest) {
			nearest = replica.LeaseExpires
		}
	}
	if nearest.IsZero() {
		return nil
	}
	delay := nearest.Sub(instant)
	if delay <= 0 || delay > remaining {
		return nil
	}
	return time.After(delay)
}

func validPeerDirectoryETagHeader(value string) bool {
	if len(value) < 3 || value[0] != '"' || value[len(value)-1] != '"' {
		return false
	}
	inner := value[1 : len(value)-1]
	if inner == "" {
		return false
	}
	for _, character := range inner {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func peerDirectoryETag(generation string) string { return `"` + generation + `"` }

func writePeerDirectory(response http.ResponseWriter, contract sdkinfrastructure.PeerDirectoryPollContract, directory sdkmodels.PeerDirectory) {
	body, err := json.Marshal(directory)
	if err != nil || int64(len(body)) > contract.Responses.Directory.MaximumBytes {
		writePeerDirectoryProblem(response, contract, "unavailable")
		return
	}
	response.Header().Set(http.CanonicalHeaderKey("content-type"), contract.DirectoryMediaType)
	response.Header().Set(http.CanonicalHeaderKey("etag"), peerDirectoryETag(directory.Generation))
	response.Header().Set(http.CanonicalHeaderKey("cache-control"), "no-store")
	response.WriteHeader(contract.Responses.Directory.Status)
	_, _ = response.Write(body)
}

func writePeerDirectoryUnchanged(response http.ResponseWriter, contract sdkinfrastructure.PeerDirectoryPollContract, etag string) {
	response.Header().Set(http.CanonicalHeaderKey("etag"), etag)
	response.Header().Set(http.CanonicalHeaderKey("cache-control"), "no-store")
	response.WriteHeader(contract.Responses.Unchanged.Status)
}

func writePeerDirectoryProblem(response http.ResponseWriter, contract sdkinfrastructure.PeerDirectoryPollContract, name string) {
	problem, ok := contract.Problems[name]
	if !ok || problem.Status < 400 || problem.Code == "" {
		problem = contract.Problems["unavailable"]
	}
	response.Header().Set(http.CanonicalHeaderKey("cache-control"), "no-store")
	writeJSONResponse(response, problem.Status, http.CanonicalHeaderKey("content-type"), contract.ProblemMediaType,
		map[string]string{"code": problem.Code})
}
