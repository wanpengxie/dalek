package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_NodeCRUD_JSON(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")
	prepareDemoProjectWithOneTicket(t, bin, repo, home)

	addOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "add",
		"--name", "node-c",
		"--status", "online",
		"--roles", "run,dev",
		"--provider-modes", "run_executor,codex",
		"--endpoint", "http://node-c",
		"--default-provider", "run_executor",
		"-o", "json",
	)
	var addResp struct {
		Schema string `json:"schema"`
		Node   struct {
			Name   string `json:"Name"`
			Status string `json:"Status"`
		} `json:"node"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(addOut)), &addResp); err != nil {
		t.Fatalf("decode node add response failed: %v raw=%s", err, addOut)
	}
	if addResp.Schema != "dalek.node.add.v1" || addResp.Node.Name != "node-c" || addResp.Node.Status != "online" {
		t.Fatalf("unexpected add response: %+v", addResp)
	}

	listOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "ls",
		"-o", "json",
	)
	var listResp struct {
		Schema string `json:"schema"`
		Nodes  []struct {
			Name string `json:"Name"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(listOut)), &listResp); err != nil {
		t.Fatalf("decode node list response failed: %v raw=%s", err, listOut)
	}
	if listResp.Schema != "dalek.node.list.v1" || len(listResp.Nodes) != 1 || listResp.Nodes[0].Name != "node-c" {
		t.Fatalf("unexpected list response: %+v", listResp)
	}

	showOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "show",
		"--name", "node-c",
		"-o", "json",
	)
	var showResp struct {
		Schema string `json:"schema"`
		Node   struct {
			Name   string `json:"Name"`
			Status string `json:"Status"`
		} `json:"node"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(showOut)), &showResp); err != nil {
		t.Fatalf("decode node show response failed: %v raw=%s", err, showOut)
	}
	if showResp.Schema != "dalek.node.show.v1" || showResp.Node.Name != "node-c" {
		t.Fatalf("unexpected show response: %+v", showResp)
	}

	rmOut, _ := runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "rm",
		"--name", "node-c",
		"-o", "json",
	)
	var rmResp struct {
		Schema  string `json:"schema"`
		Name    string `json:"name"`
		Removed bool   `json:"removed"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(rmOut)), &rmResp); err != nil {
		t.Fatalf("decode node rm response failed: %v raw=%s", err, rmOut)
	}
	if rmResp.Schema != "dalek.node.rm.v1" || rmResp.Name != "node-c" || !rmResp.Removed {
		t.Fatalf("unexpected rm response: %+v", rmResp)
	}

	listOut, _ = runCLIOK(
		t,
		bin,
		repo,
		"-home", home,
		"-project", "demo",
		"node", "ls",
		"-o", "json",
	)
	listResp = struct {
		Schema string `json:"schema"`
		Nodes  []struct {
			Name string `json:"Name"`
		} `json:"nodes"`
	}{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(listOut)), &listResp); err != nil {
		t.Fatalf("decode node list response failed: %v raw=%s", err, listOut)
	}
	if len(listResp.Nodes) != 0 {
		t.Fatalf("expected empty node list after remove: %+v", listResp.Nodes)
	}
}
