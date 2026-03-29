package supr

import "testing"

func TestSuprRouteMetadataPayloadTracksLatestRoute(t *testing.T) {
	ch := &SuprChannel{}

	ch.rememberRouteMetadata("supr:agent:main:main", "writer", "explicit")
	payload := ch.routeMetadataPayload("supr:agent:main:main")
	if payload == nil {
		t.Fatal("payload should not be nil")
	}
	if payload["resolved_agent_id"] != "writer" {
		t.Fatalf("resolved_agent_id = %v, want writer", payload["resolved_agent_id"])
	}
	if payload["route_matched_by"] != "explicit" {
		t.Fatalf("route_matched_by = %v, want explicit", payload["route_matched_by"])
	}
}

func TestSuprRouteMetadataPayloadRetainsPreviousValuesOnPartialUpdate(t *testing.T) {
	ch := &SuprChannel{}

	ch.rememberRouteMetadata("supr:agent:main:main", "writer", "explicit")
	ch.rememberRouteMetadata("supr:agent:main:main", "", "default")
	payload := ch.routeMetadataPayload("supr:agent:main:main")
	if payload == nil {
		t.Fatal("payload should not be nil")
	}
	if payload["resolved_agent_id"] != "writer" {
		t.Fatalf("resolved_agent_id = %v, want writer", payload["resolved_agent_id"])
	}
	if payload["route_matched_by"] != "default" {
		t.Fatalf("route_matched_by = %v, want default", payload["route_matched_by"])
	}
}
