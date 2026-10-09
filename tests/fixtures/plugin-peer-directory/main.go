package main

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Liapoldus/core/internal/domain/models"
	"github.com/Liapoldus/core/internal/infrastructure/plugins"
	"github.com/Liapoldus/core/internal/presentation/api"
	sdkmodels "github.com/Liapoldus/plugin-sdk/domain/models"
)

func main() {
	lifecycle, err := plugins.LoadSDKReplicaLifecycleContract()
	check(err)
	poll, err := plugins.LoadSDKPeerDirectoryPollContract()
	check(err)

	contractVersion := lifecycle.ContractVersion
	now := func() time.Time { return time.Now().UTC() }
	directory, err := plugins.NewPluginReplicaDirectory(lifecycle, now)
	check(err)
	changes := plugins.NewPeerDirectoryBroadcaster()

	caller := sdkmodels.PeerReplicaID{InstanceID: "caller-instance", ReplicaID: "caller-replica", IncarnationID: "inc-caller", PlacementID: "placement-one"}
	target := sdkmodels.PeerReplicaID{InstanceID: "target-instance", ReplicaID: "target-replica", IncarnationID: "inc-target", PlacementID: "placement-one"}
	secondTarget := sdkmodels.PeerReplicaID{InstanceID: "second-instance", ReplicaID: "second-replica", IncarnationID: "inc-second", PlacementID: "placement-one"}

	callerCertificate := certificateFor(takeURI(lifecycle, caller))
	_, err = directory.Register(registrationFor(contractVersion, caller), callerCertificate)
	check(err)
	targetEndpoint := sdkmodels.ReplicaPeerEndpoint{Carrier: sdkmodels.PeerCarrierUnix, Endpoint: "/run/liapoldus/target.sock", SecurityProfile: sdkmodels.PeerSecurityMTLS}
	_, err = directory.Register(registrationFor(contractVersion, target, targetEndpoint), certificateFor(takeURI(lifecycle, target)))
	check(err)

	linkRule := models.PeerLinkRule{
		CallerInstanceID: caller.InstanceID, TargetInstanceID: target.InstanceID,
		PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierUnix, Weight: 5,
	}
	var rules atomic.Pointer[[]models.PeerLinkRule]
	initialRules := []models.PeerLinkRule{linkRule}
	rules.Store(&initialRules)
	provider := func(string) []models.PeerLinkRule { return *rules.Load() }

	notFound := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { response.WriteHeader(http.StatusTeapot) })
	handler, err := api.NewPluginPeerDirectoryHandler(poll, directory, provider, changes.Subscribe, now, 15*time.Second, notFound)
	check(err)

	foreign := certificateFor("spiffe://liapoldus/plugin/foreign-instance/foreign-replica/inc-foreign")

	// A request for another path falls through to the wrapped handler.
	otherPath := call(handler, http.MethodGet, "/internal/v2/other", callerCertificate, true, nil)
	// The endpoint is GET-only.
	wrongMethod := call(handler, http.MethodPost, poll.Endpoint.Path, callerCertificate, true, nil)
	// A TLS peer without a verified client certificate is unauthenticated.
	noCertificate := call(handler, http.MethodGet, poll.Endpoint.Path, nil, false, nil)
	unverified := call(handler, http.MethodGet, poll.Endpoint.Path, callerCertificate, false, nil)
	// A verified certificate that names no live replica cannot poll a directory.
	foreignCertificate := call(handler, http.MethodGet, poll.Endpoint.Path, foreign, true, nil)
	// A caller cannot supply identity: a non-matching but valid ETag still maps
	// to the authenticated replica, so an unregistered certificate is rejected.
	foreignConditional := call(handler, http.MethodGet, poll.Endpoint.Path, foreign, true, map[string]string{"if-none-match": `"` + strings.Repeat("a", 64) + `"`})

	initial := call(handler, http.MethodGet, poll.Endpoint.Path, callerCertificate, true, nil)
	var initialDirectory sdkmodels.PeerDirectory
	decodeBody(initial, &initialDirectory)
	etag := initial.Header().Get("etag")

	// An initial request must not wait.
	initialWaits := call(handler, http.MethodGet, poll.Endpoint.Path+"?waitMs=1000", callerCertificate, true, nil)
	// Unknown query parameters are rejected outright.
	unknownQuery := call(handler, http.MethodGet, poll.Endpoint.Path+"?cursor=1", callerCertificate, true, nil)
	// waitMs must be a bounded integer.
	oversizedWait := call(handler, http.MethodGet, poll.Endpoint.Path+"?waitMs=999999", callerCertificate, true, nil)
	malformedWait := call(handler, http.MethodGet, poll.Endpoint.Path+"?waitMs=abc", callerCertificate, true, nil)
	// A conditional request with a stale but well-formed ETag returns at once.
	stale := call(handler, http.MethodGet, poll.Endpoint.Path+"?waitMs=1000", callerCertificate, true, map[string]string{"if-none-match": `"` + strings.Repeat("b", 64) + `"`})
	// A malformed ETag cannot be matched.
	malformedETag := call(handler, http.MethodGet, poll.Endpoint.Path+"?waitMs=1000", callerCertificate, true, map[string]string{"if-none-match": "not-a-tag"})
	// A conditional request with the current ETag and no change returns 304.
	unchanged := call(handler, http.MethodGet, poll.Endpoint.Path+"?waitMs=120", callerCertificate, true, map[string]string{"if-none-match": etag})

	// A membership or policy change wakes a pending conditional poll early and
	// it answers with the new generation.
	_, err = directory.Register(registrationFor(contractVersion, secondTarget, targetEndpoint), certificateFor(takeURI(lifecycle, secondTarget)))
	check(err)
	changedResult := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		changedResult <- call(handler, http.MethodGet, poll.Endpoint.Path+"?waitMs=5000", callerCertificate, true, map[string]string{"if-none-match": etag})
	}()
	time.Sleep(75 * time.Millisecond)
	updatedRules := []models.PeerLinkRule{linkRule, {
		CallerInstanceID: caller.InstanceID, TargetInstanceID: secondTarget.InstanceID,
		PlacementRule: models.PeerLinkPlacementSame, Carrier: models.PeerLinkCarrierUnix, Weight: 5,
	}}
	rules.Store(&updatedRules)
	changes.Notify()
	changedResponse := <-changedResult
	var changedDirectory sdkmodels.PeerDirectory
	decodeBody(changedResponse, &changedDirectory)

	result := map[string]bool{
		"otherPathFallsThrough":             otherPath.Code == http.StatusTeapot,
		"invalidMethodRejected":             wrongMethod.Code == poll.Problems["invalidRequest"].Status,
		"missingCertificateUnauthenticated": noCertificate.Code == poll.Problems["unauthenticated"].Status,
		"unverifiedChainUnauthenticated":    unverified.Code == poll.Problems["unauthenticated"].Status,
		"foreignIdentityMismatch":           foreignCertificate.Code == poll.Problems["identityMismatch"].Status,
		"foreignConditionalMismatch":        foreignConditional.Code == poll.Problems["identityMismatch"].Status,
		"initialDirectoryOK":                initial.Code == poll.Responses.Directory.Status,
		"initialContentType":                initial.Header().Get("content-type") == poll.DirectoryMediaType,
		"initialCacheControlNoStore":        strings.EqualFold(initial.Header().Get("cache-control"), "no-store"),
		"initialStrongETag":                 strings.HasPrefix(etag, `"`) && strings.HasSuffix(etag, `"`) && strings.Trim(etag, `"`) == initialDirectory.Generation,
		"initialCallerScope":                initialDirectory.Caller == caller && len(initialDirectory.Links) == 1,
		"initialTargetReady":                len(initialDirectory.Links) == 1 && initialDirectory.Links[0].TargetInstanceID == target.InstanceID && len(initialDirectory.Links[0].Replicas) == 1 && initialDirectory.Links[0].Replicas[0].Eligibility == sdkmodels.PeerEligibilityReady && initialDirectory.Links[0].Replicas[0].Endpoint == targetEndpoint.Endpoint,
		"initialRequestMustNotWait":         initialWaits.Code == poll.Problems["invalidRequest"].Status,
		"unknownQueryRejected":              unknownQuery.Code == poll.Problems["invalidRequest"].Status,
		"oversizedWaitRejected":             oversizedWait.Code == poll.Problems["invalidRequest"].Status,
		"malformedWaitRejected":             malformedWait.Code == poll.Problems["invalidRequest"].Status,
		"staleETagReturnsImmediately":       stale.Code == poll.Responses.Directory.Status && stale.Header().Get("etag") == etag,
		"malformedETagRejected":             malformedETag.Code == poll.Problems["invalidRequest"].Status,
		"unchangedPoll304":                  unchanged.Code == poll.Responses.Unchanged.Status && unchanged.Header().Get("etag") == etag && unchanged.Body.Len() == 0,
		"policyChangeWakesPoll":             changedResponse.Code == poll.Responses.Directory.Status && changedResponse.Header().Get("etag") != etag && len(changedDirectory.Links) == 2,
	}
	check(json.NewEncoder(os.Stdout).Encode(result))
}

func call(handler http.Handler, method, target string, certificate *x509.Certificate, verified bool, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, nil)
	if certificate != nil {
		state := &tls.ConnectionState{PeerCertificates: []*x509.Certificate{certificate}}
		if verified {
			state.VerifiedChains = [][]*x509.Certificate{{certificate}}
		}
		request.TLS = state
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeBody(recorder *httptest.ResponseRecorder, target *sdkmodels.PeerDirectory) {
	if recorder.Body.Len() == 0 {
		return
	}
	check(json.Unmarshal(recorder.Body.Bytes(), target))
}

func registrationFor(contractVersion string, identity sdkmodels.PeerReplicaID, endpoints ...sdkmodels.ReplicaPeerEndpoint) sdkmodels.ReplicaRegistrationRequest {
	if endpoints == nil {
		endpoints = []sdkmodels.ReplicaPeerEndpoint{}
	}
	return sdkmodels.ReplicaRegistrationRequest{
		ContractVersion:     contractVersion,
		Identity:            identity,
		RestEndpoint:        "https://" + identity.InstanceID + ".internal:9443",
		PeerEndpoints:       endpoints,
		Release:             sdkmodels.ReplicaRelease{Version: "1.0.0", SHA256: strings.Repeat("0", 64)},
		AdvertisedContracts: []sdkmodels.ContractVersion{},
		AcceptedContracts:   []sdkmodels.ContractRange{},
		AppliedGeneration:   "1",
		Ready:               true,
	}
}

func takeURI(contract plugins.SDKReplicaLifecycleContract, identity sdkmodels.PeerReplicaID) string {
	uri, err := contract.ReplicaIdentityURI(identity)
	check(err)
	return uri
}

func certificateFor(value string) *x509.Certificate {
	identifier, err := url.Parse(value)
	check(err)
	return &x509.Certificate{URIs: []*url.URL{identifier}}
}

func check(err error) {
	if err != nil {
		panic(err)
	}
}
