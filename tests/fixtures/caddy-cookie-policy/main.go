package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/Liapoldus/core/internal/infrastructure/caddy"
)

type requestReport struct {
	Cookies []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"cookies"`
}

func main() {
	if len(os.Args) != 3 {
		os.Exit(2)
	}
	address, endpoint := os.Args[1], os.Args[2]
	source := []byte("http://" + address + " {\n  liapoldus_plugin fixture forms.submit call\n}\n")
	base := caddy.PluginInstance{Name: "fixture", Endpoint: endpoint, Timeout: 3 * time.Second, StartTimeout: 3 * time.Second, MaxConcurrentCalls: 8}
	runtime, _, err := caddy.StartCaddyfileWithPlugins(source, []caddy.PluginInstance{base})
	if err != nil {
		panic(err)
	}
	defer runtime.Stop()
	client := &http.Client{Timeout: 5 * time.Second}
	before := requestCookies(client, address)
	base.CookiePolicies = []json.RawMessage{json.RawMessage(`{"version":1,"instanceId":"fixture","capability":"forms.submit","allowedNames":["session"]}`)}
	if err := runtime.ReplaceCaddyfileWithPlugins(source, []caddy.PluginInstance{base}); err != nil {
		panic(err)
	}
	after := requestCookies(client, address)
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{"before": before, "after": after}); err != nil {
		panic(err)
	}
}

func requestCookies(client *http.Client, address string) []map[string]string {
	request, err := http.NewRequest(http.MethodPost, "http://"+address+"/submission", bytes.NewBufferString("{}"))
	if err != nil {
		panic(err)
	}
	request.Header.Set("Cookie", "session=allowed; theme=blocked")
	request.Close = true
	response, err := client.Do(request)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
	var report requestReport
	if err := json.NewDecoder(response.Body).Decode(&report); err != nil {
		panic(err)
	}
	cookies := make([]map[string]string, 0, len(report.Cookies))
	for _, cookie := range report.Cookies {
		cookies = append(cookies, map[string]string{"name": cookie.Name, "value": cookie.Value})
	}
	return cookies
}
