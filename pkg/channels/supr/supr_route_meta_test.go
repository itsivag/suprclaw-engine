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

func TestSuprMessageUpdatePayloadIncludesRouteMetadata(t *testing.T) {
	ch := &SuprChannel{}

	ch.rememberRouteMetadata("supr:agent:seo:main", "seo", "explicit")
	payload := ch.messageUpdatePayload("supr:agent:seo:main", "msg-1", "updated response")

	if payload["message_id"] != "msg-1" {
		t.Fatalf("message_id = %v, want msg-1", payload["message_id"])
	}
	if payload["content"] != "updated response" {
		t.Fatalf("content = %v, want updated response", payload["content"])
	}
	if payload["resolved_agent_id"] != "seo" {
		t.Fatalf("resolved_agent_id = %v, want seo", payload["resolved_agent_id"])
	}
	if payload["route_matched_by"] != "explicit" {
		t.Fatalf("route_matched_by = %v, want explicit", payload["route_matched_by"])
	}
}
