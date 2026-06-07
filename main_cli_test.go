package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestParseAllowedHostsFlag(t *testing.T) {
	hosts := parseAllowedHostsFlag(" Jarvis01, , prod-box ,dev ")
	want := []string{"Jarvis01", "prod-box", "dev"}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("expected %v, got %v", want, hosts)
	}
}

func TestLoadAllowedHostsFromConfigMissingFileIsSilent(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	var logs bytes.Buffer
	originalWriter := log.Writer()
	originalFlags := log.Flags()
	log.SetOutput(&logs)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(originalWriter)
		log.SetFlags(originalFlags)
	}()

	hosts := loadAllowedHostsFromConfig()
	if hosts != nil {
		t.Fatalf("expected nil hosts for missing config, got %v", hosts)
	}
	if logs.Len() != 0 {
		t.Fatalf("expected missing config to be silent, got log output %q", logs.String())
	}
}

func TestLoadAllowedHostsFromConfigReadsHosts(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("chdir temp dir: %v", err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()

	configPath := filepath.Join(tmpDir, configFileName)
	config := []byte("{\n  \"hosts\": [\"Jarvis01\", \"prod-box\"]\n}\n")
	if err := os.WriteFile(configPath, config, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	hosts := loadAllowedHostsFromConfig()
	want := []string{"Jarvis01", "prod-box"}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("expected %v, got %v", want, hosts)
	}
}
