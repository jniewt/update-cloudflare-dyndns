package main

import (
	"testing"
)

func TestGetExternalIP(t *testing.T) {
	got, err := GetExternalIP()
	if err != nil {
		t.Fatalf("GetExternalIP() failed: %v", err)
	}
	t.Log(got)
}
