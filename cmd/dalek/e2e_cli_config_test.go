package main

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

type configE2EValuePayload struct {
	Schema string `json:"schema"`
	Key    string `json:"key"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

func TestCLI_E2E_ConfigCommands(t *testing.T) {
	bin := buildCLIBinary(t)
	repo := initGitRepo(t)
	home := filepath.Join(t.TempDir(), "home")

	_, _ = runCLIOK(t, bin, repo, "-home", home, "init", "-name", "demo")

	lsOut, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "ls", "-o", "json")
	var lsPayload struct {
		Schema  string `json:"schema"`
		Configs []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Source string `json:"source"`
		} `json:"configs"`
	}
	if err := json.Unmarshal([]byte(lsOut), &lsPayload); err != nil {
		t.Fatalf("decode config ls json failed: %v\nraw=%s", err, lsOut)
	}
	if lsPayload.Schema != "dalek.config.list.v1" {
		t.Fatalf("unexpected config ls schema: %q", lsPayload.Schema)
	}
	gotByKey := make(map[string]struct {
		value  string
		source string
	}, len(lsPayload.Configs))
	for _, item := range lsPayload.Configs {
		gotByKey[strings.TrimSpace(item.Key)] = struct {
			value  string
			source string
		}{
			value:  strings.TrimSpace(item.Value),
			source: strings.TrimSpace(item.Source),
		}
	}
	requiredKeys := []string{
		"daemon.internal.listen",
		"daemon.internal.allow_cidrs",
		"daemon.public.listen",
		"daemon.max_concurrent",
		"project.max_running_workers",
		"agent.provider",
		"agent.model",
	}
	for _, key := range requiredKeys {
		if _, ok := gotByKey[key]; !ok {
			t.Fatalf("config ls missing key=%s payload=%s", key, lsOut)
		}
	}
	if got := gotByKey["daemon.max_concurrent"]; got.value != "4" || got.source != "default" {
		t.Fatalf("unexpected daemon.max_concurrent initial value/source: %+v", got)
	}
	if got := gotByKey["project.max_running_workers"]; got.value != "3" || got.source != "db" {
		t.Fatalf("unexpected project.max_running_workers initial value/source: %+v", got)
	}

	getOut, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "get", "daemon.max_concurrent", "-o", "json")
	var getPayload configE2EValuePayload
	if err := json.Unmarshal([]byte(getOut), &getPayload); err != nil {
		t.Fatalf("decode config get json failed: %v\nraw=%s", err, getOut)
	}
	if getPayload.Schema != "dalek.config.get.v1" || getPayload.Value != "4" || getPayload.Source != "default" {
		t.Fatalf("unexpected config get daemon.max_concurrent payload: %+v", getPayload)
	}

	getOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "get", "daemon.internal.allow_cidrs", "-o", "json")
	if err := json.Unmarshal([]byte(getOut), &getPayload); err != nil {
		t.Fatalf("decode config get daemon.internal.allow_cidrs json failed: %v\nraw=%s", err, getOut)
	}
	if getPayload.Value != "127.0.0.1/32,::1/128" {
		t.Fatalf("unexpected daemon.internal.allow_cidrs default payload: %+v", getPayload)
	}

	setOut, _ := runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "set", "daemon.max_concurrent", "8", "--global", "-o", "json")
	var setPayload configE2EValuePayload
	if err := json.Unmarshal([]byte(setOut), &setPayload); err != nil {
		t.Fatalf("decode config set daemon.max_concurrent json failed: %v\nraw=%s", err, setOut)
	}
	if setPayload.Schema != "dalek.config.set.v1" || setPayload.Value != "8" || setPayload.Source != "global" {
		t.Fatalf("unexpected config set daemon.max_concurrent payload: %+v", setPayload)
	}

	getOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "get", "daemon.max_concurrent", "-o", "json")
	if err := json.Unmarshal([]byte(getOut), &getPayload); err != nil {
		t.Fatalf("decode config get daemon.max_concurrent(after set) json failed: %v\nraw=%s", err, getOut)
	}
	if getPayload.Value != "8" || getPayload.Source != "global" {
		t.Fatalf("unexpected daemon.max_concurrent after set: %+v", getPayload)
	}

	setOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "set", "daemon.internal.allow_cidrs", "10.0.0.0/8,192.168.0.0/16", "--global", "-o", "json")
	if err := json.Unmarshal([]byte(setOut), &setPayload); err != nil {
		t.Fatalf("decode config set daemon.internal.allow_cidrs json failed: %v\nraw=%s", err, setOut)
	}
	if setPayload.Value != "10.0.0.0/8,192.168.0.0/16" || setPayload.Source != "global" {
		t.Fatalf("unexpected config set daemon.internal.allow_cidrs payload: %+v", setPayload)
	}

	setOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "set", "project.max_running_workers", "5", "-o", "json")
	if err := json.Unmarshal([]byte(setOut), &setPayload); err != nil {
		t.Fatalf("decode config set project.max_running_workers json failed: %v\nraw=%s", err, setOut)
	}
	if setPayload.Value != "5" || setPayload.Source != "db" {
		t.Fatalf("unexpected config set project.max_running_workers payload: %+v", setPayload)
	}

	getOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "get", "project.max_running_workers", "-o", "json")
	if err := json.Unmarshal([]byte(getOut), &getPayload); err != nil {
		t.Fatalf("decode config get project.max_running_workers json failed: %v\nraw=%s", err, getOut)
	}
	if getPayload.Value != "5" || getPayload.Source != "db" {
		t.Fatalf("unexpected project.max_running_workers after set: %+v", getPayload)
	}

	setOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "set", "agent.provider", "claude", "--local", "-o", "json")
	if err := json.Unmarshal([]byte(setOut), &setPayload); err != nil {
		t.Fatalf("decode config set agent.provider json failed: %v\nraw=%s", err, setOut)
	}
	if setPayload.Value != "claude" || setPayload.Source != "local" {
		t.Fatalf("unexpected config set agent.provider payload: %+v", setPayload)
	}

	setOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "set", "agent.model", "claude-3-7-sonnet", "--local", "-o", "json")
	if err := json.Unmarshal([]byte(setOut), &setPayload); err != nil {
		t.Fatalf("decode config set agent.model json failed: %v\nraw=%s", err, setOut)
	}
	if setPayload.Value != "claude-3-7-sonnet" || setPayload.Source != "local" {
		t.Fatalf("unexpected config set agent.model payload: %+v", setPayload)
	}

	getOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "get", "agent.provider", "-o", "json")
	if err := json.Unmarshal([]byte(getOut), &getPayload); err != nil {
		t.Fatalf("decode config get agent.provider json failed: %v\nraw=%s", err, getOut)
	}
	if getPayload.Value != "claude" || getPayload.Source != "local" {
		t.Fatalf("unexpected agent.provider after set: %+v", getPayload)
	}

	getOut, _ = runCLIOK(t, bin, repo, "-home", home, "-project", "demo", "config", "get", "agent.model", "-o", "json")
	if err := json.Unmarshal([]byte(getOut), &getPayload); err != nil {
		t.Fatalf("decode config get agent.model json failed: %v\nraw=%s", err, getOut)
	}
	if getPayload.Value != "claude-3-7-sonnet" || getPayload.Source != "local" {
		t.Fatalf("unexpected agent.model after set: %+v", getPayload)
	}

	_, stderr, err := runCLI(t, bin, repo, "-home", home, "-project", "demo", "config", "set", "daemon.internal.listen", "127.0.0.1:19081", "--local")
	if err == nil {
		t.Fatalf("config set daemon.internal.listen --local should fail")
	}
	if !strings.Contains(stderr, "不支持 scope=local") {
		t.Fatalf("unexpected stderr for invalid scope:\n%s", stderr)
	}
}
