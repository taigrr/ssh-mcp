package main

import (
	"reflect"
	"testing"

	"github.com/taigrr/jety"
)

func TestLoadAllowedHostsReturnsNilWhenHostsMissing(t *testing.T) {
	jety.Set("hosts", nil)
	if hosts := loadAllowedHosts(); hosts != nil {
		t.Fatalf("expected nil hosts when config key is missing, got %v", hosts)
	}
}

func TestLoadAllowedHostsReturnsNilForNonSlice(t *testing.T) {
	jety.Set("hosts", "Jarvis01")
	if hosts := loadAllowedHosts(); hosts != nil {
		t.Fatalf("expected nil hosts for non-slice config value, got %v", hosts)
	}
}

func TestLoadAllowedHostsKeepsStringEntries(t *testing.T) {
	jety.Set("hosts", []any{"Jarvis01", 42, true, "prod-box"})

	hosts := loadAllowedHosts()
	want := []string{"Jarvis01", "prod-box"}
	if !reflect.DeepEqual(hosts, want) {
		t.Fatalf("expected %v, got %v", want, hosts)
	}
}
