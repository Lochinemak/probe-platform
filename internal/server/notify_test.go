package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"probe-platform/internal/protocol"
)

func TestNotifyGotify(t *testing.T) {
	var got struct {
		path, key, ctype string
		body             map[string]any
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.key = r.Header.Get("X-Gotify-Key")
		got.ctype = r.Header.Get("Content-Type")
		_ = json.NewDecoder(r.Body).Decode(&got.body)
		if got.key != "apptoken" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"Unauthorized","errorCode":401,"errorDescription":"you need to provide a valid access token"}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":1,"appid":1,"message":"x","title":"t","priority":5,"date":"2026-01-01T00:00:00Z"}`))
	}))
	defer srv.Close()

	n := NewNotifier(discardLogger())
	ch := &protocol.NotifyChannel{Type: "gotify", Config: map[string]string{"server": srv.URL + "/", "token": "apptoken"}}
	if err := n.Send(context.Background(), ch, "标题", "第一行\n第二行"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.path != "/message" {
		t.Errorf("path = %q, want /message", got.path)
	}
	if !strings.HasPrefix(got.ctype, "application/json") {
		t.Errorf("content-type = %q", got.ctype)
	}
	if got.body["title"] != "标题" || got.body["message"] != "第一行\n第二行" {
		t.Errorf("body = %v", got.body)
	}
	if p, _ := got.body["priority"].(float64); int(p) != gotifyDefaultPriority {
		t.Errorf("priority = %v, want default %d", got.body["priority"], gotifyDefaultPriority)
	}

	ch.Config["priority"] = "8"
	if err := n.Send(context.Background(), ch, "t", "x"); err != nil {
		t.Fatalf("send with priority: %v", err)
	}
	if p, _ := got.body["priority"].(float64); int(p) != 8 {
		t.Errorf("priority = %v, want 8", got.body["priority"])
	}

	ch.Config["priority"] = "high"
	if err := n.Send(context.Background(), ch, "t", "x"); err == nil {
		t.Error("expected error for non-numeric priority")
	}

	ch.Config["priority"] = ""
	ch.Config["token"] = "wrong"
	err := n.Send(context.Background(), ch, "t", "x")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("expected 401 error, got %v", err)
	}

	for _, cfg := range []map[string]string{{"token": "x"}, {"server": srv.URL}} {
		if err := n.Send(context.Background(), &protocol.NotifyChannel{Type: "gotify", Config: cfg}, "t", "x"); err == nil {
			t.Errorf("expected validation error for %v", cfg)
		}
	}
}

func TestNotifyPushDeer(t *testing.T) {
	var got struct {
		path, ctype string
		form        map[string]string
	}
	reply := `{"code":0,"content":{"result":["{\"counts\":1,\"logs\":[],\"success\":\"ok\"}"]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		got.ctype = r.Header.Get("Content-Type")
		_ = r.ParseForm()
		got.form = map[string]string{}
		for k := range r.PostForm {
			got.form[k] = r.PostForm.Get(k)
		}
		_, _ = w.Write([]byte(reply))
	}))
	defer srv.Close()

	n := NewNotifier(discardLogger())
	ch := &protocol.NotifyChannel{Type: "pushdeer", Config: map[string]string{"pushkey": "PDU1TXXXX", "server": srv.URL}}
	if err := n.Send(context.Background(), ch, "标题", "第一行\n第二行"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got.path != "/message/push" {
		t.Errorf("path = %q, want /message/push", got.path)
	}
	if !strings.HasPrefix(got.ctype, "application/x-www-form-urlencoded") {
		t.Errorf("content-type = %q", got.ctype)
	}
	want := map[string]string{"pushkey": "PDU1TXXXX", "text": "标题", "desp": "第一行\n\n第二行", "type": "markdown"}
	for k, v := range want {
		if got.form[k] != v {
			t.Errorf("form[%s] = %q, want %q", k, got.form[k], v)
		}
	}

	// PushDeer signals failures with HTTP 200 and a non-zero code.
	reply = `{"code":80100,"error":"pushkey 不存在"}`
	err := n.Send(context.Background(), ch, "t", "x")
	if err == nil || !strings.Contains(err.Error(), "80100") {
		t.Errorf("expected rejected error, got %v", err)
	}

	if err := n.Send(context.Background(), &protocol.NotifyChannel{Type: "pushdeer", Config: map[string]string{"server": srv.URL}}, "t", "x"); err == nil {
		t.Error("expected validation error without pushkey")
	}
}
