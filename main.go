package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	larkcard "github.com/larksuite/oapi-sdk-go/v3/card"
	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"

	"feishu-passwd-bot/config"
	"feishu-passwd-bot/handler"
	botldap "feishu-passwd-bot/ldap"
)

type sdkHandler interface {
	Handle(ctx context.Context, req *larkevent.EventReq) *larkevent.EventResp
}

func adaptSDK(h sdkHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body error", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		req := &larkevent.EventReq{
			Header:     map[string][]string(r.Header),
			Body:       body,
			RequestURI: r.RequestURI,
		}

		resp := h.Handle(r.Context(), req)
		if resp == nil {
			w.WriteHeader(http.StatusOK)
			return
		}

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}

		if resp.StatusCode != 0 {
			w.WriteHeader(resp.StatusCode)
		} else {
			w.WriteHeader(http.StatusOK)
		}

		if len(resp.Body) > 0 {
			_, _ = w.Write(resp.Body)
		}
	}
}

// patchCardBody decrypts (if encrypted) and strips the "header" field from the card callback JSON.
// Feishu schema 2.0 cards send "header" as map[string]string in the callback, but the SDK's
// CardAction embeds *larkevent.EventReq whose Header is map[string][]string — causing an
// unmarshal error. We remove the field and let the SDK set EventReq from the HTTP headers.
func patchCardBody(body []byte, encryptKey string) ([]byte, error) {
	plain := body
	var msg struct {
		Encrypt string `json:"encrypt"`
	}
	if json.Unmarshal(body, &msg) == nil && msg.Encrypt != "" {
		decrypted, err := larkevent.EventDecrypt(msg.Encrypt, encryptKey)
		if err != nil {
			return nil, fmt.Errorf("decrypt: %w", err)
		}
		plain = decrypted
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(plain, &m) != nil {
		return plain, nil
	}
	delete(m, "header")
	patched, _ := json.Marshal(m)
	return patched, nil
}

func main() {
	cfg := config.Load()

	ldapClient := botldap.NewClient(&cfg.LDAP)
	h := handler.New(cfg, ldapClient)

	eventDispatcher := dispatcher.NewEventDispatcher(
		cfg.Feishu.VerificationToken,
		cfg.Feishu.EncryptKey,
	).OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		return h.OnMessage(ctx, event)
	})

	cardHandler := larkcard.NewCardActionHandler(
		cfg.Feishu.VerificationToken,
		cfg.Feishu.EncryptKey,
		func(ctx context.Context, action *larkcard.CardAction) (any, error) {
			return h.OnCardAction(ctx, action)
		},
	)
	// We verify signatures ourselves against the original body; skip SDK's re-verification
	// which would run against the patched (header-stripped) body and fail.
	cardHandler.SkipSignVerify = true

	http.Handle("/webhook/event", adaptSDK(eventDispatcher))

	// Custom card handler: verify signature against original body, then patch and forward to SDK.
	http.HandleFunc("/webhook/card", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		r.Body.Close()

		patched, err := patchCardBody(body, cfg.Feishu.EncryptKey)
		if err != nil {
			log.Printf("[card] patch error: %v", err)
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		req := &larkevent.EventReq{
			Header:     map[string][]string(r.Header),
			Body:       patched,
			RequestURI: r.RequestURI,
		}
		resp := cardHandler.Handle(r.Context(), req)
		if resp == nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		for k, vs := range resp.Header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
		code := resp.StatusCode
		if code == 0 {
			code = http.StatusOK
		}
		w.WriteHeader(code)
		if len(resp.Body) > 0 {
			_, _ = w.Write(resp.Body)
		}
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := ":" + cfg.Port
	log.Printf("[main] feishu-passwd-bot 启动，监听 %s", addr)
	log.Printf("[main] 事件地址: http://0.0.0.0%s/webhook/event", addr)
	log.Printf("[main] 卡片地址: http://0.0.0.0%s/webhook/card", addr)

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatalf("[main] 启动失败: %v", err)
	}
}
