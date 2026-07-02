package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
)

func main() {
	baseURL := flag.String("base-url", "http://127.0.0.1:3739", "LocalGate admin base URL")
	id := flag.String("id", "", "approval id")
	action := flag.String("action", "approve", "approve or reject")
	actor := flag.String("actor", "cli", "approval actor")
	host := flag.String("host", "", "optional HTTP Host header for reverse-proxy routing")
	flag.Parse()

	if *id == "" {
		fmt.Fprintln(os.Stderr, "missing -id")
		os.Exit(1)
	}
	if *action != "approve" && *action != "reject" {
		fmt.Fprintln(os.Stderr, "invalid -action")
		os.Exit(1)
	}

	body, _ := json.Marshal(map[string]any{"actor": *actor})
	req, err := http.NewRequest(http.MethodPost, *baseURL+"/approvals/"+*id+"/"+*action, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	req.Header.Set("Content-Type", "application/json")
	if *host != "" {
		req.Host = *host
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		fmt.Fprintln(os.Stderr, string(b))
		os.Exit(1)
	}
	fmt.Println(string(b))
}
